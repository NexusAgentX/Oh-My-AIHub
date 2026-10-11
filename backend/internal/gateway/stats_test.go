package gateway

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

// bucketStart is the start of five-minute bucket number epoch.
func bucketStart(epoch int64) time.Time { return time.Unix(epoch*statsBucketSeconds, 0) }

// someEpoch is a bucket of a plain day, far from any boundary of the ring.
const someEpoch int64 = 5_900_000

func attemptOf(reason string, ttft int) Attempt {
	attempt := Attempt{EndReason: reason}
	if ttft >= 0 {
		attempt.TTFTMS = &ttft
	}
	return attempt
}

func read(book *StatsBook, now time.Time) map[string]ChannelStats { return book.view(now).channels }

func TestHistogramEdgesAreAscendingAndCoverTheTimeouts(t *testing.T) {
	if ttftEdges[0] != 0 || ttftEdges[1] != 1 {
		t.Fatalf("edges start %v, want 0, 1", ttftEdges[:3])
	}
	if !sort.SliceIsSorted(ttftEdges, func(i, j int) bool { return ttftEdges[i] < ttftEdges[j] }) || len(slices.Compact(slices.Clone(ttftEdges))) != len(ttftEdges) {
		t.Fatal("edges are not strictly ascending")
	}
	// The longest first-byte timeout the channel settings allow is 600 s.
	if last := ttftEdges[len(ttftEdges)-1]; last != ttftMaxMS || ttftBinOf(600_000) == len(ttftEdges)-1 {
		t.Fatalf("last edge %d, bin of 600 s %d of %d", last, ttftBinOf(600_000), len(ttftEdges))
	}
	if got := ttftBinOf(-5); got != 0 {
		t.Fatalf("negative time in bin %d", got)
	}
	if got := ttftBinOf(math.MaxInt); got != len(ttftEdges)-1 {
		t.Fatalf("huge time in bin %d, want the last", got)
	}
	// A time sits in the bin whose edges enclose it.
	for _, ms := range []int{0, 1, 2, 15, 16, 17, 18, 100, 420, 12345, ttftMaxMS - 1, ttftMaxMS, ttftMaxMS + 1} {
		bin := ttftBinOf(ms)
		if int(ttftEdges[bin]) > ms && ms > 0 || bin+1 < len(ttftEdges) && int(ttftEdges[bin+1]) <= ms {
			t.Errorf("%d ms in bin %d [%d, %d)", ms, bin, ttftEdges[bin], ttftEdges[min(bin+1, len(ttftEdges)-1)])
		}
	}
	// The returned copy is the caller's own.
	copied := TTFTBinEdges()
	copied[1] = 99
	if ttftEdges[1] != 1 {
		t.Fatal("TTFTBinEdges returned the shared slice")
	}
}

// A first-token time is reported as the middle of its bin: exact below 16 ms,
// within 4.6% above (the worst case is 4.53%, at 6317 ms).
func TestHistogramValueErrorIsBounded(t *testing.T) {
	const bound = 0.046
	worst := 0.0
	for ms := 1; ms <= 2*ttftMaxMS; ms++ {
		got := ttftBinValue(ttftBinOf(ms))
		if ms < 16 && got != float64(ms) {
			t.Fatalf("%d ms reported as %v, want exact", ms, got)
		}
		if ms > ttftMaxMS {
			continue // the last bin holds everything above and reports its lower edge
		}
		worst = max(worst, math.Abs(got-float64(ms))/float64(ms))
	}
	if worst > bound {
		t.Fatalf("worst relative error %.4f exceeds %.3f", worst, bound)
	}
	t.Logf("worst relative error of a single time: %.4f", worst)
}

// The median of many samples, read off the histogram, is within the same bound
// of the exact median (percentile_cont(0.5), the mean of the two middle ones
// for an even count), whatever the shape of the data.
func TestMedianFromHistogramMatchesTheExactMedian(t *testing.T) {
	const bound = 0.046
	random := rand.New(rand.NewPCG(1, 2))
	shapes := map[string]func() int{
		"log-normal around 400 ms": func() int { return int(math.Exp(random.NormFloat64()*0.6 + math.Log(400))) },
		"uniform 100-5000 ms":      func() int { return 100 + random.IntN(4900) },
		"two modes, 80 and 3000":   func() int { return []int{80, 3000}[random.IntN(2)] + random.IntN(40) },
		"fast and nearly equal":    func() int { return 6 + random.IntN(4) },
	}
	for name, sample := range shapes {
		for _, count := range []int{1, 2, 3, 10, 101, 1000, 5000} {
			book := NewStatsBook()
			values := make([]float64, 0, count)
			for range count {
				ms := sample()
				values = append(values, float64(ms))
				book.Record("c", "m", true, attemptOf(EndCompleted, ms), bucketStart(someEpoch))
			}
			sort.Float64s(values)
			exact := values[(count-1)/2]
			if count%2 == 0 {
				exact = (values[count/2-1] + values[count/2]) / 2
			}
			stats := read(book, bucketStart(someEpoch))["c"]
			if stats.TTFTP50MS == nil {
				t.Fatalf("%s, %d samples: no median", name, count)
			}
			if err := math.Abs(*stats.TTFTP50MS-exact) / exact; err > bound {
				t.Errorf("%s, %d samples: median %.1f against exact %.1f (%.1f%% off)", name, count, *stats.TTFTP50MS, exact, err*100)
			}
		}
	}
}

func TestWindowRollsAndBucketsExpireWhole(t *testing.T) {
	book := NewStatsBook()
	book.Record("c", "m", false, attemptOf(EndCompleted, -1), bucketStart(someEpoch).Add(2*time.Minute)) // first bucket
	book.Record("c", "m", false, attemptOf(EndUpstreamError, -1), bucketStart(someEpoch+100))
	book.Record("c", "m", false, attemptOf(EndCompleted, -1), bucketStart(someEpoch+100).Add(4*time.Minute+59*time.Second))

	at := func(epoch int64, offset time.Duration) ChannelStats {
		return read(book, bucketStart(epoch).Add(offset))["c"]
	}
	if got := at(someEpoch+100, 0); got.Attempts != 3 || got.Successes != 2 {
		t.Fatalf("in the window: %+v", got)
	}
	// The window ends with the bucket of now and holds the 287 before it: the
	// first bucket is still in until the last second before epoch+288 begins.
	if got := at(someEpoch+287, 5*time.Minute-time.Second); got.Attempts != 3 {
		t.Fatalf("last second of the first bucket's window: %+v", got)
	}
	if got := at(someEpoch+288, 0); got.Attempts != 2 || got.Successes != 1 {
		t.Fatalf("the first bucket is out from its 288th successor on: %+v", got)
	}
	if got := at(someEpoch+387, 5*time.Minute-time.Second); got.Attempts != 2 {
		t.Fatalf("last second of the second bucket's window: %+v", got)
	}
	if got, found := read(book, bucketStart(someEpoch+388))["c"]; found || got.Attempts != 0 {
		t.Fatalf("an empty window still reports the channel: %+v", got)
	}
	if len(book.channels) != 0 || len(book.pairs) != 0 {
		t.Fatalf("empty windows are kept: %d channels, %d pairs", len(book.channels), len(book.pairs))
	}
}

func TestRecordingIntoAnOccupiedSlotReplacesTheExpiredBucket(t *testing.T) {
	book := NewStatsBook()
	first := 120
	book.Record("c", "m", true, attemptOf(EndCompleted, first), bucketStart(someEpoch))
	book.Record("c", "m", true, attemptOf(EndCompleted, first), bucketStart(someEpoch))
	// 288 buckets later the same slot is taken by the new bucket; the old one
	// leaves the totals, histogram included.
	later := 3000
	book.Record("c", "m", true, attemptOf(EndCompleted, later), bucketStart(someEpoch+statsBuckets))
	stats := read(book, bucketStart(someEpoch+statsBuckets))["c"]
	if stats.Attempts != 1 || stats.Successes != 1 || stats.TTFTP50MS == nil || math.Abs(*stats.TTFTP50MS-3000)/3000 > 0.046 {
		t.Fatalf("stats = %+v", stats)
	}
	// A late sample for a bucket the slot has already moved past is dropped.
	book.Record("c", "m", true, attemptOf(EndUpstreamError, -1), bucketStart(someEpoch))
	if got := read(book, bucketStart(someEpoch+statsBuckets))["c"]; got.Attempts != 1 {
		t.Fatalf("late sample counted: %+v", got)
	}
	// Times that cannot be a bucket are ignored.
	book.Record("c", "m", true, attemptOf(EndUpstreamError, -1), time.Time{})
	if got := read(book, bucketStart(someEpoch+statsBuckets))["c"]; got.Attempts != 1 {
		t.Fatalf("zero time counted: %+v", got)
	}
}

func TestWhatCountsTowardsAChannelsHealth(t *testing.T) {
	now := bucketStart(someEpoch)
	record := func(book *StatsBook, stream bool, reason string, ttft int) {
		book.Record("c", "m", stream, attemptOf(reason, ttft), now)
	}

	book := NewStatsBook()
	record(book, true, EndCompleted, 300)
	record(book, true, EndCompleted, 900)
	record(book, false, EndCompleted, 5000)        // a non-streaming first byte is the whole answer
	record(book, true, EndUpstreamError, -1)       // failed: an attempt, not a success
	record(book, true, EndInterrupted, 250)        // cut short after the first byte: first-token time of a failed attempt
	record(book, true, EndTimeoutTTFT, -1)         // failed
	record(book, true, EndClientDisconnected, 100) // the client left: nothing to say about the channel
	record(book, false, EndClientDisconnected, -1) // same
	record(book, true, EndClientError, -1)         // the request's own fault: not a channel attempt either
	record(book, false, EndClientError, -1)        // same
	stats := read(book, now)["c"]
	if stats.Attempts != 6 || stats.Successes != 3 {
		t.Fatalf("attempts %d successes %d, want 6 and 3", stats.Attempts, stats.Successes)
	}
	// Only the two completed streaming attempts feed the median: 300 and 900.
	if stats.TTFTP50MS == nil {
		t.Fatal("no first-token median")
	}
	want := (ttftBinValue(ttftBinOf(300)) + ttftBinValue(ttftBinOf(900))) / 2
	if *stats.TTFTP50MS != want {
		t.Fatalf("median %v, want %v from the two completed streams", *stats.TTFTP50MS, want)
	}

	// A channel that has served only non-streaming calls has no first-token figure.
	plain := NewStatsBook()
	record(plain, false, EndCompleted, 800)
	record(plain, false, EndCompleted, 900)
	if got := read(plain, now)["c"]; got.Attempts != 2 || got.TTFTP50MS != nil {
		t.Fatalf("non-streaming only: %+v", got)
	}

	// A channel that has only had attempts nobody counts is not listed at all,
	// but its model still shows the 4xx.
	quiet := NewStatsBook()
	record(quiet, true, EndClientDisconnected, -1)
	record(quiet, true, EndClientError, -1)
	view := quiet.view(now)
	if len(view.channels) != 0 {
		t.Fatalf("channels = %v", view.channels)
	}
	if sums := view.models["m"]; len(sums) != 1 || sums[0].total != 1 || sums[0].client != 1 {
		t.Fatalf("model sums = %+v, want one 4xx attempt (a client_disconnected is not one)", sums)
	}
}

func TestModelCountsKeepFourXXPerChannelAndModel(t *testing.T) {
	book := NewStatsBook()
	now := bucketStart(someEpoch)
	for range 7 {
		book.Record("a", "m1", false, attemptOf(EndClientError, -1), now)
	}
	for range 3 {
		book.Record("a", "m1", false, attemptOf(EndCompleted, -1), now)
	}
	book.Record("a", "m2", false, attemptOf(EndUpstreamError, -1), now)
	book.Record("b", "m1", false, attemptOf(EndCompleted, -1), now)
	book.Record("b", "", false, attemptOf(EndCompleted, -1), now) // no model: only the channel counts

	view := book.view(now)
	got := map[string][2]int64{}
	for model, sums := range view.models {
		for _, sum := range sums {
			got[model+"/"+sum.channel] = [2]int64{sum.total, sum.client}
		}
	}
	want := map[string][2]int64{"m1/a": {10, 7}, "m2/a": {1, 0}, "m1/b": {1, 0}}
	if len(got) != len(want) {
		t.Fatalf("model sums = %v, want %v", got, want)
	}
	for key, counts := range want {
		if got[key] != counts {
			t.Errorf("%s = %v, want %v", key, got[key], counts)
		}
	}
	if view.channels["a"].Attempts != 4 || view.channels["b"].Attempts != 2 {
		t.Fatalf("channels = %+v", view.channels)
	}
}

// naiveWindow recomputes a channel's statistics from the events themselves.
type naiveEvent struct {
	epoch   int64
	success bool
	ttft    int // -1 for none
}

func naiveStats(events []naiveEvent, now int64) (attempts, successes int64, median *float64) {
	var times []float64
	for _, event := range events {
		if event.epoch < now-statsBuckets+1 {
			continue
		}
		attempts++
		if event.success {
			successes++
		}
		if event.ttft >= 0 {
			times = append(times, ttftBinValue(ttftBinOf(event.ttft)))
		}
	}
	if len(times) == 0 {
		return attempts, successes, nil
	}
	sort.Float64s(times)
	value := (times[(len(times)-1)/2] + times[len(times)/2]) / 2
	return attempts, successes, &value
}

// The running totals always equal what recomputing from the events gives, over
// a long random history with the clock moving forward through several windows.
func TestRunningTotalsEqualARecomputation(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 8))
	book := NewStatsBook()
	events := map[string][]naiveEvent{}
	channels := []string{"a", "b", "c"}
	epoch := someEpoch
	for step := range 3000 {
		epoch += int64(random.IntN(4)) // 0 to 3 buckets forward: about 4500 buckets, over 15 windows
		id := channels[random.IntN(len(channels))]
		stream := random.IntN(3) > 0
		reason := []string{EndCompleted, EndCompleted, EndUpstreamError, EndInterrupted, EndClientDisconnected, EndClientError}[random.IntN(6)]
		ttft := -1
		if random.IntN(4) > 0 {
			ttft = 1 + random.IntN(8000)
		}
		book.Record(id, "m", stream, attemptOf(reason, ttft), bucketStart(epoch).Add(time.Duration(random.IntN(300))*time.Second))
		if reason != EndClientDisconnected && reason != EndClientError {
			counted := ttft
			if !(reason == EndCompleted && stream) {
				counted = -1
			}
			events[id] = append(events[id], naiveEvent{epoch: epoch, success: reason == EndCompleted, ttft: counted})
		}
		if step%25 == 0 {
			view := book.view(bucketStart(epoch).Add(time.Second))
			for _, id := range channels {
				attempts, successes, median := naiveStats(events[id], epoch)
				got, listed := view.channels[id]
				if (attempts > 0) != listed {
					t.Fatalf("step %d channel %s: listed = %v with %d attempts in the window", step, id, listed, attempts)
				}
				if got.Attempts != attempts || got.Successes != successes {
					t.Fatalf("step %d channel %s: %d/%d, want %d/%d", step, id, got.Successes, got.Attempts, successes, attempts)
				}
				if (median == nil) != (got.TTFTP50MS == nil) || median != nil && *median != *got.TTFTP50MS {
					t.Fatalf("step %d channel %s: median %v, want %v", step, id, got.TTFTP50MS, median)
				}
			}
		}
	}
}

func TestStatsBookIsSafeForConcurrentUse(t *testing.T) {
	book := NewStatsBook()
	now := bucketStart(someEpoch)
	const writers, perWriter = 16, 2000
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			id := []string{"a", "b", "c", "d"}[w%4]
			for i := range perWriter {
				reason := EndCompleted
				if i%4 == 3 {
					reason = EndUpstreamError
				}
				book.Record(id, "m", true, attemptOf(reason, 100+i%500), now)
			}
		})
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				book.view(now)
			}
		}
	})
	wg.Wait()
	close(stop)
	readers.Wait()

	view := book.view(now)
	for _, id := range []string{"a", "b", "c", "d"} {
		stats := view.channels[id]
		if stats.Attempts != writers/4*perWriter || stats.Successes != stats.Attempts*3/4 {
			t.Errorf("channel %s: %d/%d", id, stats.Successes, stats.Attempts)
		}
	}
}

func TestLoadSeedsTheWindowFromTheDatabaseRows(t *testing.T) {
	now := bucketStart(someEpoch + 500).Add(time.Minute)
	book := NewStatsBook()
	bin := ttftBinOf(400)
	book.Load([]AttemptHistoryRow{
		{ChannelID: "a", ModelID: "m", Bucket: someEpoch + 500, TTFTBin: bin, Counted: 4, Successes: 4},
		{ChannelID: "a", ModelID: "m", Bucket: someEpoch + 500, TTFTBin: -1, Counted: 3, Successes: 1, ClientErrors: 2},
		{ChannelID: "a", ModelID: "m", Bucket: someEpoch + 500 - 287, TTFTBin: -1, Counted: 5, Successes: 5}, // the oldest bucket still in
		{ChannelID: "a", ModelID: "m", Bucket: someEpoch + 500 - 288, TTFTBin: -1, Counted: 100, Successes: 100},
		{ChannelID: "b", ModelID: "m", Bucket: someEpoch + 500, TTFTBin: -1, Counted: 0, ClientErrors: 6}, // only 4xx: a model figure, no channel health
		{ChannelID: "b", ModelID: "m", Bucket: 0, TTFTBin: -1, Counted: 9},
	}, now)
	// Attempts counted after the load add to it.
	book.Record("a", "m", true, attemptOf(EndCompleted, 400), now)

	view := book.view(now)
	a := view.channels["a"]
	if a.Attempts != 4+3+5+1 || a.Successes != 4+1+5+1 || a.TTFTP50MS == nil || *a.TTFTP50MS != ttftBinValue(bin) {
		t.Fatalf("channel a = %+v", a)
	}
	if _, listed := view.channels["b"]; listed {
		t.Fatalf("channel b listed: %+v", view.channels["b"])
	}
	got := map[string][2]int64{}
	for _, sum := range view.models["m"] {
		got[sum.channel] = [2]int64{sum.total, sum.client}
	}
	// a: 4 + (3+2) + 5 + 1 attempts, 2 of them 4xx. b: 6 attempts, all 4xx.
	if got["a"] != [2]int64{15, 2} || got["b"] != [2]int64{6, 6} {
		t.Fatalf("model sums = %v", got)
	}
}

// The part of a row that belongs to calls started before the channel's last edit
// stays in the channel's health but not in the 4xx anomaly rule.
func TestLoadLeavesTheAttemptsBeforeAnEditOutOfTheAnomalyRule(t *testing.T) {
	now := bucketStart(someEpoch).Add(time.Minute)
	book := NewStatsBook()
	book.Load([]AttemptHistoryRow{
		// 10 attempts completed and 30 were 4xx; of the 40, 25 (20 of them 4xx) began before the edit.
		{ChannelID: "a", ModelID: "m", Bucket: someEpoch, TTFTBin: -1, Counted: 10, Successes: 9, ClientErrors: 30, BeforeEditAttempts: 25, BeforeEditClientErrors: 20},
		// Everything in this row is from before the edit: no figure for the model at all.
		{ChannelID: "a", ModelID: "m2", Bucket: someEpoch, TTFTBin: -1, Counted: 5, Successes: 1, ClientErrors: 35, BeforeEditAttempts: 40, BeforeEditClientErrors: 35},
		// A channel never edited since (no before-edit figures) is counted whole.
		{ChannelID: "b", ModelID: "m", Bucket: someEpoch, TTFTBin: -1, Counted: 10, ClientErrors: 30},
	}, now)

	view := book.view(now)
	if a := view.channels["a"]; a.Attempts != 15 || a.Successes != 10 {
		t.Fatalf("the success rate keeps every attempt: %+v", a)
	}
	got := map[string][2]int64{}
	for model, byChannel := range view.models {
		for _, sum := range byChannel {
			got[model+"/"+sum.channel] = [2]int64{sum.total, sum.client}
		}
	}
	want := map[string][2]int64{"m/a": {15, 10}, "m/b": {40, 30}}
	if len(got) != len(want) || got["m/a"] != want["m/a"] || got["m/b"] != want["m/b"] {
		t.Fatalf("model sums = %v, want %v (m2 has nothing left after the edit)", got, want)
	}
}

func TestResetAnomalyStatsClearsOneChannelsModelFiguresOnly(t *testing.T) {
	now := bucketStart(someEpoch).Add(time.Minute)
	book := NewStatsBook()
	for _, channelID := range []string{"a", "b"} {
		for _, model := range []string{"m1", "m2"} {
			for range 3 {
				book.Record(channelID, model, true, attemptOf(EndCompleted, 100), now)
			}
			book.Record(channelID, model, true, attemptOf(EndClientError, -1), now)
		}
	}
	before := book.view(now)
	if len(before.models["m1"]) != 2 || len(before.models["m2"]) != 2 {
		t.Fatalf("setup: %+v", before.models)
	}

	book.ResetAnomalyStats("a")
	after := book.view(now)
	for _, model := range []string{"m1", "m2"} {
		if len(after.models[model]) != 1 || after.models[model][0].channel != "b" || after.models[model][0].total != 4 || after.models[model][0].client != 1 {
			t.Errorf("model %s after the reset: %+v, want only b untouched", model, after.models[model])
		}
	}
	// Success rate and first-token time of a do not move.
	if got, want := after.channels["a"], before.channels["a"]; got.Attempts != want.Attempts || got.Successes != want.Successes ||
		got.TTFTP50MS == nil || want.TTFTP50MS == nil || *got.TTFTP50MS != *want.TTFTP50MS {
		t.Fatalf("channel a health = %+v, was %+v", got, want)
	}
	if got := after.channels["b"]; got.Attempts != 6 {
		t.Fatalf("channel b = %+v", got)
	}

	// It counts afresh from here, and resetting a channel the book never saw is harmless.
	book.Record("a", "m1", false, attemptOf(EndClientError, -1), now)
	book.ResetAnomalyStats("nobody")
	for _, sum := range book.view(now).models["m1"] {
		if sum.channel == "a" && (sum.total != 1 || sum.client != 1) {
			t.Fatalf("a on m1 after the reset: %+v", sum)
		}
	}
}

// ---- the anomaly rule ----

func sums(entries ...[3]int64) []pairSums {
	var result []pairSums
	for _, entry := range entries {
		result = append(result, pairSums{channel: string(rune('a' + entry[0])), total: entry[1], client: entry[2]})
	}
	return result
}

func flagged(models map[string][]pairSums) []string {
	var names []string
	for model, byChannel := range detectAnomalies(models) {
		for channel := range byChannel {
			names = append(names, model+"/"+channel)
		}
	}
	sort.Strings(names)
	return names
}

func TestAnomalyThresholds(t *testing.T) {
	cases := []struct {
		name    string
		channel []pairSums
		want    []string
	}{
		// The sample size: 20 attempts, not 19.
		{"20 samples, all 4xx, alone", sums([3]int64{0, 20, 20}), []string{"m/a"}},
		{"19 samples are too few", sums([3]int64{0, 19, 19}), nil},
		{"nothing at all", nil, nil},
		// The share: 80%, not 79%.
		{"exactly 80%", sums([3]int64{0, 20, 16}), []string{"m/a"}},
		{"just below 80%", sums([3]int64{0, 100, 79}), nil},
		{"above 80% in a big sample", sums([3]int64{0, 1000, 801}), []string{"m/a"}},
		// Against the others: every other channel below 20%.
		{"others all below 20%", sums([3]int64{0, 40, 36}, [3]int64{1, 50, 9}, [3]int64{2, 20, 3}), []string{"m/a"}},
		{"one of the others at 20% is not below it, but 3x the median still holds", sums([3]int64{0, 40, 36}, [3]int64{1, 50, 10}, [3]int64{2, 20, 1}), []string{"m/a"}},
		// ...or at least three times their median.
		{"3x the median of one other", sums([3]int64{0, 100, 90}, [3]int64{1, 100, 30}), []string{"m/a"}},
		{"just short of 3x the median of one other", sums([3]int64{0, 100, 89}, [3]int64{1, 100, 30}), nil},
		{"median of an odd number of others", sums([3]int64{0, 100, 90}, [3]int64{1, 100, 30}, [3]int64{2, 100, 25}, [3]int64{3, 100, 28}), []string{"m/a"}},
		{"an odd median just too high", sums([3]int64{0, 100, 83}, [3]int64{1, 100, 30}, [3]int64{2, 100, 25}, [3]int64{3, 100, 28}), nil},
		{"median of an even number averages the two in the middle", sums([3]int64{0, 100, 90}, [3]int64{1, 100, 10}, [3]int64{2, 100, 50}), []string{"m/a"}},
		{"an even median just too high", sums([3]int64{0, 100, 85}, [3]int64{1, 100, 10}, [3]int64{2, 100, 50}), nil},
		{"others as broken as it is", sums([3]int64{0, 100, 95}, [3]int64{1, 100, 90}), nil},
		{"everyone broken, no one stands out", sums([3]int64{0, 100, 100}, [3]int64{1, 100, 100}, [3]int64{2, 100, 100}), nil},
		// Other channels with too few attempts are not compared with.
		{"others with 19 attempts do not count, so none to compare with", sums([3]int64{0, 40, 40}, [3]int64{1, 19, 19}), []string{"m/a"}},
		{"others with 20 attempts do", sums([3]int64{0, 40, 40}, [3]int64{1, 20, 20}), nil},
		{"a broken channel with too few attempts is not flagged either", sums([3]int64{0, 19, 19}, [3]int64{1, 50, 0}), nil},
		// Two channels can both be flagged when each stands out from the rest.
		{"two bad among good", sums([3]int64{0, 50, 50}, [3]int64{1, 50, 45}, [3]int64{2, 50, 1}, [3]int64{3, 50, 2}, [3]int64{4, 50, 0}), []string{"m/a", "m/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flagged(map[string][]pairSums{"m": tc.channel})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("flagged %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnomalyIsJudgedPerModel(t *testing.T) {
	// The same channel is broken on one model and fine on another.
	got := flagged(map[string][]pairSums{
		"m1": sums([3]int64{0, 30, 30}, [3]int64{1, 30, 0}),
		"m2": sums([3]int64{0, 30, 0}, [3]int64{1, 30, 0}),
	})
	if !slices.Equal(got, []string{"m1/a"}) {
		t.Fatalf("flagged %v", got)
	}
	if detectAnomalies(nil) != nil {
		t.Fatal("no models should find nothing")
	}
}

func TestAnomalyReason(t *testing.T) {
	anomaly := Anomaly{Model: "gpt-4o", Channel: "c1", Total: 37, Client: 35}
	if got := anomaly.Reason(); got != "gpt-4o: 4xx 35/37 (95%)" {
		t.Fatalf("reason = %q", got)
	}
}
