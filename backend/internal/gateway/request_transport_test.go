package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestServeCompressedResponsesIsolatesContextAndKeepsOpaqueBytes(t *testing.T) {
	const input = `{"client_metadata":{"session_id":"client-session"}, "input":[{"type":"reasoning","id":"opaque-id","encrypted_content":"opaque-secret"}], "model" : "gpt-test", "store":false}`
	namespace, err := gatewaycontract.NewContextNamespace(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	wantSession, _ := namespace.Identifier(gatewaycontract.Principal{AccountID: "account-1", KeyID: "key-1", Active: true}, "client-session")
	seen := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		wire, _ := io.ReadAll(r.Body)
		body, decodeErr := nativeencoding.Decode(r.Context(), r.Header.Get("Content-Encoding"), wire)
		if decodeErr != nil || r.Header.Get("Content-Encoding") != "zstd" || r.ContentLength != int64(len(wire)) {
			t.Errorf("incoherent request encoding: %v length %d/%d", decodeErr, r.ContentLength, len(wire))
		}
		want := strings.Replace(strings.Replace(input, "client-session", wantSession, 1), `"gpt-test"`, `"upstream-model"`, 1)
		if string(body) != want {
			t.Error("model/context splice changed opaque/native bytes")
		}
		if r.Header.Get("Session-Id") != wantSession || r.Header.Get("Thread-Id") != "" || r.Header.Get("ChatGPT-Account-ID") != "" || r.Header.Get("Digest") != "" || r.Header.Get("Authorization") != "Bearer sk-synthetic-upstream" {
			t.Error("context, digest or identity leaked into provider request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fixture-response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	run := newCredentialRun(t, 200)
	store := &protocolErrorStore{candidate: Candidate{ChannelID: "upstream", OwnerID: "provider", BaseURL: upstream.URL, UpstreamModel: "upstream-model", MultiplierNano: ledger.UnitRate, Credential: run.seal("upstream", "sk-synthetic-upstream")}, fallbackCredential: run.seal("zz-fallback", "sk-synthetic-upstream")}
	store.candidate.Evidence = map[string]bool{"upstream_protocol/codex-special.RL14/public_zstd_request": true}
	store.candidate.Advanced.HeaderRules.Set = []channel.HeaderSet{{Name: "Session-Id", Value: "foreign-session"}, {Name: "Thread-Id", Value: "foreign-thread"}, {Name: "Content-Encoding", Value: "gzip"}, {Name: "Digest", Value: "stale"}}
	e := newServeFixture(t, 10, defaultRouting{}).engine
	e.Store = store
	e.Keyring = run.keyring
	e.ContextNamespace = namespace
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{AccountMaxInflightCalls: 10, DefaultMaxAttempts: 2}, outbound: fakeOutbound{transport: http.DefaultTransport}})
	wire, err := nativeencoding.Encode(context.Background(), "zstd", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(wire))
	r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
	r.Header.Set("Content-Encoding", "zstd")
	r.Header.Set("Session-Id", "client-session")
	r.Header.Set("ChatGPT-Account-ID", "aihub-account-local")
	r.Header.Set("Digest", "invalid-after-splice")
	w := httptest.NewRecorder()
	e.Serve(w, r, channel.FormatOpenAIResponses, "", false)
	if w.Code != 200 || seen != 1 || len(store.finished) != 1 {
		t.Fatalf("status=%d upstream=%d finishes=%d body=%s", w.Code, seen, len(store.finished), w.Body.String())
	}
	// Removing support evidence must stop the compressed request locally,
	// rather than trying an unknown supplier or inventing compatibility.
	store.candidate.Evidence = map[string]bool{"upstream_protocol/codex-special.RL14/native_zstd_request": true}
	r = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(wire))
	r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
	r.Header.Set("Content-Encoding", "zstd")
	w = httptest.NewRecorder()
	e.Serve(w, r, channel.FormatOpenAIResponses, "", false)
	if w.Code != 503 || seen != 1 || len(store.finished) != 2 || store.finished[1].Bill != nil {
		t.Fatalf("missing evidence called or billed an upstream: status=%d requests=%d", w.Code, seen)
	}
}

func TestOrdinaryInferenceDoesNotAllocateForContextDetection(t *testing.T) {
	body := []byte(`{"model":"fixture","input":"plain","client_metadata":null}`)
	_, scan, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	e := NewEngine(Dependencies{})
	allocs := testing.AllocsPerRun(100, func() {
		next, out, mapped, err := e.isolateCodexRequest(r, body, scan, channel.FormatOpenAIResponses, KeyAuth{})
		if err != nil || mapped || next != r || &out[0] != &body[0] {
			panic("ordinary request copied or required namespace")
		}
	})
	if allocs != 0 {
		t.Fatalf("ordinary context detection allocations=%g", allocs)
	}
}

func TestContextDetectionCannotBypassNamespaceWithMissingOriginator(t *testing.T) {
	e := NewEngine(Dependencies{})
	for _, body := range []string{`{"model":"fixture","context":{"session_id":"client"}}`, `{"model":"fixture","client_metadata":{"x-codex-turn-metadata":"{}"}}`} {
		_, scan, err := ScanRequest([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/responses", nil)
		if _, _, _, err = e.isolateCodexRequest(r, []byte(body), scan, channel.FormatOpenAIResponses, KeyAuth{}); !errors.Is(err, errContextNamespaceUnavailable) {
			t.Fatal("unconfigured namespace did not fail closed", err)
		}
	}
}

func TestZstdRequirementMatchesManifestCapabilityLabel(t *testing.T) {
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/responses", "/backend-api/codex/responses"} {
		found := false
		for _, suite := range m.Suites {
			for _, requirement := range suite.Requirements {
				for _, label := range requirement.Assertions {
					if requirement.ID+"/"+label == nativeZstdRequirement(path).ID {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatal("request gate does not match a projected manifest capability", path)
		}
	}
	if nativeZstdRequirement("/v1/messages") == nativeZstdRequirement("/v1/responses") {
		t.Fatal("unmeasured endpoint borrowed Responses transport evidence")
	}
}

func TestCodexPlainAndSpecialToolsRequireOnlyRequestedCapabilities(t *testing.T) {
	for _, input := range []struct{ body, want, absent string }{
		{`{"model":"m","input":"hello"}`, "RL14/uncompressed_request", "RL03/standard_tools_roundtrip"},
		{`{"model":"m","tools":[{"type":"custom","name":"echo"}]}`, "RL07/custom_tools_roundtrip", "RL03/standard_tools_roundtrip"},
		{`{"model":"m","tools":[{"type":"function","name":"echo"}]}`, "RL03/standard_tools_roundtrip", "RL07/custom_tools_roundtrip"},
	} {
		found := false
		for _, required := range codexInferenceRequirements("/backend-api/codex/responses", []byte(input.body), "") {
			found = found || strings.HasSuffix(required.ID, input.want)
			if strings.HasSuffix(required.ID, input.absent) || strings.Contains(required.ID, "RL12/") {
				t.Fatal("request requires an unrelated full tool replay", required)
			}
		}
		if !found {
			t.Fatal("requested native capability was omitted", input.want)
		}
	}
}

func TestSelfContainedCompactionRequiresCompatibleResumeProofOnOneCandidate(t *testing.T) {
	input := []byte(`{"model":"m","input":[{"type":"compaction","id":"unregistered-internal-id","encrypted_content":"opaque"}]}`)
	required := codexInferenceRequirements("/backend-api/codex/responses", input, "")
	base := "upstream_protocol/codex-special.RL14/uncompressed_request"
	for _, capability := range []string{"RL20/compaction_continuation", "RL21/compaction_continuation"} {
		candidate := Candidate{Evidence: map[string]bool{base: true, "upstream_protocol/codex-special." + capability: true}}
		if !supportsAll(candidate, required) {
			t.Fatal("native compatible resume was unnecessarily bound to the producing API", capability)
		}
	}
	if supportsAll(Candidate{Evidence: map[string]bool{base: true}}, required) {
		t.Fatal("unverified compacted state reached an arbitrary candidate")
	}
	if supportsAll(Candidate{Evidence: map[string]bool{"upstream_protocol/codex-special.RL21/compaction_continuation": true}}, required) {
		t.Fatal("alternative proof relaxed another hard requirement")
	}
	parts := strings.TrimSuffix(strings.Repeat(`{"type":"compaction","encrypted_content":"opaque"},`, 1024), ",")
	repeated := codexInferenceRequirements("/backend-api/codex/responses", []byte(`{"model":"m","input":[`+parts+`]}`), "")
	if len(repeated) != len(required) {
		t.Fatal("repeated state expanded routing requirements", len(repeated))
	}
}
