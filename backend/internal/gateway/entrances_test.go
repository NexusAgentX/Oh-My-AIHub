package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type entranceStore struct {
	serveStore
	candidates []Candidate
}

func (s *entranceStore) Candidates(context.Context, string, channel.Format) ([]Candidate, error) {
	return slices.Clone(s.candidates), nil
}

const entranceJSON = `{"id":"synthetic","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`

func TestEngineUsesOrderedEntrancesWithOneChannelSlotAndGlobalAttemptLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statuses  []int
		budget    int
		want      []string
		status    int
		stream    bool
		cooldownA bool
		occupiedA bool
	}{
		{"address then channel", []int{500, 502, 200}, 3, []string{"a1", "a2", "b1"}, 200, false, true, false},
		{"same engine recovers", []int{500, 200, 200}, 3, []string{"a1", "a2"}, 200, false, false, false},
		{"global budget stops before B slot", []int{500, 502, 200}, 2, []string{"a1", "a2"}, 502, false, true, false},
		{"request error is final", []int{400, 200, 200}, 3, []string{"a1"}, 400, false, false, false},
		{"rate limit cools whole channel", []int{429, 200, 200}, 3, []string{"a1", "b1"}, 200, false, true, false},
		{"output already delivered", []int{200, 200, 200}, 3, []string{"a1"}, 200, true, false, false},
		{"occupied channel skips all its entrances", []int{200, 200, 200}, 1, []string{"b1"}, 200, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			run := newCredentialRun(t, 200)
			fixture := newServeFixture(t, 10, defaultRouting{})
			engine := fixture.engine
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/v1/chat/completions")
				index := slices.Index([]string{"a1", "a2", "b1"}, name)
				if index < 0 {
					t.Errorf("unexpected native path %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				mu.Lock()
				seen = append(seen, name)
				mu.Unlock()
				channelID, key := "a", "synthetic-a-key"
				if name == "b1" {
					channelID, key = "b", "synthetic-b-key"
				}
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("wrong channel credential")
				}
				if _, why := engine.Runtime.Check(channelID, Limits{Concurrency: 1}, time.Now()); why != "concurrency" {
					t.Errorf("channel slot released between entrances: %s", why)
				}
				status := tc.statuses[index]
				if status == 429 {
					w.Header().Set("Retry-After", "1")
				}
				if tc.stream && status == 200 {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"visible\"}}]}\n\n")
					w.(http.Flusher).Flush()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = io.WriteString(w, entranceJSON)
				} else {
					_, _ = io.WriteString(w, `{"error":{"type":"synthetic_error","message":"synthetic upstream failure"}}`)
				}
			}))
			defer up.Close()
			one := int32(1)
			store := &entranceStore{candidates: []Candidate{{ChannelID: "a", OwnerID: "provider", BaseURL: up.URL + "/ignored", UpstreamModel: "native", Credential: run.seal("a", "synthetic-a-key"), MultiplierNano: ledger.UnitRate, Advanced: channel.Advanced{ConcurrencyLimit: &one, RPMLimit: &one}, Entrances: []Entrance{{ID: "a1", BaseURL: up.URL + "/a1"}, {ID: "a2", BaseURL: up.URL + "/a2"}}}, {ChannelID: "b", OwnerID: "provider", BaseURL: up.URL + "/b1", UpstreamModel: "native", Credential: run.seal("b", "synthetic-b-key"), MultiplierNano: ledger.UnitRate, Advanced: channel.Advanced{ConcurrencyLimit: &one, RPMLimit: &one}, Entrances: []Entrance{{ID: "b1", BaseURL: up.URL + "/b1"}}}}}
			for i := range store.candidates {
				store.candidates[i] = quotedCandidate(t, store.candidates[i], catalog.Model{ID: "gpt-test"})
			}
			engine.Store, engine.Keyring = store, run.keyring
			if tc.occupiedA {
				defer engine.Runtime.RestoreInflight("a")()
			}
			engine.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: int32(tc.budget), DefaultCooldownFailures: 1, DefaultCooldownSeconds: 60}, outbound: fakeOutbound{transport: http.DefaultTransport}})
			body := `{"model":"gpt-test"}`
			if tc.stream {
				body = `{"model":"gpt-test","stream":true}`
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
			w := httptest.NewRecorder()
			engine.Serve(w, r, channel.FormatOpenAIChat, "", true)
			mu.Lock()
			actual := slices.Clone(seen)
			mu.Unlock()
			if w.Code != tc.status || !slices.Equal(actual, tc.want) {
				t.Fatalf("HTTP %d sequence=%v body=%s", w.Code, actual, w.Body.String())
			}
			if len(store.finished) != 1 || len(store.finished[0].Attempts) != len(tc.want) {
				t.Fatalf("attempt timeline incomplete: %+v", store.finished)
			}
			attempts := store.finished[0].Attempts
			for i, a := range attempts {
				wantLevel := ""
				if i > 0 {
					wantLevel = "address"
					if tc.want[i] == "b1" {
						wantLevel = "channel"
					}
				}
				if a.AddressID != tc.want[i] || a.FallbackLevel != wantLevel || a.SelectionFinal == nil {
					t.Fatalf("missing entrance diagnostics: %+v", a)
				}
				if tc.want[i] == "a1" && len(tc.want) > 1 && tc.want[1] == "a2" && *a.SelectionFinal {
					t.Fatal("intermediate address became channel health result")
				}
			}
			engine.Runtime.mutex.Lock()
			a := engine.Runtime.state("a")
			b := engine.Runtime.state("b")
			aRPM, bRPM, inflight := len(a.requests), len(b.requests), a.inflight+b.inflight
			engine.Runtime.mutex.Unlock()
			wantARPM, wantInflight := 1, 0
			if tc.occupiedA {
				wantARPM, wantInflight = 0, 1
			}
			if aRPM != wantARPM || inflight != wantInflight || bRPM != map[bool]int{true: 1, false: 0}[slices.Contains(tc.want, "b1")] {
				t.Fatalf("address count amplified admission: a=%d b=%d inflight=%d", aRPM, bRPM, inflight)
			}
			if cooling := !engine.Runtime.CooldownUntil("a", time.Now()).IsZero(); cooling != tc.cooldownA {
				t.Fatalf("channel health counted intermediate address: cooling=%v", cooling)
			}
			stats := engine.StatsBook.view(time.Now()).channels
			if s, ok := stats["a"]; ok && s.Attempts > 1 {
				t.Fatalf("address count amplified health: %+v", s)
			}
			if tc.name == "same engine recovers" && stats["a"].Successes != 1 {
				t.Fatal("address recovery was not one channel success")
			}
			if tc.stream && !strings.Contains(w.Body.String(), "visible") {
				t.Fatal("stream output lost")
			}
		})
	}
}

func TestEntranceTimeoutAndCancellationUseOneCallDeadline(t *testing.T) {
	for _, kind := range []string{"address_ttft", "call_deadline", "client_cancel"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			var mu sync.Mutex
			seen := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/v1/chat/completions")
				mu.Lock()
				seen = append(seen, name)
				mu.Unlock()
				if name == "a1" {
					close(entered)
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, entranceJSON)
			}))
			defer up.Close()
			run := newCredentialRun(t, 200)
			fixture := newServeFixture(t, 10, defaultRouting{})
			e := fixture.engine
			store := &entranceStore{candidates: []Candidate{{ChannelID: "a", OwnerID: "provider", Name: "A", Credential: run.seal("a", "synthetic-key"), MultiplierNano: ledger.UnitRate, Entrances: []Entrance{{ID: "a1", BaseURL: up.URL + "/a1"}, {ID: "a2", BaseURL: up.URL + "/a2"}}}}}
			store.candidates[0] = quotedCandidate(t, store.candidates[0], catalog.Model{ID: "gpt-test"})
			e.Store, e.Keyring = store, run.keyring
			e.CallTimeLimit = time.Second
			firstByte := int32(20)
			if kind != "address_ttft" {
				firstByte = 500
			}
			if kind == "call_deadline" {
				e.CallTimeLimit = 40 * time.Millisecond
			}
			e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 3, DefaultTTFTTimeoutMS: firstByte, DefaultCooldownFailures: 1, DefaultCooldownSeconds: 60}, outbound: fakeOutbound{transport: http.DefaultTransport}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true}`)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { e.Serve(w, r, channel.FormatOpenAIChat, "", true); close(done) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("first entrance not reached")
			}
			if kind == "client_cancel" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("call did not finish within its original budget")
			}
			mu.Lock()
			actual := slices.Clone(seen)
			mu.Unlock()
			want := []string{"a1"}
			if kind == "address_ttft" {
				want = append(want, "a2")
			}
			if !slices.Equal(actual, want) {
				t.Fatal("timeout/cancel changed entrance retry boundary", actual)
			}
			if len(store.finished) != 1 {
				t.Fatal("call did not settle")
			}
			if kind == "address_ttft" && w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			e.Runtime.mutex.Lock()
			inflight := e.Runtime.state("a").inflight
			e.Runtime.mutex.Unlock()
			if inflight != 0 {
				t.Fatal("channel slot leaked")
			}
		})
	}
}
