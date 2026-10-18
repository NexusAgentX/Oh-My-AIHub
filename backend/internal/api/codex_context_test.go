package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type codexRouteStore struct {
	proxyStore
	rows []gateway.CandidateRecord
}

type codexFixtureModels struct{ proxyModels }

func (codexFixtureModels) Get(_ context.Context, id string) (catalog.Model, error) {
	return catalog.Model{ID: id, Enabled: true, PriceCurrency: "CNY"}, nil
}

func (s *codexRouteStore) AuxiliaryCandidates(context.Context, channel.Format) ([]gateway.CandidateRecord, error) {
	return s.rows, nil
}

func (s *codexRouteStore) Candidates(_ context.Context, model string, format channel.Format) ([]gateway.Candidate, error) {
	out := []gateway.Candidate{}
	for _, r := range s.rows {
		if r.ModelID == model {
			for _, f := range r.Formats {
				if f == format {
					out = append(out, r.Candidate)
					break
				}
			}
		}
	}
	return out, nil
}

func newCodexContextFixture(t *testing.T, upstream *httptest.Server) (http.Handler, *codexRouteStore, *proxyRecords) {
	t.Helper()
	keyring, err := channel.ParseKeyring("test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	store := &codexRouteStore{proxyStore: proxyStore{key: gateway.KeyAuth{OwnerID: "account-own", KeyID: "key-own", AccountActive: true, Enabled: true}}}
	for _, id := range []string{"a", "b"} {
		group, offering := "group-"+id, "offering-"+id
		book, err := quotepricing.Freeze(quotepricing.Book{GroupID: group, GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", Revision: 1, Quote: market.Quote{ID: "quote-" + id, GroupModelID: offering, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}})
		if err != nil {
			t.Fatal(err)
		}
		secret := "shared-synthetic-upstream"
		if id == "b" {
			secret = "different-synthetic-upstream"
		}
		sealed, err := keyring.Encrypt("channel-"+id, 1, secret)
		if err != nil {
			t.Fatal(err)
		}
		proof := map[string]bool{}
		for _, suite := range m.Suites {
			for _, req := range suite.Requirements {
				for _, label := range req.Assertions {
					proof["upstream_protocol/"+req.ID+"/"+label] = true
				}
			}
		}
		candidate := gateway.Candidate{GroupID: group, GroupModelID: offering, ChannelID: "channel-" + id, CredentialRevision: 1, CredentialScope: "synthetic-scope-" + id, OwnerID: "provider", BaseURL: "https://synthetic.example", UpstreamModel: "native/model", Credential: sealed, Quotes: book, MultiplierNano: ledger.UnitRate, Evidence: proof}
		store.rows = append(store.rows, gateway.CandidateRecord{ModelID: "platform/model:2026", Formats: []channel.Format{channel.FormatOpenAIResponses, channel.FormatOpenAIImages}, Candidate: candidate})
	}
	namespace, err := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	records := &proxyRecords{}
	engine := gateway.NewEngine(gateway.Dependencies{ContextNamespace: namespace, Store: store, Catalog: codexFixtureModels{}, Settings: settings.NewService(newFakeStore()), Routing: newFakeRoutingStore(), Keyring: keyring, Outbound: newTestOutbound(t, upstream), Resources: records})
	return NewHandler(Dependencies{Gateway: engine}), store, records
}

func codexRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Session-Id", "client-session")
	r.Header.Set("X-Openai-Encrypted-Tool-Arguments", "true")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestCodexAllContextAndIdentityRoutesUseRealHandlers(t *testing.T) {
	spec := loadOpenAPI(t)
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid native context body")
		}
		if _, exists := body["model"]; exists {
			t.Error("model-less operation invented a model")
		}
		var ctx struct {
			Session string `json:"session_id"`
			Agent   string `json:"current_agent_name"`
		}
		_ = json.Unmarshal(body["context"], &ctx)
		if ctx.Session == "client-session" || ctx.Session != r.Header.Get("Session-Id") || ctx.Agent != "/root" || r.Header.Get("Authorization") != "Bearer shared-synthetic-upstream" {
			t.Error("context or upstream credential mapping failed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("ChatGPT-Account-ID", "foreign-upstream-account")
		_, _ = io.WriteString(w, `["opaque-native-value",{"encrypted_output":"retain-exactly"}]`)
	}))
	defer upstream.Close()
	handler, store, records := newCodexContextFixture(t, upstream)
	for _, ep := range m.Endpoints {
		if ep.Feature != "F8" || (ep.Scope != "context_acl_only" && !strings.HasPrefix(ep.ID, "identity.")) {
			continue
		}
		t.Run(ep.ID, func(t *testing.T) {
			before := seen
			body := `{"context":{"session_id":"client-session","current_agent_name":"/root"},"path":"synthetic-note","text":"synthetic-text","query":"synthetic","window_id":"opaque-window","item_id":"opaque-item"}`
			if ep.Method == "GET" {
				body = ""
			}
			w := codexRequest(handler, ep.Method, ep.Path, body)
			spec.assertResponse(t, ep.Method, ep.Path, w)
			if w.Code != 200 {
				t.Fatal("native entry failed", w.Code, w.Body.String())
			}
			if ep.Scope == "context_acl_only" {
				if seen != before+1 || w.Body.String() != `["opaque-native-value",{"encrypted_output":"retain-exactly"}]` || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ChatGPT-Account-ID") != "" {
					t.Fatal("native value changed or request not forwarded")
				}
			} else if seen != before || !strings.Contains(w.Body.String(), "aihub-account-account-own") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("identity was not local")
			}
		})
	}
	if store.count != 0 || len(store.finished) != 0 || records.writes != 0 || len(records.rows) != 0 {
		t.Fatal("context/identity operation stored content, resource ownership or an inference charge")
	}
	// These endpoints use the platform API Key. Browser cookies cannot invoke
	// them merely because native CLI requests are exempt from the Origin check.
	r := httptest.NewRequest("POST", "/backend-api/codex/alpha/notes/v2/thread_hint", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "session", Value: "synthetic-cookie"})
	w := httptest.NewRecorder()
	before := seen
	handler.ServeHTTP(w, r)
	if w.Code != 401 || seen != before {
		t.Fatal("browser cookie authenticated a native endpoint")
	}
}

func TestCodexNotesCrossGroupsWithoutSourceStickinessAndIsolateKeys(t *testing.T) {
	notes := map[string]string{}
	seen := 0
	var credentials []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		credentials = append(credentials, r.Header.Get("Authorization"))
		var body struct {
			Context struct {
				Session string `json:"session_id"`
			}
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/write_file") {
			notes[body.Context.Session] = body.Text
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": notes[body.Context.Session]})
	}))
	defer upstream.Close()
	handler, store, _ := newCodexContextFixture(t, upstream)
	write := "/backend-api/codex/alpha/notes/v2/write_file"
	read := "/backend-api/codex/alpha/notes/v2/read_file"
	body := `{"context":{"session_id":"client-session","current_agent_name":"/root"},"path":"note","text":"synthetic-private-marker"}`
	if w := codexRequest(handler, "POST", write, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A cannot serve reads; B belongs to another group and receives the same
	// protected context. The gateway must not recover a source-channel binding.
	delete(store.rows[0].Candidate.Evidence, "upstream_protocol/codex-special.RL18/read_file")
	w := codexRequest(handler, "POST", read, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-private-marker") || seen != 2 || credentials[0] == credentials[1] {
		t.Fatal("cross-group continuation became sticky", w.Code, w.Body.String(), seen)
	}
	store.key.KeyID = "another-platform-key"
	w = codexRequest(handler, "POST", read, body)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private-marker") || seen != 3 {
		t.Fatal("another platform Key reused the first namespace", w.Code, w.Body.String())
	}
	store.key.OwnerID = "another-account"
	w = codexRequest(handler, "POST", read, body)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private-marker") || seen != 4 {
		t.Fatal("another account reused protected context")
	}
	store.key.AllowedModels = []string{"forbidden-model"}
	w = codexRequest(handler, "POST", read, body)
	if w.Code != 503 || seen != 4 {
		t.Fatal("model-less operation bypassed Key model permissions")
	}
	store.key.AllowedModels = nil
	w = codexRequest(handler, "POST", read, `{"context":{"session_id":"client-session"}}`)
	if w.Code != 400 || seen != 4 {
		t.Fatal("missing agent context reached shared upstream")
	}
	store.key.Enabled = false
	w = codexRequest(handler, "POST", read, body)
	if w.Code != 403 || seen != 4 {
		t.Fatal("revoked Key reached upstream")
	}
}

func TestCodexModelDirectoryUsesCurrentKeyScopeAndMeasuredMetadata(t *testing.T) {
	spec := loadOpenAPI(t)
	seen := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Method != "GET" || r.URL.Path != "/backend-api/codex/models" || r.Header.Get("If-None-Match") != "" || r.Header.Get("Session-Id") == "client-session" {
			t.Error("catalog path, ownership or revalidation incorrect")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", "shared-account-etag")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = io.WriteString(w, `{"models":[{"slug":"native/model","display_name":"upstream display","input_modalities":["text","image"],"context_window":999999,"supports_experimental_context":true,"service_tiers":[{"id":"ultrafast","name":"Ultra","description":"declared only"}]},{"slug":"unoffered-model","display_name":"private"}],"accounts":["private-account"]}`)
	}))
	defer upstream.Close()
	handler, store, _ := newCodexContextFixture(t, upstream)
	store.key.AllowedModels = []string{"platform/model:2026"}
	store.key.Aliases = map[string]string{"shortcut": "platform/model:2026"}
	w := codexRequest(handler, "GET", "/backend-api/codex/models", "")
	spec.assertResponse(t, "GET", "/backend-api/codex/models", w)
	if w.Code != 200 || seen != 1 || !strings.Contains(w.Body.String(), "shortcut") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "unoffered") || strings.Contains(w.Body.String(), "999999") || strings.Contains(w.Body.String(), "ultrafast") || w.Header().Get("ETag") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog bypassed current scope or advertised unproven values", w.Code, w.Body.String())
	}
	store.key.AllowedModels = []string{"another-model"}
	w = codexRequest(handler, "GET", "/backend-api/codex/models", "")
	if w.Code != 503 || seen != 1 {
		t.Fatal("catalog bypassed Key model witness permissions", w.Code, w.Body.String())
	}
}

func TestCodexInferenceAndImagePathsPreserveNativeBodiesAndSettleProducts(t *testing.T) {
	spec := loadOpenAPI(t)
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range manifest.Endpoints {
		if ep.Feature != "F8" || ep.Replay != "inference" {
			continue
		}
		t.Run(ep.ID, func(t *testing.T) {
			seen := 0
			var actual []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
				actual, _ = io.ReadAll(r.Body)
				if r.URL.Path != ep.Path {
					t.Error("Codex path converted to another protocol", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(ep.ID, "codex.images") {
					_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"c3ludGhldGlj"}]}`)
				} else {
					_, _ = io.WriteString(w, `{"object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
				}
			}))
			defer upstream.Close()
			handler, store, _ := newCodexContextFixture(t, upstream)
			if strings.HasPrefix(ep.ID, "codex.images") {
				for i := range store.rows {
					c := &store.rows[i].Candidate
					book, err := quotepricing.Freeze(quotepricing.Book{GroupID: c.GroupID, GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", Revision: 1, Quote: market.Quote{ID: "image-quote", GroupModelID: c.GroupModelID, Version: 1, Currency: "CNY", BillingUnit: "per_successful_image", UnitPrices: []market.UnitPrice{{Unit: "successful_image", Price: 1_000_000_000}}}}}})
					if err != nil {
						t.Fatal(err)
					}
					c.Quotes = book
				}
			}
			body := `{"model":"platform/model:2026", "input":"synthetic", "prompt":"synthetic", "n":9, "store":false, "unknown":{"opaque":"retain"}}`
			w := codexRequest(handler, "POST", ep.Path, body)
			spec.assertResponse(t, "POST", ep.Path, w)
			if w.Code != 200 || seen != 1 || string(actual) != strings.Replace(body, `"platform/model:2026"`, `"native/model"`, 1) || len(store.finished) != 1 {
				t.Fatal("native body/entry/settlement failed", w.Code, w.Body.String(), string(actual))
			}
			if strings.HasPrefix(ep.ID, "codex.images") && store.finished[0].Cost.Nano() != 1_000_000_000 {
				t.Fatal("image billed requested n instead of one real product", store.finished[0].Cost)
			}
			if strings.HasPrefix(ep.ID, "codex.images") {
				store.key.AllowedModels = []string{"another-text-model"}
				denied := codexRequest(handler, "POST", ep.Path, body)
				if denied.Code != 403 || seen != 1 {
					t.Fatal("independent image request borrowed another model permission", denied.Code, seen)
				}
			}
		})
	}
}
