package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"github.com/klauspost/compress/zstd"
)

func TestCodexRequirements(t *testing.T) {
	for _, tc := range []struct {
		path, body, op string
		caps           []string
	}{
		{"/v1/responses", `{"model":"m","input":"hello"}`, "inference", nil},
		{"/backend-api/codex/responses", `{"model":"m","tools":[{"type":"web_search"}],"service_tier":"priority"}`, "inference", []string{"codex_backend", "priority", "web_search"}},
		{"/backend-api/codex/responses", `{"model":"m","input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},{"type":"compaction_trigger"}]}`, "compact_v2", []string{"codex_backend", "responses_lite", "web_search", "compact_v2"}},
		{"/backend-api/codex/responses/compact", `{"model":"m"}`, "compact_v1", []string{"codex_backend", "compact_v1"}},
		{"/backend-api/codex/alpha/search", `{"model":"m"}`, "codex_search", []string{"codex_backend", "codex_search"}},
		{"/v1/responses", `{"model":"m","input":[{"type":"reasoning","encrypted_content":"portable"},{"type":"compaction","id":"cmp_any","encrypted_content":"portable"}]}`, "inference", nil},
	} {
		facts, scan, err := ScanRequest([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		op, caps := identifyCapabilities(tc.path, channel.FormatOpenAIResponses, []byte(tc.body), scan, facts.ServiceTier)
		if op != tc.op || !slices.Equal(caps, tc.caps) {
			t.Fatalf("%s: %s %v", tc.path, op, caps)
		}
	}
}
func TestCodexZstdTransparentForwarding(t *testing.T) {
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Content-Encoding") != "" || r.Header.Get("Chatgpt-Account-Id") != "" {
			t.Error("local identity or compressed encoding leaked")
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "upstream-model" {
			t.Error(payload)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_native","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	s.candidate.Advanced.Capabilities = []string{"codex_backend"}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	raw := []byte(`{"model":"gpt-test","store":false,"input":[{"type":"compaction","id":"cmp_foreign","encrypted_content":"self-contained"}]}`)
	compressed := encoder.EncodeAll(raw, nil)
	req := resourceRequest("POST", "/backend-api/codex/responses", "")
	req.Body = io.NopCloser(bytes.NewReader(compressed))
	req.ContentLength = int64(len(compressed))
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("Chatgpt-Account-Id", "fixed-local-id")
	out := httptest.NewRecorder()
	e.Serve(out, req, channel.FormatOpenAIResponses, "", false)
	if out.Code != 200 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
	if len(s.rows) != 0 {
		t.Fatal("store:false portable content created resource binding")
	}
}
func TestCodexDecompressionBudget(t *testing.T) {
	encoder, _ := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	defer encoder.Close()
	bomb := encoder.EncodeAll(bytes.Repeat([]byte("a"), maxRequestBytes+1), nil)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(bomb))
	req.Header.Set("Content-Encoding", "zstd")
	if _, err := readRequestBody(httptest.NewRecorder(), req); err == nil {
		t.Fatal("decoded size limit ignored")
	}
	req = httptest.NewRequest("POST", "/v1/responses", strings.NewReader("bad-frame"))
	req.Header.Set("Content-Encoding", "zstd")
	if _, err := readRequestBody(httptest.NewRecorder(), req); err == nil {
		t.Fatal("corrupt frame accepted")
	}
}
func TestCodexFixedDocumentsAuthenticateAndStayExact(t *testing.T) {
	e, s := newResourceFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("fixed documents reached upstream") })
	raw := "{\n \"models\": [], \"unknown_extension\": true\n}"
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{Codex: settings.Codex{Models: raw}}})
	for _, path := range []string{"/backend-api/codex/models", "/v1/user-auth-credential/whoami", "/backend-api/wham/accounts/check"} {
		out := httptest.NewRecorder()
		e.ServeCodexConfig(out, resourceRequest("GET", path, ""))
		if strings.HasSuffix(path, "models") {
			if out.Code != 200 || out.Body.String() != raw {
				t.Fatal(out.Code, out.Body.String())
			}
		} else if out.Code != 503 {
			t.Fatal(out.Code)
		}
	}
	s.key.Enabled = false
	out := httptest.NewRecorder()
	e.ServeCodexConfig(out, resourceRequest("GET", "/backend-api/codex/models", ""))
	if out.Code != 401 {
		t.Fatal(out.Code)
	}
}

type capabilityFixture struct {
	*resourceFixture
	candidates []Candidate
}

func (s capabilityFixture) Candidates(context.Context, string, channel.Format) ([]Candidate, error) {
	return append([]Candidate(nil), s.candidates...), nil
}
func TestCapabilityFailoverStaysWithinSupportingChannels(t *testing.T) {
	seen := 0
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		seen++
		w.Header().Set("Content-Type", "application/json")
		if seen == 1 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"temporary"}}`)
			return
		}
		io.WriteString(w, `{"id":"resp_retry","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	first := s.candidate
	first.Capabilities = []string{"web_search"}
	// Distinct candidates use the same fixture identity and credential so this
	// test isolates selection, rather than credential encryption.
	second := first
	second.ChannelID = "second"
	var err error
	second.Credential, err = e.Keyring.Encrypt("second", 1, "original-secret")
	if err != nil {
		t.Fatal(err)
	}
	unsupported := s.candidate
	unsupported.Capabilities = nil
	unsupported.BaseURL = "http://unsupported.invalid"
	e.Store = capabilityFixture{s, []Candidate{unsupported, first, second}}
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 3}, outbound: fakeOutbound{transport: http.DefaultTransport}})
	out := httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","store":false,"tools":[{"type":"web_search"}],"input":"hello"}`), channel.FormatOpenAIResponses, "", false)
	if out.Code != 200 || seen != 2 {
		t.Fatalf("%d %s attempts=%d", out.Code, out.Body.String(), seen)
	}
}

func TestOrdinaryCapabilityScanDoesNotAllocate(t *testing.T) {
	body := []byte(`{"model":"m","input":"hello"}`)
	_, scan, err := ScanRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		op, caps := identifyCapabilities("/v1/responses", channel.FormatOpenAIResponses, body, scan, "")
		if op != "inference" || len(caps) != 0 {
			t.Fatal(op, caps)
		}
	})
	if allocations != 0 {
		t.Fatalf("ordinary request capability scan allocated %g times", allocations)
	}
}
