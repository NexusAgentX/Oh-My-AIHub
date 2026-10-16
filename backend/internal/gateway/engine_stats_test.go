package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// ---- counting an attempt where it ends ----

func TestStreamBackCountsACompletedStreamWithItsFirstTokenTime(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, time.Second, chatContent, func(r *drainRun) {
		first := 240
		r.call.attempts[0].TTFTMS = &first
		r.call.modelID = "m"
	})
	run.waitWritten(1)
	run.send(chatRest)
	run.end()
	run.wait(2 * time.Second)

	stats := run.engine.Stats()["channel"]
	if stats.Attempts != 1 || stats.Successes != 1 || stats.TTFTP50MS == nil || *stats.TTFTP50MS != ttftBinValue(ttftBinOf(240)) {
		t.Fatalf("stats = %+v, want one completed attempt with a 240 ms first token", stats)
	}
}

func TestStreamBackCountsAnInterruptedStreamAsAFailureAndALeavingClientNotAtAll(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, time.Second, chatContent, func(r *drainRun) {
		first := 240
		r.call.attempts[0].TTFTMS = &first
		r.call.modelID = "m"
	})
	run.waitWritten(1)
	run.body.fail <- errors.New("connection reset")
	run.wait(2 * time.Second)
	// The reader failed and the client was there: the channel is to blame.
	stats := run.engine.Stats()["channel"]
	if stats.Attempts != 1 || stats.Successes != 0 || stats.TTFTP50MS != nil {
		t.Fatalf("interrupted stream: %+v", stats)
	}

	left := startDrainRun(t, channel.FormatOpenAIChat, time.Second, chatContent, func(r *drainRun) { r.call.modelID = "m" })
	left.waitWritten(1)
	left.leave()
	time.Sleep(50 * time.Millisecond) // the upstream is still talking
	left.send(chatRest)
	left.end()
	left.wait(2 * time.Second)
	if stats, listed := left.engine.Stats()["channel"]; listed {
		t.Fatalf("a client that left says nothing about the channel: %+v", stats)
	}
}

// ---- the published snapshot and the events ----

type announcement struct{ channel, kind, reason string }

type statsRig struct {
	engine *Engine
	book   *StatsBook
	clock  *time.Time
	mutex  sync.Mutex
	seen   []announcement
}

func newStatsRig() *statsRig {
	rig := &statsRig{book: NewStatsBook()}
	start := bucketStart(someEpoch)
	rig.clock = &start
	runtime := NewRuntime(func(channelID, kind, reason string) {
		rig.mutex.Lock()
		defer rig.mutex.Unlock()
		rig.seen = append(rig.seen, announcement{channelID, kind, reason})
	})
	rig.engine = NewEngine(Dependencies{Runtime: runtime, StatsBook: rig.book, Now: func() time.Time { return *rig.clock }})
	return rig
}

func (r *statsRig) announcements() []announcement {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]announcement(nil), r.seen...)
}

// fourXX records n attempts of channel on model m, of which client were 4xx.
func (r *statsRig) record(channelID string, n, client int) {
	for i := range n {
		reason := EndCompleted
		if i < client {
			reason = EndClientError
		}
		r.book.Record(channelID, "m", false, attemptOf(reason, -1), *r.clock)
	}
}

func TestAChannelBecomingAnomalousIsAnnouncedOnceAndRanksLast(t *testing.T) {
	rig := newStatsRig()
	rig.record("bad", 30, 29)
	rig.record("good", 30, 1)
	rig.engine.RefreshStats()

	events := rig.announcements()
	if len(events) != 1 || events[0].channel != "bad" || events[0].kind != channel.EventAnomalousClientErrors || events[0].reason != "m: 4xx 29/30 (97%)" {
		t.Fatalf("events = %+v", events)
	}
	if _, bad := rig.engine.statsView().anomalies["m"]["bad"]; !bad || len(rig.engine.statsView().anomalies["m"]) != 1 {
		t.Fatalf("anomalies = %+v", rig.engine.statsView().anomalies)
	}

	// Still anomalous on later refreshes: no second event.
	for range 5 {
		*rig.clock = rig.clock.Add(10 * time.Second)
		rig.engine.RefreshStats()
	}
	if got := rig.announcements(); len(got) != 1 {
		t.Fatalf("events after more refreshes = %+v", got)
	}

	// It recovers once enough good answers have come in, and the flag goes.
	rig.record("bad", 300, 0)
	rig.engine.RefreshStats()
	if len(rig.engine.statsView().anomalies) != 0 {
		t.Fatalf("anomalies after recovery = %+v", rig.engine.statsView().anomalies)
	}
}

// bulk adds counted attempts of channel on model m to the current bucket, of
// which client were 4xx, without going through them one by one.
func (r *statsRig) bulk(channelID string, counted, client int64) {
	r.book.Load([]AttemptHistoryRow{{ChannelID: channelID, ModelID: "m", Bucket: statsEpoch(*r.clock), TTFTBin: -1, Counted: counted - client, ClientErrors: client}}, *r.clock)
}

func TestAnomalyEventsAreNotRepeatedWithinAnHourOfFlapping(t *testing.T) {
	rig := newStatsRig()
	flagged := func() bool { _, bad := rig.engine.statsView().anomalies["m"]["bad"]; return bad }
	step := func(after time.Duration, counted, client int64, wantFlagged bool, wantEvents int) {
		t.Helper()
		*rig.clock = rig.clock.Add(after)
		rig.bulk("bad", counted, client)
		rig.engine.RefreshStats()
		if flagged() != wantFlagged || len(rig.announcements()) != wantEvents {
			t.Fatalf("after %s: flagged %v with %d events, want %v with %d (%+v)", after, flagged(), len(rig.announcements()), wantFlagged, wantEvents, rig.announcements())
		}
	}
	step(0, 30, 30, true, 1)                   // broken: announced (alone, so nothing to compare with)
	step(10*time.Minute, 600, 0, false, 1)     // 30 of 630: cleared
	step(10*time.Minute, 3000, 3000, true, 1)  // 3030 of 3630 = 83%: flagged again, but announced a few minutes ago
	step(5*time.Minute, 0, 0, true, 1)         // still flagged: nothing new
	step(10*time.Minute, 40000, 0, false, 1)   // cleared
	step(2*time.Hour, 200000, 200000, true, 2) // flagged again two hours after the first event: announced again
}

func TestAnomaliesFoundWhileLoadingAreNotAnnounced(t *testing.T) {
	rig := newStatsRig()
	store := &historyStore{rows: []AttemptHistoryRow{{ChannelID: "bad", ModelID: "m", Bucket: someEpoch, TTFTBin: -1, ClientErrors: 40}}}
	rig.engine.Store = store
	if err := rig.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rig.announcements()) != 0 {
		t.Fatalf("a restart announced %+v", rig.announcements())
	}
	if _, bad := rig.engine.statsView().anomalies["m"]["bad"]; !bad {
		t.Fatal("the loaded anomaly does not demote the channel")
	}
	// Still no event when it goes on, and the window the store was asked for
	// starts with the oldest bucket still in the window.
	rig.engine.RefreshStats()
	if len(rig.announcements()) != 0 {
		t.Fatalf("events = %+v", rig.announcements())
	}
	if want := bucketStart(someEpoch - statsBuckets + 1); !store.since.Equal(want) {
		t.Fatalf("history asked from %s, want %s", store.since, want)
	}
	// A new anomaly after the load is announced.
	for range 30 {
		rig.book.Record("worse", "m2", false, attemptOf(EndClientError, -1), *rig.clock)
	}
	rig.engine.RefreshStats()
	if got := rig.announcements(); len(got) != 1 || got[0].channel != "worse" || !strings.HasPrefix(got[0].reason, "m2: ") {
		t.Fatalf("events = %+v", got)
	}
}

// ---- editing a channel ----

func (r *statsRig) demoted(channelID string) bool {
	_, bad := r.engine.statsView().anomalies["m"][channelID]
	return bad
}

// The channel is flagged as soon as its 4xx answers say so, and an edit takes
// the flag away at once (not at the next refresh) while the channel's health
// and every other channel stay as they are.
func TestEditingAChannelEndsItsDemotionAtOnceAndLeavesTheRestAlone(t *testing.T) {
	rig := newStatsRig()
	rig.record("bad", 30, 29)
	rig.record("other", 30, 29) // also broken, and not edited
	for _, id := range []string{"good1", "good2", "good3"} {
		rig.record(id, 30, 1)
	}
	rig.engine.RefreshStats()
	if !rig.demoted("bad") || !rig.demoted("other") || rig.demoted("good1") {
		t.Fatalf("setup: anomalies = %+v", rig.engine.statsView().anomalies)
	}
	health := rig.engine.Stats()["bad"]

	rig.engine.ChannelEdited("bad")

	if rig.demoted("bad") {
		t.Fatal("the edited channel is still demoted")
	}
	if !rig.demoted("other") {
		t.Fatal("editing one channel cleared another")
	}
	if got := rig.engine.Stats()["bad"]; got.Attempts != health.Attempts || got.Successes != health.Successes || health.Attempts == 0 {
		t.Fatalf("health of the edited channel = %+v, was %+v", got, health)
	}
	// It stays clear on later refreshes: the old answers are gone, not hidden.
	for range 3 {
		*rig.clock = rig.clock.Add(10 * time.Second)
		rig.engine.RefreshStats()
	}
	if rig.demoted("bad") {
		t.Fatal("the demotion came back on a refresh")
	}
}

// After an edit the rule needs a full sample of its own again, and when the new
// configuration is just as broken the channel is flagged and announced again,
// without waiting out the hour that normally separates two events.
func TestAChannelStillBrokenAfterAnEditIsFlaggedAgainOnFreshAttemptsAndAnnounced(t *testing.T) {
	rig := newStatsRig()
	rig.record("bad", 30, 29)
	rig.record("good", 30, 1)
	rig.engine.RefreshStats()
	if got := rig.announcements(); len(got) != 1 {
		t.Fatalf("setup: events = %+v", got)
	}

	rig.engine.ChannelEdited("bad")
	*rig.clock = rig.clock.Add(time.Minute) // well inside anomalyRenotifyAfter

	rig.record("bad", anomalyMinSamples-1, anomalyMinSamples-1)
	rig.engine.RefreshStats()
	if rig.demoted("bad") {
		t.Fatalf("flagged on %d fresh attempts, fewer than the %d the rule needs", anomalyMinSamples-1, anomalyMinSamples)
	}
	rig.record("bad", 1, 1)
	rig.engine.RefreshStats()
	if !rig.demoted("bad") {
		t.Fatal("not flagged again on a full sample of 4xx answers")
	}
	events := rig.announcements()
	if len(events) != 2 || events[1].channel != "bad" || events[1].kind != channel.EventAnomalousClientErrors {
		t.Fatalf("events = %+v, want the first one and a second for the new configuration", events)
	}
}

// Editing a channel that has no figures, or before anything was counted, is
// harmless and still publishes a snapshot.
func TestEditingAChannelTheStatisticsNeverSawIsHarmless(t *testing.T) {
	rig := newStatsRig()
	rig.engine.ChannelEdited("unknown")
	if got := rig.engine.Stats(); len(got) != 0 {
		t.Fatalf("stats = %v", got)
	}
	rig.record("a", 3, 0)
	rig.engine.ChannelEdited("unknown")
	if got := rig.engine.Stats()["a"]; got.Attempts != 3 {
		t.Fatalf("stats of another channel = %+v", got)
	}
}

// What the store reports for the time before the edit is not held against the
// channel after a restart either, but its health is read as before.
func TestLoadingTheHistoryOfAnEditedChannelDoesNotDemoteItOnTheOldAnswers(t *testing.T) {
	rig := newStatsRig()
	row := func(channelID string, edited bool) AttemptHistoryRow {
		r := AttemptHistoryRow{ChannelID: channelID, ModelID: "m", Bucket: someEpoch, TTFTBin: -1, Counted: 2, Successes: 2, ClientErrors: 38}
		if edited {
			r.BeforeEditAttempts, r.BeforeEditClientErrors = 40, 38
		}
		return r
	}
	rig.engine.Store = &historyStore{rows: []AttemptHistoryRow{row("edited", true), row("untouched", false)}}
	if err := rig.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rig.demoted("edited") || !rig.demoted("untouched") {
		t.Fatalf("anomalies = %+v, want only the channel that was not edited", rig.engine.statsView().anomalies)
	}
	if got := rig.engine.Stats()["edited"]; got.Attempts != 2 || got.Successes != 2 {
		t.Fatalf("health of the edited channel = %+v", got)
	}
}

type historyStore struct {
	Store
	rows  []AttemptHistoryRow
	since time.Time
	calls int
	err   error
}

func (s *historyStore) AttemptHistory(_ context.Context, since time.Time) ([]AttemptHistoryRow, error) {
	s.calls++
	s.since = since
	return s.rows, s.err
}

func TestLoadFailureLeavesTheStatisticsEmptyAndUsable(t *testing.T) {
	rig := newStatsRig()
	rig.engine.Store = &historyStore{err: errors.New("database down")}
	if err := rig.engine.LoadStats(context.Background()); err == nil || !strings.Contains(err.Error(), "database down") {
		t.Fatalf("LoadStats = %v", err)
	}
	if got := rig.engine.Stats(); len(got) != 0 {
		t.Fatalf("stats = %v", got)
	}
	rig.record("a", 3, 0)
	rig.engine.RefreshStats()
	if got := rig.engine.Stats()["a"]; got.Attempts != 3 {
		t.Fatalf("stats = %+v", got)
	}
}

// The statistics never touch the database after the startup read, however long
// the refresh loop runs.
func TestRefreshLoopNeverQueriesTheDatabase(t *testing.T) {
	f := newRefreshFixture(t)
	store := &historyStore{}
	f.engine.Store = store
	if err := f.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	now := time.Now()
	for range 4 {
		f.engine.StatsBook.Record("a", "m", false, attemptOf(EndCompleted, -1), now)
	}
	eventually(t, "the loop to publish what was recorded", func() bool { return f.engine.Stats()["a"].Attempts == 4 })
	time.Sleep(30 * time.Millisecond) // many more rounds
	if store.calls != 1 {
		t.Fatalf("AttemptHistory ran %d times, want only the startup read", store.calls)
	}
}

func TestReadersSeeTheSnapshotWhileItIsRebuilt(t *testing.T) {
	rig := newStatsRig()
	rig.record("a", 5, 0)
	rig.engine.RefreshStats()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					if got := rig.engine.Stats()["a"].Attempts; got < 5 {
						t.Errorf("attempts = %d", got)
						return
					}
				}
			}
		})
	}
	for range 200 {
		rig.record("a", 1, 0)
		rig.engine.RefreshStats()
	}
	close(stop)
	wg.Wait()
	if got := rig.engine.Stats()["a"].Attempts; got != 205 {
		t.Fatalf("attempts = %d, want 205", got)
	}
}

// ---- ranking ----

func TestManualOrderRemainsAuthoritativeDespiteAnomalyFlags(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2)}
	got := Rank(RankInput{Mode: routing.ModeManual, Order: []string{"a", "b"}, Candidates: candidates, Demoted: map[string]Anomaly{"a": {}}})
	if ids(got) != "ab" {
		t.Fatal(ids(got))
	}
}

func TestRankKeepsADemotedChannelThatIsTheOnlyOneAndLetsTheStickyOneLead(t *testing.T) {
	only := []Candidate{candidate("a", "x", 1)}
	if got := ids(Rank(RankInput{Mode: routing.ModeManual, Order: []string{"a"}, Candidates: only, Demoted: map[string]Anomaly{"a": {}}})); got != "a" {
		t.Fatalf("order = %s: a demoted channel is not excluded", got)
	}
	// A Responses turn continues where the previous one ran, healthy or not.
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2), candidate("c", "x", 3)}
	in := RankInput{Mode: routing.ModeManual, Order: []string{"a", "b", "c"}, Candidates: candidates, Demoted: map[string]Anomaly{"c": {}}, Sticky: "c"}
	if got := ids(Rank(in)); got != "cab" {
		t.Fatalf("sticky order = %s, want acb", got)
	}
	in.Sticky = "b"
	if got := ids(Rank(in)); got != "bac" {
		t.Fatalf("order = %s, want bac", got)
	}
	// An excluded or unavailable channel stays out whether it is demoted or not.
	in = RankInput{Mode: routing.ModeManual, Order: []string{"a", "b", "c"}, Candidates: candidates, Demoted: map[string]Anomaly{"a": {}}, Excluded: []string{"b"}}
	if got := ids(Rank(in)); got != "ac" {
		t.Fatalf("order = %s, want ac", got)
	}
}
