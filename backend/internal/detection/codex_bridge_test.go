package detection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

func TestPrivateCodexBridgeSaturationIsPlatformFailureNotSupplierEvidence(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":"new synthetic context receipt"}`)
	}))
	defer up.Close()
	_, s, _ := codexTestSession(t, up.URL)
	b, err := newCodexBridge(s, "synthetic-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	b.requests = 256
	req := httptest.NewRequest("POST", up.URL+"/backend-api/codex/responses", nil)
	if _, err := (codexProbeOutbound{bridge: b}).RoundTrip(req); !errors.Is(err, ErrBusy) || !b.limited || s.usage.HTTP != 0 {
		t.Fatal("request cap produced a supplier result", err)
	}
	b.requests, b.limited = 0, false
	for i := range 128 {
		b.contextOutputs[[32]byte{byte(i)}] = map[string]bool{"codex.notes.read_file": true}
	}
	_, err = b.exchange(context.Background(), Request{EndpointID: "codex.notes.read_file", Path: "/backend-api/codex/alpha/notes/v2/read_file", Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"path":"synthetic.txt","context":{"session_id":"owned","current_agent_name":"/root"}}`)})
	if err != nil || !b.limited || len(b.contextOutputs) != 128 || len(b.store.finished) != 0 {
		t.Fatal("receipt capacity was silently treated as supplier capability", err)
	}
}

func codexLayerItem(t *testing.T, r *Registry, s *Session, id, layer string) Planned {
	t.Helper()
	plan, err := r.Plan(s.Target)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Layer == layer && slices.Contains(item.Requirements, id) {
			return item
		}
	}
	t.Fatal("required proof layer missing", id, layer)
	return Planned{}
}

func TestPrivateCodexGatewayProofUsesRealHandlerAndRealWitnesses(t *testing.T) {
	calls := map[string]int{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.Header.Get("Authorization") != "Bearer synthetic-private-detector-key" {
			t.Error("platform/placeholder Key reached actual upstream")
		}
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		wire, _ := io.ReadAll(r.Body)
		plain, err := nativeencoding.Decode(r.Context(), r.Header.Get("Content-Encoding"), wire)
		body, decodeErr := decodeNative(plain)
		if err != nil || decodeErr != nil || str(body["model"]) != "synthetic-model" || strings.Contains(string(plain), codexProbeModel) {
			t.Error("actual gateway did not replace/re-encode the private model alias", err, decodeErr)
		}
		_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", nativeMarker, nil))
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexGateway(); err != nil {
		t.Fatal(err)
	}
	item := codexLayerItem(t, r, s, "codex-special.RL14", "gateway_proxy")
	result := service.run(context.Background(), s, item, nil)
	if result.State != "pass" || result.Layer != "gateway_proxy" || result.Source != "synthetic" || len(result.Assertions) != 5 || result.Usage.HTTP != 5 || result.Usage.Generate != 4 || calls["/backend-api/codex/responses"] != 2 || calls["/v1/responses"] != 2 {
		t.Fatalf("private gateway evidence/counters invalid: %+v calls=%v", result, calls)
	}
	encoded, _ := json.Marshal(result)
	for _, private := range []string{codexProbeCredential, "synthetic-private-detector-key", nativeMarker} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private bridge body/credential entered report")
		}
	}
	plan, err := r.Plan(s.Target)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Items {
		if p.Layer == "official_cli" && p.Implemented && !p.Aggregate {
			t.Fatal("gateway bridge falsely registered official CLI proof", p)
		}
	}
}

func TestPrivateCodexIdentityAndCatalogKeepIndependentLayerAssertions(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model","display_name":"private name","input_modalities":["text","image"],"context_window":999999,"use_responses_lite":true,"supports_experimental_context":true,"supports_search_tool":true,"service_tiers":[{"id":"ultrafast","name":"Ultra"}]},{"slug":"foreign-model","display_name":"foreign"}],"accounts":["upstream-owner"]}`)
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	s.Target.Model = ""
	if err := r.RegisterCodexGateway(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"RL13", "RL15"} {
		result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special."+id, "gateway_proxy"), nil)
		if result.State != "pass" || result.Layer != "gateway_proxy" {
			t.Fatalf("platform layer failed: %+v", result)
		}
		want := 2
		if len(result.Assertions) != want {
			t.Fatal("gateway included upstream/CLI assertions", result)
		}
	}
}

func TestPrivateCodexBridgePreventsHeaderOverrideAndRejectsUnknownAccess(t *testing.T) {
	seen := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		var body struct {
			Context struct {
				Session string `json:"session_id"`
			} `json:"context"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Context.Session) != 36 || body.Context.Session[14] != '8' || body.Context.Session != r.Header.Get("Session-Id") || r.Header.Get("Authorization") != "Bearer synthetic-private-detector-key" {
			t.Error("inherited headers undid namespace or actual Key isolation")
		}
		_, _ = io.WriteString(w, `["opaque",{"encrypted_output":"unchanged"}]`)
	}))
	defer up.Close()
	_, s, _ := codexTestSession(t, up.URL)
	s.resolved.Headers.Set = []market.HeaderSet{{Name: "Session-Id", Value: "forbidden-override"}, {Name: "Authorization", Value: "Bearer forbidden-override"}}
	b, err := newCodexBridge(s, "synthetic-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	response, err := b.exchange(context.Background(), Request{EndpointID: "codex.notes.thread_hint", Path: "/backend-api/codex/alpha/notes/v2/thread_hint", Headers: http.Header{"Content-Type": {"application/json"}, "Session-Id": {"client-original"}}, Body: []byte(`{"context":{"session_id":"client-original","current_agent_name":"/root"}}`)})
	if err != nil || response.Status != 200 || string(response.Body) != `["opaque",{"encrypted_output":"unchanged"}]` || seen != 1 || b.requests != 1 {
		t.Fatal("private real-handler operation failed", err, response.Status)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/user-auth-credential/whoami", nil)
	r.Header.Set("Authorization", "Bearer forged-platform-key")
	b.ServeHTTP(w, r)
	if w.Code != 401 || seen != 1 {
		t.Fatal("forged private platform Key accepted")
	}
	if _, err := b.exchange(context.Background(), Request{EndpointID: "codex.notes.thread_hint", Path: "/v1/files"}); err == nil {
		t.Fatal("private bridge opened arbitrary resource endpoint")
	}
	if len(b.store.finished) != 0 {
		t.Fatal("model-less context operation entered private inference settlement")
	}
}
