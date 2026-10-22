package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// The two JSON entrances are forwarded without server-side state: a store flag
// never reaches the upstream, and a request that continues an upstream-side
// conversation is refused with a code the client can act on.
func TestGatewayForwardsWithoutServerSideState(t *testing.T) {
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var seen [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"response","id":"resp-own","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	keyring, err := channel.ParseKeyring(testKeyring, "k1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keyring.Encrypt("channel-own", 1, "upstream-synthetic")
	if err != nil {
		t.Fatal(err)
	}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "group-own", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", UpstreamModel: "native/model:2026", Revision: 1, Quote: market.Quote{ID: "quote-own", GroupModelID: "offering-own", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}})
	if err != nil {
		t.Fatal(err)
	}
	proof := map[string]bool{}
	for _, suite := range manifest.Suites {
		for _, requirement := range suite.Requirements {
			for _, label := range requirement.Assertions {
				proof["upstream_protocol/"+requirement.ID+"/"+label] = true
			}
		}
	}
	candidate := gateway.Candidate{GroupID: "group-own", GroupModelID: "offering-own", ChannelID: "channel-own", CredentialRevision: 1, CredentialScope: "synthetic-actual-scope", ConfigRevision: 1, OwnerID: "provider", BaseURL: "https://synthetic.example", UpstreamModel: "native/model:2026", Credential: sealed, Quotes: book, MultiplierNano: ledger.UnitRate, Evidence: proof}
	store := &proxyStore{candidate: candidate, key: gateway.KeyAuth{OwnerID: "account-own", KeyID: "key-own", AccountActive: true, Enabled: true}}
	engine := gateway.NewEngine(gateway.Dependencies{Store: store, Catalog: proxyModels{}, Settings: settings.NewService(newFakeStore()), Routing: newFakeRoutingStore(), Keyring: keyring, Outbound: newTestOutbound(t, upstream)})
	handler := NewHandler(Dependencies{Gateway: engine})
	call := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer sk-aih-synthetic")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	for _, tc := range []struct{ name, path, body, want string }{
		{"responses store true", "/v1/responses", `{"model":"platform/model:2026","input":"x","store":true}`, `{"model":"native/model:2026","input":"x"}`},
		{"responses capitalised duplicate", "/v1/responses", `{"model":"platform/model:2026","Store":true,"store":"true","input":"x"}`, `{"model":"native/model:2026","input":"x"}`},
		{"chat store true", "/v1/chat/completions", `{"model":"platform/model:2026","messages":[{"role":"user","content":"x"}],"store":true}`, `{"model":"native/model:2026","messages":[{"role":"user","content":"x"}]}`},
		{"chat store false is forwarded", "/v1/chat/completions", `{"model":"platform/model:2026","messages":[{"role":"user","content":"x"}],"store":false}`, `{"model":"native/model:2026","messages":[{"role":"user","content":"x"}],"store":false}`},
		{"without store byte for byte", "/v1/responses", `{"model":"platform/model:2026","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}]}],"include":["reasoning.encrypted_content"]}`, `{"model":"native/model:2026","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}]}],"include":["reasoning.encrypted_content"]}`},
	} {
		before := len(seen)
		recorder := call(tc.path, tc.body)
		if recorder.Code != http.StatusOK || len(seen) != before+1 {
			t.Fatalf("%s: %d %s (upstream calls %d)", tc.name, recorder.Code, recorder.Body.String(), len(seen)-before)
		}
		if got := string(seen[len(seen)-1]); got != tc.want {
			t.Fatalf("%s: upstream body\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct{ name, body, field string }{
		{"previous response", `{"model":"platform/model:2026","input":"x","previous_response_id":"resp_own"}`, "previous_response_id"},
		{"conversation", `{"model":"platform/model:2026","input":"x","conversation":"conv_own"}`, "conversation"},
		{"conversation object", `{"model":"platform/model:2026","input":"x","conversation":{"id":"conv_own"}}`, "conversation"},
	} {
		before := len(seen)
		recorder := call("/v1/responses", tc.body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, recorder.Code, recorder.Body.String())
		}
		if len(seen) != before {
			t.Fatalf("%s: the request reached the upstream", tc.name)
		}
		var body struct {
			Error struct{ Message, Code string } `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "stateful_request_unsupported" || !strings.Contains(body.Error.Message, tc.field) {
			t.Fatalf("%s: code %q message %q", tc.name, body.Error.Code, body.Error.Message)
		}
	}

	// An explicit null is not a request for state and is forwarded untouched.
	before := len(seen)
	recorder := call("/v1/responses", `{"model":"platform/model:2026","input":"x","conversation":null}`)
	if recorder.Code != http.StatusOK || len(seen) != before+1 {
		t.Fatalf("null conversation: %d %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(seen[len(seen)-1], []byte(`"conversation":null`)) {
		t.Fatalf("null conversation was dropped: %s", seen[len(seen)-1])
	}
}
