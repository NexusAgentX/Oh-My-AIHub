package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func followupFixture(t *testing.T, handler http.HandlerFunc) (*Engine, *backgroundFixture, BackgroundJob) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := keyring.Encrypt("channel", 1, "synthetic-upstream-key")
	if err != nil {
		t.Fatal(err)
	}
	store := &backgroundFixture{jobs: map[string]BackgroundJob{}, finished: map[string]CallFinish{}, key: KeyAuth{KeyID: "key", OwnerID: "consumer", Enabled: true, AccountActive: true}}
	metadata := backgroundMetadata{
		Started: time.Now(), Key: store.key, ModelID: "main", Hints: billingHints{Background: true}, Attempts: []Attempt{{}},
		Candidate: Candidate{ChannelID: "channel", OwnerID: "seller", BaseURL: upstream.URL, Credential: credential, MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate},
		Model:     catalog.Model{ID: "main", InputPrice: 1_000_000_000, OutputPrice: 5_000_000_000},
	}
	metadata.Candidate = quotedCandidate(t, metadata.Candidate, metadata.Model)
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	job := BackgroundJob{CallID: "call", AccountID: "consumer", KeyID: "key", ChannelID: "channel", ResponseID: "resp_followup", Metadata: raw, Deadline: time.Now().Add(time.Hour)}
	store.jobs[job.CallID] = job
	engine := NewEngine(Dependencies{Store: store, Jobs: store, Keyring: keyring})
	engine.settingsSnap.Store(&settingsSnapshot{outbound: backgroundOutbound{client: upstream.Client()}})
	return engine, store, job
}

func followupRequest(ctx context.Context, method, query string) *http.Request {
	r := httptest.NewRequest(method, "/v1/responses/resp_followup"+query, nil).WithContext(ctx)
	r.SetPathValue("responseID", "resp_followup")
	r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
	return r
}

func TestBackgroundFollowupAuthorizationForReadsAndCancellation(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, name := range []string{"other-account", "other-key", "disabled", "expired", "inactive-account", "model-revoked", "missing-key", "unknown-id", "invalid-id"} {
			t.Run(method+"/"+name, func(t *testing.T) {
				var reached atomic.Int32
				e, store, _ := followupFixture(t, func(w http.ResponseWriter, r *http.Request) { reached.Add(1) })
				r := followupRequest(context.Background(), method, "")
				want := http.StatusUnauthorized
				switch name {
				case "other-account":
					store.key.OwnerID, want = "other", http.StatusNotFound
				case "other-key":
					store.key.KeyID, want = "other", http.StatusNotFound
				case "disabled":
					store.key.Enabled = false
				case "expired":
					expired := time.Now().Add(-time.Minute)
					store.key.ExpiresAt = &expired
				case "inactive-account":
					store.key.AccountActive = false
				case "model-revoked":
					store.key.AllowedModels, want = []string{"other"}, http.StatusForbidden
				case "missing-key":
					r.Header.Del("Authorization")
				case "unknown-id":
					r.SetPathValue("responseID", "resp_unknown")
					want = http.StatusNotFound
				case "invalid-id":
					r.SetPathValue("responseID", "../other")
					want = http.StatusNotFound
				}
				w := httptest.NewRecorder()
				e.ServeResponse(w, r, method == http.MethodPost)
				if w.Code != want || reached.Load() != 0 || len(store.finished) != 0 || len(e.followupSlots) != 0 {
					t.Fatalf("status=%d want=%d reached=%d finished=%d slots=%d", w.Code, want, reached.Load(), len(store.finished), len(e.followupSlots))
				}
			})
		}
	}
}

func TestBackgroundFollowupCapacityAndRelease(t *testing.T) {
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	var once sync.Once
	e, store, _ := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			io.WriteString(w, `{"id":"resp_followup","status":"queued"}`)
		case <-r.Context().Done():
		}
	})
	var wg sync.WaitGroup
	t.Cleanup(func() { once.Do(func() { close(release) }); wg.Wait() })
	results := make(chan int, 32)
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			w := httptest.NewRecorder()
			e.ServeResponse(w, followupRequest(context.Background(), http.MethodGet, ""), false)
			results <- w.Code
		})
	}
	for i := 0; i < 32; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent readers did not reach upstream")
		}
	}
	w := httptest.NewRecorder()
	e.ServeResponse(w, followupRequest(context.Background(), http.MethodPost, ""), true)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("full capacity accepted cancellation: %d", w.Code)
	}
	once.Do(func() { close(release) })
	wg.Wait()
	for i := 0; i < 32; i++ {
		if got := <-results; got != http.StatusOK {
			t.Fatalf("read status=%d", got)
		}
	}
	w = httptest.NewRecorder()
	e.ServeResponse(w, followupRequest(context.Background(), http.MethodPost, ""), true)
	if w.Code != http.StatusOK || len(e.followupSlots) != 0 || len(store.finished) != 0 {
		t.Fatalf("capacity not released or followups settled: status=%d slots=%d finished=%d", w.Code, len(e.followupSlots), len(store.finished))
	}
}

func TestBackgroundStreamDisconnectAndResume(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":11,\"delta\":\"first\"}\n\n"
	rest := "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":12,\"delta\":\"second\"}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":13}\n\n"
	disconnected := make(chan struct{})
	e, store, _ := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "true" || r.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
			t.Error("resume lost protocol parameters or original credential")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Query().Get("starting_after") == "11" {
			io.WriteString(w, rest)
			return
		}
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	})
	done := make(chan struct{}, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("responseID", "resp_followup")
		e.ServeResponse(w, r, false)
		done <- struct{}{}
	}))
	t.Cleanup(proxy.Close)
	client := proxy.Client()
	client.Timeout = 5 * time.Second
	get := func(query string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v1/responses/resp_followup"+query, nil)
		r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := get("?stream=true")
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text()+"\n\n" != first {
		response.Body.Close()
		t.Fatal("first stream event changed")
	}
	response.Body.Close()
	for _, signal := range []<-chan struct{}{disconnected, done} {
		select {
		case <-signal:
		case <-time.After(5 * time.Second):
			t.Fatal("disconnected stream retained upstream or slot")
		}
	}
	response = get("?stream=true&starting_after=11")
	payload, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(payload) != rest {
		t.Fatalf("resume=%q err=%v", payload, err)
	}
	<-done
	if len(e.followupSlots) != 0 || len(store.finished) != 0 {
		t.Fatal("stream recovery leaked a slot or created a charge")
	}
}

func TestBackgroundFollowupFailuresReleaseCapacity(t *testing.T) {
	for _, failure := range []string{"before-headers", "idle-stream", "connection-close", "partial-body", "upstream-error", "client-cancel"} {
		t.Run(failure, func(t *testing.T) {
			var requests atomic.Int32
			entered := make(chan struct{})
			e, store, _ := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) > 1 {
					io.WriteString(w, "recovered")
					return
				}
				close(entered)
				switch failure {
				case "before-headers", "client-cancel":
					<-r.Context().Done()
				case "idle-stream":
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {}\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "connection-close":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
				case "partial-body":
					w.Header().Set("Content-Length", "1000")
					io.WriteString(w, "partial")
				case "upstream-error":
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, `{"error":"synthetic-upstream-key"}`)
				}
			})
			e.CallTimeLimit = time.Second
			snap := e.settingsSnap.Load()
			snap.value = settings.Settings{DefaultTTFTTimeoutMS: 50, DefaultIdleTimeoutMS: 50}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { e.ServeResponse(w, followupRequest(ctx, http.MethodGet, "?stream=true"), false); close(done) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream not reached")
			}
			if failure == "client-cancel" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("failed followup did not finish")
			}
			want := http.StatusBadGateway
			if failure == "idle-stream" || failure == "partial-body" {
				want = http.StatusOK // headers were already forwarded
			} else if failure == "upstream-error" {
				want = http.StatusServiceUnavailable
			}
			if w.Code != want || strings.Contains(w.Body.String(), "synthetic-upstream-key") {
				t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
			}
			recovered := httptest.NewRecorder()
			e.ServeResponse(recovered, followupRequest(context.Background(), http.MethodGet, ""), false)
			if recovered.Code != http.StatusOK || recovered.Body.String() != "recovered" || len(e.followupSlots) != 0 || len(store.finished) != 0 {
				t.Fatal("failed followup leaked capacity or closed the task")
			}
		})
	}
}

func TestBackgroundConcurrentReadsCancellationAndSettlement(t *testing.T) {
	e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_followup","object":"response","status":"completed","usage":{"input_tokens":100,"output_tokens":20}}`)
	})
	release, _ := e.inflight.Acquire("consumer", 1)
	var channelReleased atomic.Int32
	e.jobs[job.CallID] = backgroundLease{account: release, channel: func() { channelReleased.Add(1) }}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 18; i++ {
		wg.Go(func() {
			<-start
			if i < 2 {
				e.pollBackgroundJob(context.Background(), job)
				return
			}
			method := http.MethodGet
			if i%2 == 0 {
				method = http.MethodPost
			}
			w := httptest.NewRecorder()
			e.ServeResponse(w, followupRequest(context.Background(), method, ""), method == http.MethodPost)
			if w.Code != http.StatusOK {
				t.Errorf("followup=%d", w.Code)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(store.finished) != 1 || !store.jobs[job.CallID].Settled || e.inflight.Count("consumer") != 0 || channelReleased.Load() != 1 || len(e.jobs) != 0 || len(e.followupSlots) != 0 {
		t.Fatalf("incomplete cleanup: finished=%d account=%d channel releases=%d jobs=%d reads=%d", len(store.finished), e.inflight.Count("consumer"), channelReleased.Load(), len(e.jobs), len(e.followupSlots))
	}
	if got := store.finished[job.CallID]; got.Cost.String() != "0.0002" || got.Bill == nil {
		t.Fatalf("wrong final charge: %+v", got)
	}
}

func TestBackgroundCapacityRestoreAndRelease(t *testing.T) {
	e, store, job := followupFixture(t, func(http.ResponseWriter, *http.Request) {})
	for i := 0; i < maxBackgroundJobs; i++ {
		job.CallID = fmt.Sprintf("call-%03d", i)
		store.jobs[job.CallID] = job
	}
	delete(store.jobs, "call")
	if err := e.RestoreBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.reserveBackground("excess") || e.inflight.Count("consumer") != maxBackgroundJobs {
		t.Fatal("restored jobs did not enforce capacity")
	}
	e.releaseBackground("call-000")
	e.releaseBackground("call-000")
	if !e.reserveBackground("replacement") || e.inflight.Count("consumer") != maxBackgroundJobs-1 {
		t.Fatal("released job did not restore capacity exactly once")
	}
	for id := range e.jobs {
		e.releaseBackground(id)
	}
	if e.inflight.Count("consumer") != 0 {
		t.Fatal("restored reservations leaked")
	}
}

// F0 discovery evidence reuses the real background path: the normal channel /
// model store is deliberately absent, as after business deletion. Only retained
// encrypted metadata may finish the already-registered call. New client reads
// and cancellation still authenticate the current platform Key.
func TestRegisteredBackgroundSurvivesRevocationWithOriginalCredentialAndQuote(t *testing.T) {
	var requests atomic.Int32
	e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
			t.Error("registered call changed upstream credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_followup","status":"completed","usage":{"input_tokens":100,"output_tokens":20}}`)
	})
	// Rotation retains v1 for historical snapshots while new encryption uses v2.
	rotated, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(make([]byte, 32))+",v2="+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("2", 32))), "v2")
	if err != nil {
		t.Fatal(err)
	}
	e.Keyring = rotated
	store.key.Enabled = false
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := httptest.NewRecorder()
		e.ServeResponse(w, followupRequest(t.Context(), method, ""), method == http.MethodPost)
		if w.Code != http.StatusUnauthorized || requests.Load() != 0 {
			t.Fatal("revoked Key could issue a new resource operation")
		}
	}
	if err := e.RestoreBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.pollBackgroundJob(t.Context(), job)
	got := store.finished[job.CallID]
	if requests.Load() != 1 || got.Cost.String() != "0.0002" || got.Bill == nil || !store.jobs[job.CallID].Settled || e.inflight.Count("consumer") != 0 {
		t.Fatalf("original background quote/credential was lost: requests=%d charge=%s settled=%t", requests.Load(), got.Cost.String(), store.jobs[job.CallID].Settled)
	}
	// The store's terminal-state guard, rather than a second inference charge,
	// remains the settlement boundary if the same terminal fact is observed twice.
	e.pollBackgroundJob(t.Context(), job)
	if len(store.finished) != 1 {
		t.Fatal("duplicate terminal observation charged twice")
	}
}

func TestGroupBackgroundRetainsOriginalEntranceOrderKeyAndQuote(t *testing.T) {
	for _, budget := range []int32{1, 2} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			var mu sync.Mutex
			seen := []string{}
			var engine *Engine
			e, store, job := followupFixture(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.URL.Path)
				mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
					t.Error("saved background request changed upstream credential")
				}
				if _, why := engine.Runtime.Check("channel", Limits{Concurrency: 1}, time.Now()); why != "concurrency" {
					t.Error("background address fallback released its original slot")
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/one/") {
					w.WriteHeader(503)
					return
				}
				_, _ = io.WriteString(w, `{"id":"resp_followup","status":"completed","usage":{"input_tokens":100,"output_tokens":20}}`)
			})
			engine = e
			var metadata backgroundMetadata
			if err := json.Unmarshal(job.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			base := metadata.Candidate.BaseURL
			metadata.Candidate.GroupID = "saved-group"
			metadata.Candidate.GroupModelID = "saved-offer"
			metadata.Candidate.CredentialScope = "saved-credential-scope"
			metadata.Candidate.Entrances = []Entrance{{ID: "one", BaseURL: base + "/one"}, {ID: "two", BaseURL: base + "/two"}}
			metadata.Candidate.BaseURL = base + "/removed-live-address"
			metadata.Settings.DefaultMaxAttempts = budget
			quote := market.Quote{ID: "saved-quote", GroupModelID: "saved-offer", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: 1_000_000_000, Output: 5_000_000_000}}
			frozen, err := quotepricing.Freeze(quotepricing.Book{GroupID: "saved-group", GroupRevision: 1, Offers: map[string]quotepricing.Offer{"main": {ModelID: "main", Revision: 1, Quote: quote}}})
			if err != nil {
				t.Fatal(err)
			}
			metadata.Candidate.Quotes = frozen
			job.Metadata, err = json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			store.jobs[job.CallID] = job
			// Live routing is unavailable/revoked; polling uses only the registered
			// execution snapshot. Changing a mutable source quote cannot reprice it.
			store.key.Enabled = false
			quote.Prices.Input = 999_000_000_000
			if err = e.RestoreBackground(t.Context()); err != nil {
				t.Fatal(err)
			}
			e.pollBackgroundJob(t.Context(), job)
			mu.Lock()
			actual := append([]string{}, seen...)
			mu.Unlock()
			want := []string{"/one/v1/responses/resp_followup"}
			if budget == 2 {
				want = append(want, "/two/v1/responses/resp_followup")
			}
			if strings.Join(actual, ",") != strings.Join(want, ",") {
				t.Fatal("saved entrance sequence/budget lost", actual)
			}
			if budget == 2 {
				got := store.finished[job.CallID]
				if got.Cost.String() != "0.0002" || got.Bill == nil || !store.jobs[job.CallID].Settled || e.inflight.Count("consumer") != 0 {
					t.Fatal("background lost original group billing or release", got.Cost.String())
				}
			} else {
				if len(store.finished) != 0 || store.jobs[job.CallID].Settled {
					t.Fatal("failed poll pretended to settle")
				}
				e.releaseBackground(job.CallID)
			}
		})
	}
}
