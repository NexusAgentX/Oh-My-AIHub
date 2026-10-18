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
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type proxyRecords struct {
	mu     sync.Mutex
	rows   []resourcestate.Record
	writes int
}

func (s *proxyRecords) Lookup(_ context.Context, ref gatewaycontract.Reference) ([]resourcestate.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []resourcestate.Record{}
	for _, r := range s.rows {
		if r.Kind == ref.Kind && r.ID == ref.ID && (ref.ParentID == "" || ref.ParentID == r.ParentID) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *proxyRecords) Observe(_ context.Context, rows []resourcestate.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	for _, r := range rows {
		if !resourcestate.ValidRecord(r) {
			return gatewaycontract.ErrConflict
		}
		found := false
		for i, old := range s.rows {
			if old.Kind == r.Kind && old.ID == r.ID && old.ParentID == r.ParentID && old.Binding.GroupID == r.Binding.GroupID && old.CredentialScope == r.CredentialScope {
				found = true
				if old.Resource != r.Resource || old.ModelID != r.ModelID || old.Quarantined {
					s.rows[i].Quarantined = true
					return resourcestate.ErrQuarantined
				}
			}
		}
		if !found {
			s.rows = append(s.rows, r)
		}
	}
	return nil
}
func (s *proxyRecords) Delete(_ context.Context, p gatewaycontract.Principal, ref gatewaycontract.Reference, b gatewaycontract.Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.Kind == ref.Kind && r.ID == ref.ID && r.AccountID == p.AccountID && r.KeyID == p.KeyID && r.Binding == b {
			s.rows[i].Deleted = true
		}
	}
	return nil
}

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
		if ep.Feature != "F7" {
			continue
		}
		t.Run(ep.ID, func(t *testing.T) {
			seen := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
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
				if ep.ID == "responses.get" && r.URL.Query().Get("stream") == "true" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"object\":\"response\",\"id\":\"resp-own\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"item-stream-new\"}}\n\n")
					return
				}
				raw := `{}`
				switch ep.ID {
				case "chat.create":
					raw = `{"id":"chat-own","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
				case "chat.list":
					raw = `{"object":"list","data":[{"id":"chat-foreign"},{"id":"chat-own"}],"has_more":false,"total":999}`
				case "chat.get", "chat.update":
					raw = `{"object":"chat.completion","id":"chat-own","choices":[]}`
				case "chat.delete":
					raw = `{"id":"chat-own","deleted":true}`
				case "chat.messages", "responses.items", "conversations.items.list":
					raw = `{"object":"list","data":[{"id":"item-own","type":"message"},{"id":"item-foreign","type":"message"}],"has_more":false}`
				case "responses.create", "responses.get", "responses.cancel":
					raw = `{"object":"response","id":"resp-own","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				case "responses.delete":
					raw = `{"id":"resp-own","deleted":true}`
				case "responses.count":
					raw = `{"input_tokens":7}`
				case "responses.compact":
					raw = `{"object":"response.compaction","id":"opaque-compact","output":[{"type":"compaction","id":"opaque-inner","encrypted_content":"unchanged-cipher"}],"usage":{"input_tokens":1,"output_tokens":1}}`
				case "conversations.create", "conversations.get", "conversations.update":
					raw = `{"object":"conversation","id":"conv-own","metadata":{"synthetic":"preserved"}}`
				case "conversations.delete":
					raw = `{"id":"conv-own","deleted":true}`
				case "conversations.items.create":
					raw = `{"object":"list","data":[{"type":"message","id":"item-new"}]}`
				case "conversations.items.get":
					raw = `{"type":"message","id":"item-own"}`
				case "conversations.items.delete":
					raw = `{"id":"item-own","deleted":true}`
				case "images.generations", "images.edits", "images.variations":
					raw = `{"created":1,"data":[{"url":"https://synthetic.invalid/image"}],"size":"1024x1024"}`
				case "messages.create":
					raw = `{"id":"message-native","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
				case "messages.count":
					raw = `{"input_tokens":7}`
				case "gemini.generateContent", "gemini.streamGenerateContent":
					raw = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`
				case "gemini.countTokens":
					raw = `{"totalTokens":7}`
				case "cache.create", "cache.get", "cache.update":
					raw = `{"name":"cachedContents/cache-own","model":"models/native/model:2026","ttl":"300s"}`
				case "cache.list":
					raw = `{"cachedContents":[{"name":"cachedContents/cache-foreign"},{"name":"cachedContents/cache-own"}],"total":999}`
				case "cache.delete":
					w.WriteHeader(204)
					return
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
			records := &proxyRecords{}
			seed := func(kind, id, parent string) {
				records.rows = append(records.rows, resourcestate.Record{Resource: gatewaycontract.Resource{Kind: kind, ID: id, ParentID: parent, AccountID: "account-own", KeyID: "key-own", Binding: gatewaycontract.Binding{GroupID: "group-own", ChannelID: "channel-own", CredentialVersion: "1"}, CredentialScope: "synthetic-actual-scope", CreationID: "fixture-origin"}, ModelID: "platform/model:2026"})
			}
			if ep.ID != "chat.create" {
				seed("stored_chat", "chat-own", "")
			}
			if ep.ID != "responses.create" {
				seed("response", "resp-own", "")
			}
			if ep.ID != "conversations.create" {
				seed("conversation", "conv-own", "")
			}
			if ep.ID != "cache.create" {
				seed("cache", "cachedContents/cache-own", "")
			}
			seed("item", "item-own", "conv-own")
			seed("item", "item-own", "resp-own")
			seed("item", "item-own", "chat-own")
			for _, parent := range []string{"conv-own", "resp-own", "chat-own"} {
				seed("item", "item-foreign", parent)
				records.rows[len(records.rows)-1].KeyID = "foreign-key"
				records.rows[len(records.rows)-1].AccountID = "foreign-account"
			}

			codec, e := gatewaycontract.NewCursorCodec(make([]byte, 32))
			if e != nil {
				t.Fatal(e)
			}
			engine := gateway.NewEngine(gateway.Dependencies{Store: store, Catalog: proxyModels{}, Settings: settings.NewService(newFakeStore()), Routing: newFakeRoutingStore(), Keyring: keyring, Outbound: newTestOutbound(t, upstream), Resources: records, ResourceCursors: codec})
			handler := NewHandler(Dependencies{Gateway: engine})
			path := strings.NewReplacer("{completion_id}", "chat-own", "{response_id}", "resp-own", "{conversation_id}", "conv-own", "{item_id}", "item-own", "{cache_id}", "cache-own", "{model}", "platform%2Fmodel:2026").Replace(ep.Path)
			body := []byte(`{"model":"platform/model:2026","input":"synthetic","messages":[{"role":"user","content":"synthetic"}],"contents":[{"parts":[{"text":"synthetic"}]}],"items":[],"store":true}`)
			if ep.Method == "GET" || ep.Method == "DELETE" {
				body = nil
			}
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
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code < 200 || recorder.Code >= 300 || seen != 1 {
				t.Fatalf("entry failed: %s %s =%d %s upstream=%d", ep.Method, path, recorder.Code, recorder.Body.String(), seen)
			}
			if strings.HasSuffix(ep.ID, ".list") || ep.ID == "chat.messages" || ep.ID == "responses.items" {
				if strings.Contains(recorder.Body.String(), "foreign") || strings.Contains(recorder.Body.String(), "999") {
					t.Fatal("shared list leaked another owner or total", recorder.Body.String())
				}
			}
			if strings.HasPrefix(ep.ID, "images.") {
				if len(store.finished) != 1 || store.finished[0].Cost != money.FromNano(1_000_000_000) || store.finished[0].TokensPerSecond != nil {
					t.Fatal("image product billed incorrectly", store.finished)
				}
			}
			if ep.ID == "responses.get" {
				stream := httptest.NewRecorder()
				request := httptest.NewRequest("GET", path+"?stream=true", nil)
				request.Header.Set("Authorization", "Bearer sk-aih-synthetic")
				handler.ServeHTTP(stream, request)
				if stream.Code != 200 || !strings.Contains(stream.Body.String(), "item-stream-new") {
					t.Fatal("resource stream failed", stream.Code, stream.Body.String())
				}
				rows, e := records.Lookup(context.Background(), gatewaycontract.Reference{Kind: "item", ID: "item-stream-new", ParentID: "resp-own"})
				if e != nil || len(rows) != 1 || rows[0].CreationID != "fixture-origin" {
					t.Fatal("stream child escaped original create chain", rows, e)
				}
			}
			if ep.ID == "responses.create" {
				follow := httptest.NewRecorder()
				request := httptest.NewRequest("POST", path, strings.NewReader(`{"previous_response_id":"resp-own","input":"synthetic-followup","store":false}`))
				request.Header.Set("Authorization", "Bearer sk-aih-synthetic")
				handler.ServeHTTP(follow, request)
				if follow.Code != 200 || store.finished[len(store.finished)-1].ModelID != "platform/model:2026" {
					t.Fatal("verified previous-response model context was not used", follow.Code, follow.Body.String())
				}
			}
			if ep.Billing == "none" && len(store.finished) != 0 {
				t.Fatal("metadata read/write created an inference charge")
			}
			// The same valid platform account using a different Key cannot read or
			// mutate an existing resource, even with a known native identifier.
			if len(body) == 0 && strings.Contains(ep.Path, "{") || ep.ID == "responses.cancel" {
				store.key.KeyID = "other-key"
				before := seen
				denied := httptest.NewRecorder()
				again := httptest.NewRequest(ep.Method, path, bytes.NewReader(body))
				again.Header.Set("Authorization", "Bearer sk-aih-other")
				handler.ServeHTTP(denied, again)
				if denied.Code < 400 || seen != before {
					t.Fatal("another Key reached owned resource", denied.Code, seen, before)
				}
			}
		})
	}
}
