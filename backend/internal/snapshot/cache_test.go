package snapshot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// fakeLoader is the database of the cache tests: its contents can be changed
// "behind the cache's back" and a failure can be injected.
type fakeLoader struct {
	mu         sync.Mutex
	keys       []gateway.KeyRecord
	candidates []gateway.CandidateRecord
	prefs      []routing.Scoped
	models     []catalog.Model
	points     map[string]ledger.Points
	err        error
	loads      int
}

func (l *fakeLoader) with(change func(*fakeLoader)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	change(l)
}

func (l *fakeLoader) read() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads++
	return l.err
}

func (l *fakeLoader) LoadKeys(context.Context) ([]gateway.KeyRecord, error) {
	if err := l.read(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.keys), nil
}

func (l *fakeLoader) LoadCandidates(context.Context) ([]gateway.CandidateRecord, error) {
	if err := l.read(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.candidates), nil
}

func (l *fakeLoader) LoadRoutePrefs(context.Context) ([]routing.Scoped, error) {
	if err := l.read(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.prefs), nil
}

func (l *fakeLoader) LoadModels(context.Context) ([]catalog.Model, error) {
	if err := l.read(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.models), nil
}

func (l *fakeLoader) LoadPoints(_ context.Context, accountIDs []string) (map[string]ledger.Points, error) {
	if err := l.read(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	points := make(map[string]ledger.Points, len(accountIDs))
	for _, id := range accountIDs {
		if value, found := l.points[id]; found {
			points[id] = value
		}
	}
	return points, nil
}

func newTestCache(loader *fakeLoader) *Cache {
	return New(Config{Loader: loader, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func TestBuildFleetIndexesByModelAndFormatInChannelOrder(t *testing.T) {
	custom := &channel.CustomPrices{}
	records := []gateway.CandidateRecord{
		{ModelID: "a", Formats: []channel.Format{channel.FormatOpenAIChat, channel.FormatAnthropic}, Candidate: gateway.Candidate{ChannelID: "c1"}},
		{ModelID: "a", Formats: []channel.Format{channel.FormatOpenAIChat}, Candidate: gateway.Candidate{ChannelID: "c2"}},
		{ModelID: "b", Formats: []channel.Format{channel.FormatGemini}, Candidate: gateway.Candidate{ChannelID: "c1", Custom: custom}},
	}
	fleet := buildFleet(records)

	chat := fleet.candidates[candidateKey{"a", channel.FormatOpenAIChat}]
	if len(chat) != 2 || chat[0].ChannelID != "c1" || chat[1].ChannelID != "c2" {
		t.Fatalf("a/openai_chat = %+v, want c1 then c2", chat)
	}
	if got := fleet.candidates[candidateKey{"a", channel.FormatAnthropic}]; len(got) != 1 || got[0].ChannelID != "c1" {
		t.Fatalf("a/anthropic = %+v", got)
	}
	if got := fleet.candidates[candidateKey{"a", channel.FormatGemini}]; len(got) != 0 {
		t.Fatalf("a/gemini = %+v, want none", got)
	}
	want := []gateway.OnlineModel{
		{ID: "a", Formats: []channel.Format{channel.FormatAnthropic, channel.FormatOpenAIChat}},
		{ID: "b", Formats: []channel.Format{channel.FormatGemini}},
	}
	if !reflect.DeepEqual(fleet.online, want) {
		t.Fatalf("online = %+v, want %+v", fleet.online, want)
	}

}

func TestModelListingDoesNotWidenAuxiliaryStateCandidates(t *testing.T) {
	records := []gateway.CandidateRecord{
		{ModelID: "legacy", Formats: []channel.Format{channel.FormatOpenAIChat}, Candidate: gateway.Candidate{ChannelID: "old"}},
		{ModelID: "group", Formats: []channel.Format{channel.FormatOpenAIChat}, Candidate: gateway.Candidate{ChannelID: "new", GroupID: "g"}},
	}
	fleet := buildFleet(records)
	listed := fleet.modelLists[channel.FormatOpenAIChat]
	auxiliary := fleet.auxiliary[channel.FormatOpenAIChat]
	if len(listed) != 2 || listed[0].ModelID != "legacy" || listed[1].ModelID != "group" {
		t.Fatal("listing lost an inference candidate", listed)
	}
	if len(auxiliary) != 1 || auxiliary[0].Candidate.GroupID != "g" {
		t.Fatal("legacy candidate acquired auxiliary state access", auxiliary)
	}
}

func TestBuildModelsKeepsOrderAndFiltersDisabled(t *testing.T) {
	set := buildModels([]catalog.Model{{ID: "b", Enabled: true}, {ID: "a"}, {ID: "c", Enabled: true}})
	if ids := modelIDs(set.all); !slices.Equal(ids, []string{"b", "a", "c"}) {
		t.Fatalf("all = %v", ids)
	}
	if ids := modelIDs(set.enabled); !slices.Equal(ids, []string{"b", "c"}) {
		t.Fatalf("enabled = %v", ids)
	}
	if _, ok := set.byID["a"]; !ok {
		t.Fatal("a disabled model must be found by name: the engine rejects it with its own error")
	}
	empty := buildModels(nil)
	if empty.all == nil || empty.enabled == nil {
		t.Fatal("empty lists must be non-nil like the database store's")
	}
}

func modelIDs(models []catalog.Model) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestLookupKeyAnswersFromTheSnapshotAndNeverFallsBack(t *testing.T) {
	loader := &fakeLoader{keys: []gateway.KeyRecord{{Hash: []byte("known"), Auth: gateway.KeyAuth{KeyID: "k1", Enabled: true}}}}
	raw := &countingStore{}
	store := newTestCache(loader).Gateway(raw)
	key, err := store.LookupKey(context.Background(), []byte("known"))
	if err != nil || key.KeyID != "k1" || !key.Enabled {
		t.Fatalf("known key = %+v, %v", key, err)
	}
	if _, err := store.LookupKey(context.Background(), []byte("unknown")); !errors.Is(err, gateway.ErrKeyNotFound) {
		t.Fatalf("unknown key err = %v, want ErrKeyNotFound", err)
	}
	if raw.calls.Load() != 0 {
		t.Fatal("the lookup went to the database")
	}
}

func TestCandidatesReturnsACopyTheCallerMayChange(t *testing.T) {
	loader := &fakeLoader{candidates: []gateway.CandidateRecord{
		{ModelID: "m", Formats: []channel.Format{channel.FormatOpenAIChat}, Candidate: gateway.Candidate{ChannelID: "c1", RateNano: 1}},
	}}
	store := newTestCache(loader).Gateway(&countingStore{})
	first, err := store.Candidates(context.Background(), "m", channel.FormatOpenAIChat)
	if err != nil || len(first) != 1 {
		t.Fatalf("candidates = %+v, %v", first, err)
	}
	first[0].RateNano = 99 // what the engine does before ranking
	second, _ := store.Candidates(context.Background(), "m", channel.FormatOpenAIChat)
	if second[0].RateNano != 1 {
		t.Fatalf("a caller's change leaked into the snapshot: rate %d", second[0].RateNano)
	}
	none, err := store.Candidates(context.Background(), "m", channel.FormatGemini)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no candidates = %#v, %v; want an empty, non-nil list like the database store's", none, err)
	}
}

func TestResolveFollowsKeyThenAccountThenDefault(t *testing.T) {
	loader := &fakeLoader{prefs: []routing.Scoped{
		{AccountID: "a", Pref: routing.Pref{ModelID: "m", Source: routing.SourceAccount, Mode: routing.ModeReliable}},
		{AccountID: "a", KeyID: "k", Pref: routing.Pref{ModelID: "m", Source: routing.SourceKey, Mode: routing.ModeFastest}},
	}}
	store := newTestCache(loader).Routing(nil)
	resolve := func(account, key, model string) routing.Pref {
		pref, err := store.Resolve(context.Background(), account, key, model)
		if err != nil {
			t.Fatal(err)
		}
		return pref
	}
	if pref := resolve("a", "k", "m"); pref.Source != routing.SourceKey || pref.Mode != routing.ModeFastest {
		t.Fatalf("key override = %+v", pref)
	}
	if pref := resolve("a", "other", "m"); pref.Source != routing.SourceAccount || pref.Mode != routing.ModeReliable {
		t.Fatalf("account setting = %+v", pref)
	}
	if pref := resolve("a", "", "m"); pref.Source != routing.SourceAccount {
		t.Fatalf("no key = %+v", pref)
	}
	if pref := resolve("a", "k", "unrouted"); !reflect.DeepEqual(pref, routing.Default("unrouted")) {
		t.Fatalf("default = %+v", pref)
	}
	if pref := resolve("b", "k", "m"); pref.Source != routing.SourceDefault {
		t.Fatalf("another account = %+v", pref)
	}
}

func TestModelsReaderMatchesTheCatalogService(t *testing.T) {
	loader := &fakeLoader{models: []catalog.Model{{ID: "on", Enabled: true}, {ID: "off"}}}
	models := newTestCache(loader).Models()
	if model, err := models.Get(context.Background(), " on "); err != nil || model.ID != "on" {
		t.Fatalf("Get padded name = %+v, %v (the catalog service trims it)", model, err)
	}
	if model, err := models.Get(context.Background(), "off"); err != nil || model.Enabled {
		t.Fatalf("Get disabled = %+v, %v", model, err)
	}
	if _, err := models.Get(context.Background(), "nope"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Get unknown err = %v", err)
	}
	all, _ := models.List(context.Background(), true)
	enabled, _ := models.List(context.Background(), false)
	if len(all) != 2 || len(enabled) != 1 {
		t.Fatalf("lists = %d and %d", len(all), len(enabled))
	}
}

func TestInvalidateMakesTheChangeVisibleBeforeItReturns(t *testing.T) {
	loader := &fakeLoader{keys: []gateway.KeyRecord{{Hash: []byte("k"), Auth: gateway.KeyAuth{KeyID: "k1", Enabled: true}}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	if key, _ := store.LookupKey(context.Background(), []byte("k")); !key.Enabled {
		t.Fatal("setup")
	}
	loader.with(func(l *fakeLoader) { l.keys[0].Auth.Enabled = false }) // the write commits...
	cache.Invalidate(ctx, DomainKeys)                                   // ...and the write path says so
	if key, _ := store.LookupKey(context.Background(), []byte("k")); key.Enabled {
		t.Fatal("the key still looks enabled after the invalidation returned")
	}
}

func TestFailedRebuildAfterAChangeFailsClosed(t *testing.T) {
	loader := &fakeLoader{keys: []gateway.KeyRecord{{Hash: []byte("k"), Auth: gateway.KeyAuth{KeyID: "k1", Enabled: true}}}}
	cache := newTestCache(loader)
	store := cache.Gateway(&countingStore{})
	if _, err := store.LookupKey(context.Background(), []byte("k")); err != nil {
		t.Fatal(err)
	}
	loader.with(func(l *fakeLoader) { l.keys[0].Auth.Enabled = false; l.err = errors.New("database down") })
	cache.Invalidate(ctx, DomainKeys)
	if key, err := store.LookupKey(context.Background(), []byte("k")); err == nil {
		t.Fatalf("the stale key was served: %+v", key)
	}
	loader.with(func(l *fakeLoader) { l.err = nil })
	if key, err := store.LookupKey(context.Background(), []byte("k")); err != nil || key.Enabled {
		t.Fatalf("after recovery = %+v, %v", key, err)
	}
}

func TestRunRebuildsEverythingOnItsInterval(t *testing.T) {
	loader := &fakeLoader{}
	cache := New(Config{Loader: loader, Interval: 10 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cache.Run(runCtx); close(done) }()
	defer func() { stop(); <-done }()

	store := cache.Gateway(&countingStore{})
	// A change nobody announced, like a manual SQL statement.
	loader.with(func(l *fakeLoader) {
		l.keys = []gateway.KeyRecord{{Hash: []byte("late"), Auth: gateway.KeyAuth{KeyID: "k9"}}}
	})
	waitFor(t, "the periodic rebuild to find the key", func() bool {
		_, err := store.LookupKey(context.Background(), []byte("late"))
		return err == nil
	})
}

func TestPeriodicRebuildFailureKeepsServingThePreviousSnapshot(t *testing.T) {
	loader := &fakeLoader{keys: []gateway.KeyRecord{{Hash: []byte("k"), Auth: gateway.KeyAuth{KeyID: "k1"}}}}
	cache := newTestCache(loader)
	if err := cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	loader.with(func(l *fakeLoader) { l.err = errors.New("database down") })
	if err := cache.run(ctx, domainAll, true); err == nil {
		t.Fatal("a failed rebuild reported success")
	}
	if _, err := cache.Gateway(&countingStore{}).LookupKey(context.Background(), []byte("k")); err != nil {
		t.Fatalf("the previous snapshot was dropped: %v", err)
	}
}

func TestLoadReportsAnUnreadableDatabase(t *testing.T) {
	loader := &fakeLoader{err: errors.New("database down")}
	if err := newTestCache(loader).Load(ctx); err == nil {
		t.Fatal("Load hid the failure")
	}
}

func TestResponseChannelIsRememberedWhenACallEndsAndFoundInTheDatabaseOtherwise(t *testing.T) {
	raw := &countingStore{responseChannels: map[string]string{"acct/resp_old": "ch-old"}}
	store := newTestCache(&fakeLoader{}).Gateway(raw)
	ctx := context.Background()

	// A call that ended in this process: the next turn finds it in memory.
	raw.finishResult = gateway.FinishResult{Finished: true}
	if _, err := store.FinishCall(ctx, gateway.CallFinish{AccountID: "acct", UpstreamResponseID: "resp_new", ChannelID: "ch-new"}); err != nil {
		t.Fatal(err)
	}
	if channelID, err := store.ResponseChannel(ctx, "acct", "resp_new"); err != nil || channelID != "ch-new" {
		t.Fatalf("recent response = %q, %v", channelID, err)
	}
	if raw.lookups.Load() != 0 {
		t.Fatal("a response closed by this process was looked up in the database")
	}
	// One from before the process started: the database answers once.
	for range 2 {
		if channelID, err := store.ResponseChannel(ctx, "acct", "resp_old"); err != nil || channelID != "ch-old" {
			t.Fatalf("old response = %q, %v", channelID, err)
		}
	}
	if raw.lookups.Load() != 1 {
		t.Fatalf("database lookups = %d, want 1", raw.lookups.Load())
	}
	// An unknown one stays unknown (and is not remembered as missing).
	if _, err := store.ResponseChannel(ctx, "acct", "resp_none"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown response err = %v", err)
	}
	// Another account cannot continue from it.
	if _, err := store.ResponseChannel(ctx, "other", "resp_new"); err == nil {
		t.Fatal("a response of another account was found")
	}
	// A call that did not end here, or that has no response id, is not remembered.
	raw.finishResult = gateway.FinishResult{Finished: false}
	_, _ = store.FinishCall(ctx, gateway.CallFinish{AccountID: "acct", UpstreamResponseID: "resp_late", ChannelID: "ch-x"})
	if _, err := store.ResponseChannel(ctx, "acct", "resp_late"); err == nil {
		t.Fatal("a call that did not finish here was remembered")
	}
}

func TestResponseLRUEvictsTheLeastRecentlyUsed(t *testing.T) {
	lru := newResponseLRU(2)
	lru.put("a", "1", "c1")
	lru.put("a", "2", "c2")
	if _, ok := lru.get("a", "1"); !ok { // 1 is now the most recent
		t.Fatal("1 missing")
	}
	lru.put("a", "3", "c3") // evicts 2
	if _, ok := lru.get("a", "2"); ok {
		t.Fatal("2 survived")
	}
	for _, id := range []string{"1", "3"} {
		if _, ok := lru.get("a", id); !ok {
			t.Fatalf("%s evicted", id)
		}
	}
	lru.put("a", "3", "c3b")
	if channelID, _ := lru.get("a", "3"); channelID != "c3b" {
		t.Fatalf("update = %q", channelID)
	}
}

func TestTimeoutRoutesAreReplacedPerModelNotPerField(t *testing.T) {
	idle, request := int32(2000), int32(10000)
	loader := &fakeLoader{prefs: []routing.Scoped{
		{AccountID: "a", Pref: routing.Pref{ModelID: "m", Source: routing.SourceAccount, IdleTimeoutMS: &idle, RequestTimeoutMS: &request}},
		{AccountID: "a", Pref: routing.Pref{ModelID: "other", Source: routing.SourceAccount, IdleTimeoutMS: &idle, RequestTimeoutMS: &request}},
		{AccountID: "a", KeyID: "k", Pref: routing.Pref{ModelID: "m", Source: routing.SourceKey, RequestTimeoutMS: &request}},
	}}
	store := newTestCache(loader).Routing(nil)
	own, err := store.Resolve(context.Background(), "a", "k", "m")
	if err != nil || own.Source != routing.SourceKey || own.IdleTimeoutMS != nil || own.RequestTimeoutMS == nil || *own.RequestTimeoutMS != request {
		t.Fatalf("key route = %+v, %v", own, err)
	}
	other, err := store.Resolve(context.Background(), "a", "k", "other")
	if err != nil || other.Source != routing.SourceAccount || other.IdleTimeoutMS == nil || *other.IdleTimeoutMS != idle {
		t.Fatalf("other model = %+v, %v", other, err)
	}
}
