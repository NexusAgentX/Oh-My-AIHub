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
	}{
		{"address then channel", []int{500, 502, 200}, 3, []string{"a1", "a2", "b1"}, 200, false, true},
		{"same engine recovers", []int{500, 200, 200}, 3, []string{"a1", "a2"}, 200, false, false},
		{"global budget stops before B slot", []int{500, 502, 200}, 2, []string{"a1", "a2"}, 502, false, true},
		{"request error is final", []int{400, 200, 200}, 3, []string{"a1"}, 400, false, false},
		{"rate limit cools whole channel", []int{429, 200, 200}, 3, []string{"a1", "b1"}, 200, false, true},
		{"output already delivered", []int{200, 200, 200}, 3, []string{"a1"}, 200, true, false},
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
			engine.Store, engine.Keyring = store, run.keyring
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
			if aRPM != 1 || inflight != 0 || bRPM != map[bool]int{true: 1, false: 0}[slices.Contains(tc.want, "b1")] {
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
