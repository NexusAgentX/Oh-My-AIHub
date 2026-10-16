package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type protocolErrorStore struct {
	serveStore
	candidate          Candidate
	fallbackCredential channel.EncryptedCredential
}

func (s *protocolErrorStore) Candidates(context.Context, string, channel.Format) ([]Candidate, error) {
	// A second candidate makes an unintended replay observable.
	other := s.candidate
	other.ChannelID = "zz-fallback"
	other.Credential = s.fallbackCredential
	return []Candidate{s.candidate, other}, nil
}

type defaultRouting struct{ routing.Store }

func (defaultRouting) Resolve(_ context.Context, _, _, model string) (routing.Pref, error) {
	pref := routing.Default(model)
	pref.Order = []string{"upstream", "second", "zz-fallback"}
	return pref, nil
}

// Exercise the public Engine handler over HTTP, an actual controlled HTTP
// upstream, forwarding, final diagnostics, health and the settlement boundary.
// Storage is captured in memory; no upstream subscription or database is used.
func TestHTTP200ProtocolErrorEndToEnd(t *testing.T) {
	for _, protocol := range protocolErrorCases {
		for _, withUsage := range []bool{false, true} {
			t.Run(protocol.name+"/"+map[bool]string{false: "without_usage", true: "with_usage"}[withUsage], func(t *testing.T) {
				payload := protocol.content
				if withUsage {
					payload += protocol.usage
				}
				payload += protocol.failure
				var requests atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, payload)
				}))
				defer upstream.Close()
				run := newCredentialRun(t, http.StatusOK)
				store := &protocolErrorStore{fallbackCredential: run.seal("zz-fallback", "sk-test"), candidate: Candidate{Advanced: channel.Advanced{Capabilities: channel.CapabilityList(channel.ChannelCapabilities)}, Capabilities: channel.CapabilityList(channel.ModelCapabilities), ChannelID: "upstream", OwnerID: "provider", BaseURL: upstream.URL, UpstreamModel: "upstream-model", MultiplierNano: ledger.UnitRate, Credential: run.seal("upstream", "sk-test")}}
				fixture := newServeFixture(t, 10, defaultRouting{})
				engine := fixture.engine
				engine.Store, engine.Keyring = store, run.keyring
				engine.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 2, DefaultCooldownFailures: 3, DefaultCooldownSeconds: 60}, outbound: fakeOutbound{transport: http.DefaultTransport}})
				finished := make(chan struct{}, 3)
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					pathModel := ""
					if protocol.format == channel.FormatGemini {
						pathModel = "gpt-test"
					}
					engine.Serve(w, r, protocol.format, pathModel, true)
					finished <- struct{}{}
				}))
				defer gateway.Close()
				for attemptIndex := range 3 {
					request, err := http.NewRequest(http.MethodPost, gateway.URL+benchStreams[protocol.name].path, strings.NewReader(`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`))
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Authorization", "Bearer "+apikey.Prefix+"test")
					response, err := gateway.Client().Do(request)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != http.StatusOK || string(body) != payload || requests.Load() != int32(attemptIndex+1) {
						t.Fatalf("status=%d body=%q upstream requests=%d", response.StatusCode, body, requests.Load())
					}
					<-finished
					if len(store.finished) != attemptIndex+1 {
						t.Fatalf("finished %d calls", len(store.finished))
					}
					finish := store.finished[attemptIndex]
					t.Logf("HTTP 200 with usage=%v: outcome=%s attempt=%s usage=%+v bill=%v", withUsage, finish.Outcome, finish.Attempts[0].EndReason, finish.Usage, finish.Bill)
					if finish.Outcome != OutcomeUpstreamFailed || finish.Attempts[0].EndReason != EndUpstreamError {
						t.Fatalf("protocol failure misclassified: %+v", finish)
					}
					if finish.Bill != nil || finish.Cost != 0 || finish.Fee != 0 {
						t.Fatal("explicit upstream failure must not be charged")
					}
					if withUsage && (finish.Usage.InputTokens != 7 || finish.Usage.OutputTokens != 3 || len(finish.PriceSnapshot) == 0) {
						t.Fatal("reliable usage or snapshot lost")
					}
					engine.RefreshStats()
					if stats := engine.Stats()["upstream"]; stats.Attempts != int64(attemptIndex+1) || stats.Successes != 0 {
						t.Fatalf("health=%+v", stats)
					}
					cooling := !engine.Runtime.CooldownUntil("upstream", time.Now()).IsZero()
					if cooling != (attemptIndex == 2) {
						t.Fatalf("attempt %d: cooling=%v, threshold is 3", attemptIndex+1, cooling)
					}
				}
			})
		}
	}
}
