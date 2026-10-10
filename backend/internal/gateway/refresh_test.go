package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// gatedSource stands in for one database read. By default it answers with the
// current value immediately; after block() every read parks until release()
// (or its context ends), like a slow query.
type gatedSource[T any] struct {
	mutex   sync.Mutex
	value   T
	err     error
	gate    chan struct{}
	entered chan struct{}
	calls   atomic.Int32
}

func newGatedSource[T any](value T) *gatedSource[T] {
	return &gatedSource[T]{value: value, entered: make(chan struct{}, 1000)}
}

func (g *gatedSource[T]) set(value T, err error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.value, g.err = value, err
}

func (g *gatedSource[T]) block() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.gate = make(chan struct{})
}

func (g *gatedSource[T]) release() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.gate != nil {
		close(g.gate)
		g.gate = nil
	}
}

func (g *gatedSource[T]) read(ctx context.Context) (T, error) {
	g.calls.Add(1)
	g.mutex.Lock()
	gate := g.gate
	g.mutex.Unlock()
	if gate != nil {
		g.entered <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return g.value, g.err
}

type statsOnlyStore struct {
	Store
	source *gatedSource[map[string]ChannelStats]
}

func (s statsOnlyStore) ChannelStats(ctx context.Context) (map[string]ChannelStats, error) {
	return s.source.read(ctx)
}

type settingsOnlyStore struct {
	settings.Store
	source *gatedSource[settings.Settings]
}

func (s settingsOnlyStore) GetSettings(ctx context.Context) (settings.Settings, error) {
	return s.source.read(ctx)
}

type refreshFixture struct {
	engine   *Engine
	settings *gatedSource[settings.Settings]
	stats    *gatedSource[map[string]ChannelStats]
	logs     *syncBuffer
}

type syncBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.String()
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &refreshFixture{
		settings: newGatedSource(settings.Settings{DefaultMaxAttempts: 1}),
		stats:    newGatedSource(map[string]ChannelStats{"a": {Attempts: 1}}),
		logs:     &syncBuffer{},
	}
	f.engine = NewEngine(Dependencies{
		Store:    statsOnlyStore{source: f.stats},
		Settings: settings.NewService(settingsOnlyStore{source: f.settings}),
		Outbound: policy,
		Logger:   slog.New(slog.NewTextHandler(f.logs, nil)),
	})
	return f
}

// start runs the background refresh with short intervals and returns a stop
// function that cancels it and waits for it to exit.
func (f *refreshFixture) start(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.engine.runRefresh(ctx, 5*time.Millisecond, 5*time.Millisecond)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("RunRefresh did not return after its context ended")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func (f *refreshFixture) maxAttempts(t *testing.T) int32 {
	t.Helper()
	// Bound the read: a reader that waits on a refresh is exactly the bug.
	type result struct {
		value settings.Settings
		err   error
	}
	got := make(chan result, 1)
	go func() {
		value, _, err := f.engine.Settings(context.Background())
		got <- result{value, err}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("Settings: %v", r.err)
		}
		return r.value.DefaultMaxAttempts
	case <-time.After(2 * time.Second):
		t.Fatal("Settings blocked while a refresh was running")
		return 0
	}
}

func (f *refreshFixture) attempts(t *testing.T, channelID string) int64 {
	t.Helper()
	got := make(chan int64, 1)
	go func() { got <- f.engine.Stats(context.Background())[channelID].Attempts }()
	select {
	case value := <-got:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("Stats blocked while a refresh was running")
		return 0
	}
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitEntered[T any](t *testing.T, source *gatedSource[T]) {
	t.Helper()
	select {
	case <-source.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never reached the store")
	}
}

func TestStatsReadsDoNotWaitForRefresh(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t)
	eventually(t, "the first stats load", func() bool { return f.attempts(t, "a") == 1 })

	f.stats.block()
	f.stats.set(map[string]ChannelStats{"a": {Attempts: 2}}, nil)
	waitEntered(t, f.stats) // a refresh is now parked inside the stats query

	// Readers (the Serve and browse paths) keep getting the previous value.
	for range 50 {
		if got := f.attempts(t, "a"); got != 1 {
			t.Fatalf("attempts = %d while refreshing, want the previous 1", got)
		}
	}
	// A settings change still reaches the gateway while the stats query is stuck.
	f.settings.set(settings.Settings{DefaultMaxAttempts: 7}, nil)
	eventually(t, "settings to refresh independently of stats", func() bool { return f.maxAttempts(t) == 7 })

	f.stats.release()
	eventually(t, "the new stats to be published", func() bool { return f.attempts(t, "a") == 2 })
}

func TestSettingsReadsDoNotWaitForRefresh(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t)
	eventually(t, "the first settings load", func() bool { return f.maxAttempts(t) == 1 })

	f.settings.block()
	f.settings.set(settings.Settings{DefaultMaxAttempts: 9}, nil)
	waitEntered(t, f.settings)

	for range 50 {
		if got := f.maxAttempts(t); got != 1 {
			t.Fatalf("max attempts = %d while refreshing, want the previous 1", got)
		}
		if got := f.attempts(t, "a"); got != 1 {
			t.Fatalf("stats = %d, want 1", got)
		}
	}

	f.settings.release()
	eventually(t, "the new settings to be published", func() bool { return f.maxAttempts(t) == 9 })
}

func TestFailedRefreshKeepsPreviousValues(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t)
	eventually(t, "the first load", func() bool { return f.maxAttempts(t) == 1 && f.attempts(t, "a") == 1 })

	f.settings.set(settings.Settings{}, errors.New("settings down"))
	f.stats.set(nil, errors.New("stats down"))
	eventually(t, "both failures to be logged", func() bool {
		logs := f.logs.String()
		return strings.Contains(logs, "settings down") && strings.Contains(logs, "stats down")
	})
	// Several more failing rounds later nothing has changed for readers.
	failed := f.settings.calls.Load()
	eventually(t, "more failing rounds", func() bool { return f.settings.calls.Load() >= failed+3 })
	if got := f.maxAttempts(t); got != 1 {
		t.Fatalf("max attempts = %d after failed refreshes, want the previous 1", got)
	}
	if got := f.attempts(t, "a"); got != 1 {
		t.Fatalf("attempts = %d after failed refreshes, want the previous 1", got)
	}
	if _, _, err := f.engine.Settings(context.Background()); err != nil {
		t.Fatalf("Settings returned %v after a failed refresh", err)
	}

	// Recovery: the next successful refresh replaces the value.
	f.settings.set(settings.Settings{DefaultMaxAttempts: 4}, nil)
	f.stats.set(map[string]ChannelStats{"a": {Attempts: 5}}, nil)
	eventually(t, "recovery", func() bool { return f.maxAttempts(t) == 4 && f.attempts(t, "a") == 5 })
}

func TestInvalidBlockedHostsKeepPreviousSettings(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t)
	eventually(t, "the first load", func() bool { return f.maxAttempts(t) == 1 })

	f.settings.set(settings.Settings{DefaultMaxAttempts: 2, ExtraBlockedHosts: []string{"not a host"}}, nil)
	eventually(t, "the policy error to be logged", func() bool { return strings.Contains(f.logs.String(), "invalid blocked upstream host") })
	if got := f.maxAttempts(t); got != 1 {
		t.Fatalf("max attempts = %d, want the previous settings to stay in effect", got)
	}
}

func TestFirstLoadIsSharedAndFailureIsNotCached(t *testing.T) {
	f := newRefreshFixture(t)
	f.stats.block()

	const readers = 20
	results := make(chan int64, readers)
	for range readers {
		go func() { results <- f.engine.Stats(context.Background())["a"].Attempts }()
	}
	waitEntered(t, f.stats)
	time.Sleep(20 * time.Millisecond) // let the other readers queue behind the first load
	f.stats.release()
	for range readers {
		select {
		case got := <-results:
			if got != 1 {
				t.Fatalf("attempts = %d, want 1", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a reader never got the first load")
		}
	}
	if calls := f.stats.calls.Load(); calls != 1 {
		t.Fatalf("%d stats queries for %d concurrent first readers, want 1", calls, readers)
	}

	// A failed first load degrades to an empty map and is retried, not kept.
	g := newRefreshFixture(t)
	g.stats.set(nil, errors.New("stats down"))
	if got := g.engine.Stats(context.Background()); len(got) != 0 {
		t.Fatalf("stats = %v, want empty after a failed first load", got)
	}
	g.stats.set(map[string]ChannelStats{"a": {Attempts: 3}}, nil)
	if got := g.attempts(t, "a"); got != 3 {
		t.Fatalf("attempts = %d, want the retried load to succeed", got)
	}
}

func TestRefreshStopsWhenContextEnds(t *testing.T) {
	f := newRefreshFixture(t)
	stop := f.start(t)
	eventually(t, "the first load", func() bool { return f.maxAttempts(t) == 1 })

	// Stop while a refresh is parked in the database: it must be abandoned and
	// must not be reported as a failure.
	f.stats.block()
	f.settings.block()
	waitEntered(t, f.stats)
	waitEntered(t, f.settings)
	stop()
	if logs := f.logs.String(); strings.Contains(logs, "refresh failed") {
		t.Fatalf("shutdown logged a refresh failure:\n%s", logs)
	}

	// Nothing refreshes any more.
	f.stats.release()
	f.settings.set(settings.Settings{DefaultMaxAttempts: 8}, nil)
	f.settings.release()
	calls := f.settings.calls.Load()
	time.Sleep(30 * time.Millisecond)
	if f.settings.calls.Load() != calls {
		t.Fatal("settings were still being read after RunRefresh returned")
	}
}
