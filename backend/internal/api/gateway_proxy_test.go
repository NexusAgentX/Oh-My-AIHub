package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type proxyStore struct {
	gateway.Store
	candidate gateway.Candidate
	mu        sync.Mutex
	finished  []gateway.CallFinish
	count     int
	key       gateway.KeyAuth
}

func (s *proxyStore) LookupKey(context.Context, []byte) (gateway.KeyAuth, error) { return s.key, nil }
func (s *proxyStore) TouchKey(context.Context, string, time.Time) error          { return nil }
func (s *proxyStore) Points(context.Context, string) (ledger.Points, error) {
	return ledger.Points{Balance: money.FromNano(100_000_000_000)}, nil
}
func (s *proxyStore) Candidates(context.Context, string, channel.Format) ([]gateway.Candidate, error) {
	return []gateway.Candidate{s.candidate}, nil
}
func (s *proxyStore) AuxiliaryCandidates(context.Context, channel.Format) ([]gateway.CandidateRecord, error) {
	return []gateway.CandidateRecord{{ModelID: "platform/model:2026", Formats: []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini, channel.FormatOpenAIImages}, Candidate: s.candidate}}, nil
}
func (s *proxyStore) InsertCall(context.Context, gateway.CallInsert) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	return fmt.Sprintf("fixture-call-%d", s.count), nil
}
func (s *proxyStore) FinishCall(_ context.Context, f gateway.CallFinish) (gateway.FinishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, f)
	return gateway.FinishResult{Finished: true}, nil
}

type proxyModels struct{ gateway.Models }

func (proxyModels) Get(context.Context, string) (catalog.Model, error) {
	return catalog.Model{ID: "platform/model:2026", Enabled: true, PriceCurrency: "CNY"}, nil
}

func TestGatewayF7EveryNativeEndpointUsesRealEntryAndScope(t *testing.T) {
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range manifest.Endpoints {
		// Only stateless inference is served now; the retired resource, cache
		// and token-counting paths are covered by TestRetiredGatewayEndpointsAnswerNotFound.
		if ep.Feature != "F7" || ep.Replay != "inference" || ep.Retired {
			continue
		}
		t.Run(ep.ID, func(t *testing.T) {
			seen := 0
			var inferenceBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
				if ep.ID == "responses.create" {
					inferenceBody, _ = io.ReadAll(r.Body)
				}
				if r.Header.Get("Authorization") != "Bearer upstream-synthetic" {
					t.Error("platform key or missing native auth reached upstream")
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
					_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
					if err != nil {
						t.Error(err)
						return
					}
					reader := multipart.NewReader(r.Body, params["boundary"])
					modelSeen, imageSeen := false, false
					for {
						part, e := reader.NextRawPart()
						if e == io.EOF {
							break
						}
						if e != nil {
							t.Error(e)
							return
						}
						data, _ := io.ReadAll(part)
						if part.FormName() == "model" {
							modelSeen = string(data) == "native/model:2026"
						}
						if part.FileName() != "" {
							imageSeen = bytes.Equal(data, []byte{0, 1, 2, 3, 255})
						}
					}
					if !modelSeen || !imageSeen {
						t.Error("multipart model or file bytes changed")
					}
				}
				raw := `{}`
				switch ep.ID {
				case "chat.create":
					raw = `{"id":"chat-own","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
				case "responses.create":
					raw = `{"object":"response","id":"resp-own","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				case "images.generations", "images.edits", "images.variations":
					raw = `{"created":1,"data":[{"url":"https://synthetic.invalid/image"}],"size":"1024x1024"}`
				case "messages.create":
					raw = `{"id":"message-native","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
				case "gemini.generateContent", "gemini.streamGenerateContent":
					raw = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`
				default:
					t.Errorf("missing endpoint fixture: %s", ep.ID)
				}
				_, _ = w.Write([]byte(raw))
			}))
			defer upstream.Close()
			keyring, e := channel.ParseKeyring(testKeyring, "k1")
			if e != nil {
				t.Fatal(e)
			}
			sealed, e := keyring.Encrypt("channel-own", 1, "upstream-synthetic")
			if e != nil {
				t.Fatal(e)
			}
			book, e := quotepricing.Freeze(quotepricing.Book{GroupID: "group-own", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", UpstreamModel: "native/model:2026", Revision: 1, Quote: market.Quote{ID: "quote-own", GroupModelID: "offering-own", Version: 1, Currency: "CNY", BillingUnit: "per_successful_image", UnitPrices: []market.UnitPrice{{Unit: "successful_image", Price: money.FromNano(1_000_000_000)}}}}}})
			if e != nil {
				t.Fatal(e)
			}
			proof := map[string]bool{}
			for _, suite := range manifest.Suites {
				for _, req := range suite.Requirements {
					for _, label := range req.Assertions {
						proof["upstream_protocol/"+req.ID+"/"+label] = true
					}
				}
			}
			candidate := gateway.Candidate{GroupID: "group-own", GroupModelID: "offering-own", ChannelID: "channel-own", CredentialRevision: 1, CredentialScope: "synthetic-actual-scope", ConfigRevision: 1, OwnerID: "provider", BaseURL: "https://synthetic.example", UpstreamModel: "native/model:2026", Credential: sealed, Quotes: book, MultiplierNano: ledger.UnitRate, Evidence: proof}
			store := &proxyStore{candidate: candidate, key: gateway.KeyAuth{OwnerID: "account-own", KeyID: "key-own", AccountActive: true, Enabled: true}}
			engine := gateway.NewEngine(gateway.Dependencies{Store: store, Catalog: proxyModels{}, Settings: settings.NewService(newFakeStore()), Routing: newFakeRoutingStore(), Keyring: keyring, Outbound: newTestOutbound(t, upstream)})
			handler := NewHandler(Dependencies{Gateway: engine})
			path := strings.NewReplacer("{model}", "platform%2Fmodel:2026").Replace(ep.Path)
			body := []byte(`{"model":"platform/model:2026","input":"synthetic","messages":[{"role":"user","content":"synthetic"}],"contents":[{"parts":[{"text":"synthetic"}]}],"store":true}`)
			contentType := "application/json"
			if ep.ID == "images.edits" || ep.ID == "images.variations" {
				var form bytes.Buffer
				writer := multipart.NewWriter(&form)
				_ = writer.WriteField("model", "platform/model:2026")
				file, e := writer.CreateFormFile("image", "synthetic.png")
				if e != nil {
					t.Fatal(e)
				}
				_, _ = file.Write([]byte{0, 1, 2, 3, 255})
				_ = writer.Close()
				body = form.Bytes()
				contentType = writer.FormDataContentType()
			}
			req := httptest.NewRequest(ep.Method, path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer sk-aih-synthetic")
			req.Header.Set("Content-Type", contentType)
			if ep.Protocol == "native-messages" {
				req.Header.Set("Anthropic-Version", "2023-06-01")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code < 200 || recorder.Code >= 300 || seen != 1 {
				t.Fatalf("entry failed: %s %s =%d %s upstream=%d", ep.Method, path, recorder.Code, recorder.Body.String(), seen)
			}
			if strings.HasPrefix(ep.ID, "images.") {
				if len(store.finished) != 1 || store.finished[0].Cost != money.FromNano(1_000_000_000) || store.finished[0].TokensPerSecond != nil {
					t.Fatal("image product billed incorrectly", store.finished)
				}
			}
			if ep.ID == "responses.create" {
				// A continuation needs the earlier turn, which the gateway does not
				// keep: the request is refused instead of answered from nothing.
				follow := httptest.NewRecorder()
				request := httptest.NewRequest("POST", path, strings.NewReader(`{"previous_response_id":"resp-own","input":"synthetic-followup","store":false}`))
				request.Header.Set("Authorization", "Bearer sk-aih-synthetic")
				handler.ServeHTTP(follow, request)
				if follow.Code != 400 || !strings.Contains(follow.Body.String(), "stateful_request_unsupported") {
					t.Fatal("a previous-response request was served", follow.Code, follow.Body.String())
				}
				// Self-contained state must work through a different channel
				// without any resource lookup or source binding.
				store.candidate.ChannelID = "channel-second"
				store.candidate.Credential, e = keyring.Encrypt("channel-second", 1, "upstream-synthetic")
				if e != nil {
					t.Fatal(e)
				}
				portable := `{"model":"platform/model:2026","store":false,"input":[{"type":"reasoning","id":"unregistered-internal","encrypted_content":"byte-exact-cipher"},{"type":"compaction","id":"opaque-compact","encrypted_content":"byte-exact-compact"}],"unknown":{"signature":"byte-exact-signature"}}`
				request = httptest.NewRequest("POST", path, strings.NewReader(portable))
				request.Header.Set("Authorization", "Bearer sk-aih-synthetic")
				cross := httptest.NewRecorder()
				handler.ServeHTTP(cross, request)
				want := strings.Replace(portable, `"platform/model:2026"`, `"native/model:2026"`, 1)
				if cross.Code != 200 || string(inferenceBody) != want || store.finished[len(store.finished)-1].ChannelID != "channel-second" {
					t.Fatal("self-contained state became sticky or changed", cross.Code, string(inferenceBody))
				}
			}
		})
	}
}
