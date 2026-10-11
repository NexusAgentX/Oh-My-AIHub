package gateway

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

// Channel statistics live in process memory (ADR-0040, ADR-0039): ranking and
// the model pages need each channel's recent attempts, successes and
// first-token time, and the gateway knows all three the moment an attempt
// ends, so it counts them there instead of aggregating the calls table once a
// minute.
//
// The window is the last 24 hours in 288 buckets of five minutes. A bucket is
// the unit that falls out of the window: the oldest bucket expires as a whole,
// so the window is between 23 h 55 min and 24 h long.

const (
	// statsBucketSeconds is the width of one bucket; statsBuckets of them make
	// the 24-hour window.
	statsBucketSeconds = 300
	statsBuckets       = 288

	// ttftBinsPerOctave and ttftMaxMS shape the first-token histogram: bins grow
	// geometrically, eight to every doubling, from 1 ms up to ttftMaxMS (17.5
	// minutes, above the longest first-byte timeout of 600 s). Everything slower
	// shares the last bin.
	ttftBinsPerOctave = 8
	ttftMaxMS         = 1 << 20
)

// Thresholds of the 4xx anomaly rule (Epic #360, G4b). They are the first
// values chosen, kept together so they can be tuned in one place.
const (
	// anomalyMinSamples is how many attempts a channel needs on a model, and
	// the channels it is compared with too, before the share of 4xx answers
	// counts.
	anomalyMinSamples = 20
	// anomalyMinRatio is the share of 4xx answers from which a channel on a
	// model looks broken.
	anomalyMinRatio = 0.8
	// anomalyFactor and anomalyOthersBelow say what "clearly worse than the
	// other channels of the model" means: at least anomalyFactor times their
	// median 4xx share, or every one of them below anomalyOthersBelow.
	anomalyFactor      = 3.0
	anomalyOthersBelow = 0.2
	// anomalyRenotifyAfter is the shortest time between two anomalous_client_errors
	// events of one channel on one model, so a ratio hovering at the threshold
	// does not flood the event log.
	anomalyRenotifyAfter = time.Hour
)

// ttftEdges[i] is the smallest first-token time, in milliseconds, that falls
// into bin i. Bin 0 is 0 ms; the edges are the integers ceil(2^(k/8)) without
// repeats, so the bins below about 12 ms hold one value each and the wider ones
// are about 9% wide. The database bins the warm-up rows with these same edges
// (width_bucket over this array), so memory and database agree to the bin.
var ttftEdges = buildTTFTEdges()

func buildTTFTEdges() []int32 {
	edges := []int32{0}
	for k := 0; ; k++ {
		edge := int32(math.Ceil(math.Pow(2, float64(k)/ttftBinsPerOctave)))
		if edge > ttftMaxMS {
			return edges
		}
		if edge > edges[len(edges)-1] {
			edges = append(edges, edge)
		}
	}
}

// TTFTBinEdges returns the lower edge in milliseconds of every bin of the
// first-token histogram, ascending.
func TTFTBinEdges() []int32 { return slices.Clone(ttftEdges) }

// ttftBinOf is the bin a first-token time falls into.
func ttftBinOf(ms int) int {
	if ms <= 0 {
		return 0
	}
	index, found := slices.BinarySearch(ttftEdges, int32(min(ms, math.MaxInt32)))
	if !found {
		index--
	}
	return index
}

// ttftBinValue is the first-token time reported for a bin: the middle of the
// integers it holds. For a time of at least 12 ms that is within 4.6% of the
// time itself; below that bins hold one value and the result is exact.
func ttftBinValue(bin int) float64 {
	low := float64(ttftEdges[bin])
	if bin+1 >= len(ttftEdges) {
		return low
	}
	return (low + float64(ttftEdges[bin+1]-1)) / 2
}

// statsEpoch is the number of the five-minute bucket a time falls into.
func statsEpoch(at time.Time) int64 { return at.Unix() / statsBucketSeconds }

// slotOf is where a bucket lives in a ring of statsBuckets slots.
func slotOf(epoch int64) int { return int(epoch % statsBuckets) }

// ---- one channel ----

type statsSlot struct {
	epoch     int64 // bucket number; 0 for an empty slot
	attempts  uint32
	successes uint32
	ttft      []uint32 // first-token histogram, allocated on first use
	ttftN     uint32
}

// channelWindow is the rolling window of one channel. The totals are always
// the sums of its live slots, so reading the window costs one pass over the
// slots to expire the old ones, not a merge of 288 histograms.
type channelWindow struct {
	slots     [statsBuckets]statsSlot
	attempts  int64
	successes int64
	ttft      []int64
	ttftN     int64
}

func newChannelWindow() *channelWindow {
	return &channelWindow{ttft: make([]int64, len(ttftEdges))}
}

// drop empties a slot and takes its counts out of the totals.
func (w *channelWindow) drop(slot *statsSlot) {
	w.attempts -= int64(slot.attempts)
	w.successes -= int64(slot.successes)
	if slot.ttft != nil {
		for bin, count := range slot.ttft {
			w.ttft[bin] -= int64(count)
		}
		w.ttftN -= int64(slot.ttftN)
	}
	*slot = statsSlot{}
}

// add counts attempts and successes into bucket epoch, and ttftCount
// first-token times into histogram bin ttftBin (-1 for none).
func (w *channelWindow) add(epoch, attempts, successes int64, ttftBin int, ttftCount int64) {
	slot := &w.slots[slotOf(epoch)]
	if slot.epoch != epoch {
		if slot.epoch > epoch {
			return // the slot already holds a newer bucket: this one is out of the window
		}
		w.drop(slot)
		slot.epoch = epoch
	}
	slot.attempts += uint32(attempts)
	slot.successes += uint32(successes)
	w.attempts += attempts
	w.successes += successes
	if ttftBin >= 0 && ttftCount > 0 {
		if slot.ttft == nil {
			slot.ttft = make([]uint32, len(ttftEdges))
		}
		slot.ttft[ttftBin] += uint32(ttftCount)
		slot.ttftN += uint32(ttftCount)
		w.ttft[ttftBin] += ttftCount
		w.ttftN += ttftCount
	}
}

// expire drops the buckets that are older than the window ending at bucket now.
func (w *channelWindow) expire(now int64) {
	from := now - statsBuckets + 1
	for index := range w.slots {
		if slot := &w.slots[index]; slot.epoch != 0 && slot.epoch < from {
			w.drop(slot)
		}
	}
}

// ttftP50 is the median first-token time, read off the histogram the way
// percentile_cont(0.5) reads sorted samples: the mean of the two middle ones.
func (w *channelWindow) ttftP50() *float64 {
	if w.ttftN == 0 {
		return nil
	}
	low := ttftBinValue(binAt(w.ttft, (w.ttftN-1)/2))
	high := ttftBinValue(binAt(w.ttft, w.ttftN/2))
	median := (low + high) / 2
	return &median
}

// binAt is the bin that holds the sample of the given rank (counted from 0) when
// the samples are sorted.
func binAt(counts []int64, rank int64) int {
	var seen int64
	for bin, count := range counts {
		seen += count
		if seen > rank {
			return bin
		}
	}
	return len(counts) - 1
}

// ---- one channel on one model ----

type pairSlot struct {
	epoch  int64
	total  uint32
	client uint32
}

// pairWindow counts, for one channel on one model, the attempts and how many
// of them the upstream answered with a 4xx (client_error).
type pairWindow struct {
	slots  [statsBuckets]pairSlot
	total  int64
	client int64
}

func (w *pairWindow) add(epoch, total, client int64) {
	slot := &w.slots[slotOf(epoch)]
	if slot.epoch != epoch {
		if slot.epoch > epoch {
			return
		}
		w.total -= int64(slot.total)
		w.client -= int64(slot.client)
		*slot = pairSlot{epoch: epoch}
	}
	slot.total += uint32(total)
	slot.client += uint32(client)
	w.total += total
	w.client += client
}

func (w *pairWindow) expire(now int64) {
	from := now - statsBuckets + 1
	for index := range w.slots {
		if slot := &w.slots[index]; slot.epoch != 0 && slot.epoch < from {
			w.total -= int64(slot.total)
			w.client -= int64(slot.client)
			*slot = pairSlot{}
		}
	}
}

// ---- the book ----

type pairKey struct{ model, channel string }

// StatsBook holds the rolling windows of every channel, and of every channel
// on every model. All methods are safe for concurrent use. A window with
// nothing in it is dropped when the book is next read, so the memory follows
// the channels and models that were used in the last 24 hours.
type StatsBook struct {
	mutex    sync.Mutex
	channels map[string]*channelWindow
	pairs    map[pairKey]*pairWindow
}

func NewStatsBook() *StatsBook {
	return &StatsBook{channels: map[string]*channelWindow{}, pairs: map[pairKey]*pairWindow{}}
}

// Record counts one finished attempt of a call for modelID on a channel, at
// the time it ended. Attempts the client abandoned say nothing about the
// channel and are not counted. An attempt the upstream answered with a 4xx that
// is the request's own fault (client_error) is not a channel attempt either,
// but it is kept per model for the anomaly rule. The first-token time counts
// only for a completed attempt of a streaming call: a non-streaming call's
// first byte is the whole answer.
func (b *StatsBook) Record(channelID, modelID string, stream bool, attempt Attempt, at time.Time) {
	epoch := statsEpoch(at)
	if epoch <= 0 || attempt.EndReason == EndClientDisconnected {
		return
	}
	clientError := attempt.EndReason == EndClientError
	success := attempt.EndReason == EndCompleted
	ttftBin := -1
	if success && stream && attempt.TTFTMS != nil {
		ttftBin = ttftBinOf(*attempt.TTFTMS)
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if !clientError {
		window := b.channels[channelID]
		if window == nil {
			window = newChannelWindow()
			b.channels[channelID] = window
		}
		var successes, ttftCount int64
		if success {
			successes = 1
		}
		if ttftBin >= 0 {
			ttftCount = 1
		}
		window.add(epoch, 1, successes, ttftBin, ttftCount)
	}
	if modelID != "" {
		pair := b.pairs[pairKey{modelID, channelID}]
		if pair == nil {
			pair = &pairWindow{}
			b.pairs[pairKey{modelID, channelID}] = pair
		}
		var client int64
		if clientError {
			client = 1
		}
		pair.add(epoch, 1, client)
	}
}

// AttemptHistoryRow is a group of past attempts, as the database reports them
// at startup: those of one channel on one model that belong to one five-minute
// bucket (Bucket, the unix time divided by 300) and, for completed attempts of
// streaming calls, one first-token bin of TTFTBinEdges (-1 for the rest).
type AttemptHistoryRow struct {
	ChannelID string
	ModelID   string
	Bucket    int64
	TTFTBin   int
	// Counted are the attempts that count towards a channel's success rate
	// (everything but client_disconnected and client_error), Successes those
	// that completed, ClientErrors the client_error ones.
	Counted      int64
	Successes    int64
	ClientErrors int64
	// BeforeEditAttempts (Counted plus ClientErrors) and BeforeEditClientErrors
	// are the part of the row that belongs to calls started before the channel
	// was last edited. They stay in the success rate and the first-token time
	// but not in the 4xx anomaly rule, which starts afresh after an edit (see
	// StatsBook.ResetAnomalyStats). Zero, the default, leaves everything in.
	BeforeEditAttempts     int64
	BeforeEditClientErrors int64
}

// Load adds attempts that ended before this process began counting. Buckets
// outside the 24-hour window ending at now are ignored.
func (b *StatsBook) Load(rows []AttemptHistoryRow, now time.Time) {
	current := statsEpoch(now)
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for _, row := range rows {
		if row.Bucket <= 0 || row.Bucket < current-statsBuckets+1 || row.Counted+row.ClientErrors <= 0 {
			continue
		}
		if row.Counted > 0 {
			window := b.channels[row.ChannelID]
			if window == nil {
				window = newChannelWindow()
				b.channels[row.ChannelID] = window
			}
			ttftBin, ttftCount := -1, int64(0)
			if row.TTFTBin >= 0 && row.TTFTBin < len(ttftEdges) {
				ttftBin, ttftCount = row.TTFTBin, row.Successes
			}
			window.add(row.Bucket, row.Counted, row.Successes, ttftBin, ttftCount)
		}
		if total := row.Counted + row.ClientErrors - row.BeforeEditAttempts; row.ModelID != "" && total > 0 {
			key := pairKey{row.ModelID, row.ChannelID}
			pair := b.pairs[key]
			if pair == nil {
				pair = &pairWindow{}
				b.pairs[key] = pair
			}
			pair.add(row.Bucket, total, max(row.ClientErrors-row.BeforeEditClientErrors, 0))
		}
	}
}

// ResetAnomalyStats forgets what the 4xx anomaly rule has counted for a channel
// on every model, so the rule judges the channel afresh from its next attempts.
// The channel's own window (attempts, successes, first-token times) is not
// touched: it says how the channel performs, not whether its configuration is
// wrong. The engine calls it when the channel has been edited (Engine.ChannelEdited).
func (b *StatsBook) ResetAnomalyStats(channelID string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for key := range b.pairs {
		if key.channel == channelID {
			delete(b.pairs, key)
		}
	}
}

// pairSums is what the anomaly rule needs to know about one channel on one model.
type pairSums struct {
	channel string
	total   int64
	client  int64
}

// bookView is one consistent read of the book.
type bookView struct {
	channels map[string]ChannelStats
	models   map[string][]pairSums
}

// view expires what has left the window ending at now and reads the rest.
func (b *StatsBook) view(now time.Time) bookView {
	current := statsEpoch(now)
	b.mutex.Lock()
	defer b.mutex.Unlock()
	view := bookView{channels: make(map[string]ChannelStats, len(b.channels)), models: map[string][]pairSums{}}
	for id, window := range b.channels {
		window.expire(current)
		if window.attempts <= 0 {
			delete(b.channels, id)
			continue
		}
		view.channels[id] = ChannelStats{Attempts: window.attempts, Successes: window.successes, TTFTP50MS: window.ttftP50()}
	}
	for key, window := range b.pairs {
		window.expire(current)
		if window.total <= 0 {
			delete(b.pairs, key)
			continue
		}
		view.models[key.model] = append(view.models[key.model], pairSums{channel: key.channel, total: window.total, client: window.client})
	}
	return view
}

// ---- the 4xx anomaly rule ----

// Anomaly says that a channel answers a model with a 4xx almost every time
// while the other channels of that model do not, which points at the channel's
// configuration rather than at the clients (Epic #360, G4b).
type Anomaly struct {
	Model   string
	Channel string
	// Total attempts and how many of them were answered with a 4xx.
	Total  int64
	Client int64
}

// Ratio is the share of 4xx answers.
func (a Anomaly) Ratio() float64 { return float64(a.Client) / float64(a.Total) }

// Reason is the text of the channel event.
func (a Anomaly) Reason() string {
	return fmt.Sprintf("%s: 4xx %d/%d (%d%%)", a.Model, a.Client, a.Total, int(math.Round(a.Ratio()*100)))
}

// detectAnomalies applies the rule to every model: a channel is anomalous on a
// model when it has at least anomalyMinSamples attempts there, at least
// anomalyMinRatio of them 4xx, and that share is clearly above the other
// channels of the model, which means at least anomalyFactor times their median
// share or every one of them below anomalyOthersBelow. Only other channels with
// anomalyMinSamples attempts of their own are compared; with none to compare
// the second condition holds, so a model whose every request goes to one broken
// channel (the cheapest one, say: a 4xx is never retried) is still caught. The
// result is by model, then channel.
func detectAnomalies(models map[string][]pairSums) map[string]map[string]Anomaly {
	var found map[string]map[string]Anomaly
	for model, channels := range models {
		for _, subject := range channels {
			if subject.total < anomalyMinSamples {
				continue
			}
			ratio := float64(subject.client) / float64(subject.total)
			if ratio < anomalyMinRatio {
				continue
			}
			var others []float64
			for _, other := range channels {
				if other.channel != subject.channel && other.total >= anomalyMinSamples {
					others = append(others, float64(other.client)/float64(other.total))
				}
			}
			if !clearlyAbove(ratio, others) {
				continue
			}
			if found == nil {
				found = map[string]map[string]Anomaly{}
			}
			if found[model] == nil {
				found[model] = map[string]Anomaly{}
			}
			found[model][subject.channel] = Anomaly{Model: model, Channel: subject.channel, Total: subject.total, Client: subject.client}
		}
	}
	return found
}

// clearlyAbove reports whether ratio stands out from the 4xx shares of the other
// channels: they are all below anomalyOthersBelow (which includes there being
// none), or it is at least anomalyFactor times their median.
func clearlyAbove(ratio float64, others []float64) bool {
	if len(others) == 0 {
		return true
	}
	highest := slices.Max(others)
	if highest < anomalyOthersBelow {
		return true
	}
	sort.Float64s(others)
	median := others[len(others)/2]
	if len(others)%2 == 0 {
		median = (others[len(others)/2-1] + others[len(others)/2]) / 2
	}
	return ratio+1e-9 >= anomalyFactor*median
}
