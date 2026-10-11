package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// ---- the counter ----

func TestInflightStopsAtTheLimitAndFreesASlotOnRelease(t *testing.T) {
	inflight := NewInflight()
	var releases []func()
	for range 3 {
		release, ok := inflight.Acquire("a", 3)
		if !ok {
			t.Fatal("a slot below the limit was refused")
		}
		releases = append(releases, release)
	}
	if _, ok := inflight.Acquire("a", 3); ok {
		t.Fatal("a fourth call got in with a limit of 3")
	}
	if got := inflight.Count("a"); got != 3 {
		t.Fatalf("count = %d after a refusal, want 3: a refused call must not hold a slot", got)
	}
	releases[0]()
	release, ok := inflight.Acquire("a", 3)
	if !ok {
		t.Fatal("a released slot was not reusable")
	}
	release()
	for _, release := range releases[1:] {
		release()
	}
	if got := inflight.Count("a"); got != 0 {
		t.Fatalf("count = %d after everything ended", got)
	}
}

func TestInflightReleasingTwiceDoesNotFreeAnotherCallsSlot(t *testing.T) {
	inflight := NewInflight()
	first, _ := inflight.Acquire("a", 2)
	second, _ := inflight.Acquire("a", 2)
	first()
	first()
	if got := inflight.Count("a"); got != 1 {
		t.Fatalf("count = %d, want 1: the second call is still running", got)
	}
	if _, ok := inflight.Acquire("a", 2); !ok {
		t.Fatal("one free slot out of two was refused")
	}
	if _, ok := inflight.Acquire("a", 2); ok {
		t.Fatal("a double release let a third call in")
	}
	second()
}

func TestInflightCountsAccountsSeparately(t *testing.T) {
	inflight := NewInflight()
	if _, ok := inflight.Acquire("a", 1); !ok {
		t.Fatal("first call refused")
	}
	if _, ok := inflight.Acquire("a", 1); ok {
		t.Fatal("account a is over its limit")
	}
	if _, ok := inflight.Acquire("b", 1); !ok {
		t.Fatal("account b is blocked by account a")
	}
}

func TestInflightZeroMeansUnlimitedButStillCounts(t *testing.T) {
	inflight := NewInflight()
	for range 500 {
		if _, ok := inflight.Acquire("a", 0); !ok {
			t.Fatal("a call was refused with no limit")
		}
	}
	if got := inflight.Count("a"); got != 500 {
		t.Fatalf("count = %d, want 500", got)
	}
	// Lowering the limit afterwards sees the calls that started while there was none.
	if _, ok := inflight.Acquire("a", 20); ok {
		t.Fatal("a new call got in over a limit set after the others started")
	}
	if _, ok := inflight.Acquire("a", -1); !ok {
		t.Fatal("a negative limit must also mean unlimited")
	}
}

func TestInflightForgetsAccountsThatAreIdle(t *testing.T) {
	inflight := NewInflight()
	release, _ := inflight.Acquire("a", 1)
	release()
	if len(inflight.counts) != 0 {
		t.Fatalf("idle accounts stay in the map: %v", inflight.counts)
	}
}

func TestInflightNeverAdmitsMoreThanTheLimitUnderContention(t *testing.T) {
	const attempts, limit = 200, 7
	inflight := NewInflight()
	var admitted atomic.Int32
	var tried, finish sync.WaitGroup
	tried.Add(attempts)
	finish.Add(attempts)
	hold := make(chan struct{})
	for range attempts {
		go func() {
			defer finish.Done()
			release, ok := inflight.Acquire("a", limit)
			if ok {
				admitted.Add(1)
			}
			tried.Done()
			if ok {
				<-hold // every admitted call is still running while the others try
				release()
			}
		}()
	}
	tried.Wait()
	if got := admitted.Load(); got != limit {
		t.Fatalf("%d calls admitted, want exactly %d", got, limit)
	}
	close(hold)
	finish.Wait()
	if got := inflight.Count("a"); got != 0 {
		t.Fatalf("count = %d after all calls ended", got)
	}
}

func TestInflightStaysWithinTheLimitWhileCallsComeAndGo(t *testing.T) {
	const workers, rounds, limit = 32, 200, 5
	inflight := NewInflight()
	var running, highest atomic.Int32
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for range rounds {
				release, ok := inflight.Acquire("a", limit)
				if !ok {
					continue
				}
				now := running.Add(1)
				for {
					seen := highest.Load()
					if now <= seen || highest.CompareAndSwap(seen, now) {
						break
					}
				}
				time.Sleep(10 * time.Microsecond)
				running.Add(-1)
				release()
			}
		})
	}
	group.Wait()
	if got := highest.Load(); got > limit || got == 0 {
		t.Fatalf("highest concurrency = %d, want 1..%d", got, limit)
	}
	if got := inflight.Count("a"); got != 0 {
		t.Fatalf("count = %d after all calls ended", got)
	}
}

// ---- the gate in Serve ----

type serveStore struct {
	Store
	mutex    sync.Mutex
	inserted []CallInsert
	finished []CallFinish
}

func (s *serveStore) LookupKey(context.Context, []byte) (KeyAuth, error) {
	return KeyAuth{KeyID: "key-1", OwnerID: "account-1", Enabled: true, AccountActive: true}, nil
}

func (s *serveStore) TouchKey(context.Context, string, time.Time) error { return nil }

func (s *serveStore) Points(context.Context, string) (ledger.Points, error) {
	return ledger.Points{Balance: money.FromNano(10 * money.Scale)}, nil
}

func (s *serveStore) InsertCall(_ context.Context, call CallInsert) (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.inserted = append(s.inserted, call)
	return "call-1", nil
}

func (s *serveStore) FinishCall(_ context.Context, finish CallFinish) (FinishResult, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.finished = append(s.finished, finish)
	return FinishResult{Finished: true}, nil
}

type oneModel struct{ catalog.Store }

func (oneModel) GetModel(_ context.Context, id string) (catalog.Model, error) {
	return catalog.Model{ID: id, Enabled: true}, nil
}

// stoppingRouting ends every admitted call at the routing step, which comes
// right after the in-flight gate: a call that gets past the gate answers 500
// (or panics when panicWith is set) instead of reaching a channel.
type stoppingRouting struct {
	routing.Store
	panicWith any
}

func (r stoppingRouting) Resolve(context.Context, string, string, string) (routing.Pref, error) {
	if r.panicWith != nil {
		panic(r.panicWith)
	}
	return routing.Pref{}, errors.New("stop here")
}

type serveFixture struct {
	engine *Engine
	store  *serveStore
}

func newServeFixture(t *testing.T, limit int32, routes routing.Store) *serveFixture {
	t.Helper()
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &serveStore{}
	engine := NewEngine(Dependencies{
		Store:    store,
		Catalog:  catalog.NewService(oneModel{}),
		Settings: settings.NewService(settingsOnlyStore{source: newGatedSource(settings.Settings{AccountMaxInflightCalls: limit, DefaultMaxAttempts: 1})}),
		Routing:  routes,
		Outbound: policy,
	})
	return &serveFixture{engine: engine, store: store}
}

func (f *serveFixture) serve(path string, format channel.Format, header map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-test"}`))
	request.Header.Set("Authorization", "Bearer "+apikey.Prefix+"test")
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	f.engine.Serve(recorder, request, format, "", false)
	return recorder
}

func (f *serveFixture) serveChat() *httptest.ResponseRecorder {
	return f.serve("/v1/chat/completions", channel.FormatOpenAIChat, nil)
}

func TestServeRefusesWith429WhenTheAccountIsAtItsLimitAndRecordsIt(t *testing.T) {
	f := newServeFixture(t, 2, stoppingRouting{})
	for range 2 {
		if _, ok := f.engine.inflight.Acquire("account-1", 2); !ok {
			t.Fatal("setup: could not occupy a slot")
		}
	}

	recorder := f.serveChat()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusTooManyRequests || body.Error.Code != "too_many_inflight_calls" || body.Error.Type != "rate_limit_error" ||
		body.Error.Message != "同时进行的调用过多，请稍后重试" || recorder.Header().Get("X-AIHub-Request-Id") != "call-1" {
		t.Fatalf("answer = %d %s (id %q)", recorder.Code, recorder.Body.String(), recorder.Header().Get("X-AIHub-Request-Id"))
	}
	if len(f.store.finished) != 1 {
		t.Fatalf("the refused call must be recorded once: %+v", f.store.finished)
	}
	finish := f.store.finished[0]
	if finish.ID != "call-1" || finish.Outcome != OutcomeRejectedInflight || finish.Bill != nil || finish.Cost != 0 || finish.ChannelID != "" {
		t.Fatalf("recorded call = %+v", finish)
	}
	if got := f.engine.inflight.Count("account-1"); got != 2 {
		t.Fatalf("count = %d: a refused call must not hold or free a slot", got)
	}
}

func TestServeAnswers429InTheClientsOwnErrorFormat(t *testing.T) {
	f := newServeFixture(t, 1, stoppingRouting{})
	if _, ok := f.engine.inflight.Acquire("account-1", 1); !ok {
		t.Fatal("setup: could not occupy a slot")
	}

	anthropic := f.serve("/v1/messages", channel.FormatAnthropic, map[string]string{"anthropic-version": "2023-06-01"})
	var claude struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(anthropic.Body.Bytes(), &claude); err != nil {
		t.Fatal(err)
	}
	if anthropic.Code != http.StatusTooManyRequests || claude.Type != "error" || claude.Error.Type != "rate_limit_error" || claude.RequestID != "call-1" {
		t.Fatalf("anthropic = %d %s", anthropic.Code, anthropic.Body.String())
	}

	gemini := f.serve("/v1beta/models/gemini-test:generateContent", channel.FormatGemini, nil)
	var google struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(gemini.Body.Bytes(), &google); err != nil {
		t.Fatal(err)
	}
	if gemini.Code != http.StatusTooManyRequests || google.Error.Code != 429 || google.Error.Status != "RESOURCE_EXHAUSTED" ||
		len(google.Error.Details) != 1 || google.Error.Details[0].Reason != "TOO_MANY_INFLIGHT_CALLS" {
		t.Fatalf("gemini = %d %s", gemini.Code, gemini.Body.String())
	}
}

func TestServeLetsOtherAccountsAndUnlimitedSettingsThrough(t *testing.T) {
	f := newServeFixture(t, 1, stoppingRouting{})
	if _, ok := f.engine.inflight.Acquire("someone-else", 1); !ok {
		t.Fatal("setup: could not occupy a slot")
	}
	if recorder := f.serveChat(); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("another account's call must not count against this one: %d %s", recorder.Code, recorder.Body.String())
	}

	unlimited := newServeFixture(t, 0, stoppingRouting{})
	for range 100 {
		if _, ok := unlimited.engine.inflight.Acquire("account-1", 0); !ok {
			t.Fatal("setup: unlimited acquire refused")
		}
	}
	if recorder := unlimited.serveChat(); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("limit 0 must not refuse: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestServeGivesTheSlotBackWhenTheCallEndsOrPanics(t *testing.T) {
	f := newServeFixture(t, 1, stoppingRouting{})
	if recorder := f.serveChat(); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("call = %d %s", recorder.Code, recorder.Body.String())
	}
	if got := f.engine.inflight.Count("account-1"); got != 0 {
		t.Fatalf("count = %d after a call that ended with an error", got)
	}
	if recorder := f.serveChat(); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("the next call was refused although the first one ended: %d", recorder.Code)
	}

	panicking := newServeFixture(t, 1, stoppingRouting{panicWith: "boom"})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("setup: the routing step was expected to panic")
			}
		}()
		panicking.serveChat()
	}()
	if got := panicking.engine.inflight.Count("account-1"); got != 0 {
		t.Fatalf("count = %d after a panic: the slot leaked", got)
	}
}
