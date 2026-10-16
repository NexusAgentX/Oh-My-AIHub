package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type compatibilityTestOutbound struct {
	Outbound
	client *http.Client
}

func (p compatibilityTestOutbound) FreshClient(ctx context.Context, _ string) (*http.Client, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 20*time.Second {
		panic("unbounded probe")
	}
	return p.client, nil
}

func TestCompatibilityProbesValidateEvidenceAndRedactSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, scope, feature, contentType, body string
		status                                  int
		ok, skipped                             bool
	}{
		{"valid inference", "channel", "inference", "application/json", `{"id":"resp_probe","status":"completed","output":[]}`, 200, true, false},
		{"empty 200 is not inference", "channel", "inference", "application/json", `{}`, 200, false, false},
		{"text answer is not a tool call", "model", "function_tools", "application/json", `{"id":"resp_probe","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`, 200, false, false},
		{"downgrade is not fast", "model", "fast", "application/json", `{"id":"resp_probe","status":"completed","output":[],"service_tier":"default"}`, 200, false, false},
		{"priority is fast", "model", "fast", "application/json", `{"id":"resp_probe","status":"completed","output":[],"service_tier":"priority"}`, 200, true, false},
		{"downgrade is not ultrafast", "model", "ultrafast", "application/json", `{"id":"resp_probe","status":"completed","output":[],"service_tier":"priority"}`, 200, false, false},
		{"context must be confirmed", "model", "reasoning_all_turns", "application/json", `{"id":"resp_probe","status":"completed","output":[],"reasoning":{"context":"current_turn"}}`, 200, false, false},
		{"encrypted content must exist", "model", "encrypted_reasoning", "application/json", `{"id":"resp_probe","status":"completed","output":[{"type":"reasoning"}]}`, 200, false, false},
		{"stream error after 200", "channel", "stream", "text/event-stream", "data: {\"type\":\"response.failed\"}\n\n", 200, false, false},
		{"stream must terminate", "channel", "stream", "text/event-stream", "data: {\"type\":\"response.created\"}\n\n", 200, false, false},
		{"stream completed", "channel", "stream", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"status\":\"completed\",\"output\":[]}}\n\n", 200, true, false},
		{"secret in error", "channel", "inference", "application/json", `{"error":"synthetic-secret"}`, 403, false, false},
		{"manual confirmation", "model", "mcp", "application/json", `{}`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Header.Get("Authorization") != "Bearer synthetic-secret" {
					t.Error("wrong upstream identity")
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got := (&Service{}).probeCompatibilityCell(context.Background(), compatibilityTestOutbound{client: server.Client()}, Channel{BaseURL: server.URL}, compatibilityProbe{Model: Model{ModelID: "m", UpstreamModel: "up"}, Protocol: "responses", Feature: tc.feature, Scope: tc.scope}, "synthetic-secret")
			if got.OK != tc.ok || got.Skipped != tc.skipped {
				t.Fatalf("%+v", got)
			}
			if got.Error != nil && strings.Contains(*got.Error, "synthetic-secret") {
				t.Fatal("credential leaked")
			}
			if tc.skipped && hits != 0 {
				t.Fatal("manual-only feature invoked upstream")
			}
		})
	}
}

func TestCompatibilityCellsStayProtocolAndScopeSpecific(t *testing.T) {
	m := Compatibility{"responses": {"function_tools"}, "codex": {"responses_lite"}}
	if !ValidCapabilities(m.Cells(), ModelCapabilities) {
		t.Fatal(m)
	}
	if ValidCapabilities(m.Cells(), ChannelCapabilities) {
		t.Fatal("model cells accepted at channel scope")
	}
	if got := Matrix(m.Cells()); len(got["responses"]) != 1 || len(got["codex"]) != 1 {
		t.Fatal(got)
	}
	if got := DeclaredFormats([]string{"codex/search"}); len(got) != 0 {
		t.Fatal("search incorrectly advertises inference")
	}
}

func TestFunctionCompatibilityIncludesResultContinuation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("bad request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			io.WriteString(w, `{"id":"resp_tool","status":"completed","output":[{"type":"function_call","name":"compat_echo","call_id":"call_probe","arguments":"{\"value\":\"OK\"}"}]}`)
			return
		}
		raw := string(request["input"])
		if !strings.Contains(raw, "function_call_output") || !strings.Contains(raw, "call_probe") || !strings.Contains(raw, "compatibility-result-17") {
			t.Error("tool result not linked to the call")
		}
		io.WriteString(w, `{"id":"resp_continuation","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"compatibility-result-17"}]}]}`)
	}))
	result := (&Service{}).probeCompatibilityCell(context.Background(), compatibilityTestOutbound{client: server.Client()}, Channel{BaseURL: server.URL}, compatibilityProbe{Model: Model{ModelID: "m", UpstreamModel: "up"}, Protocol: "responses", Feature: "function_tools", Scope: "model"}, "synthetic-secret")
	server.Close()
	if !result.OK || calls != 2 {
		t.Fatalf("%+v calls=%d", result, calls)
	}
}

func TestCodexProbeUsesNativeStreamingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/backend-api/codex/responses" || string(body["stream"]) != "true" || string(body["store"]) != "false" || body["max_output_tokens"] != nil || body["instructions"] == nil || !strings.Contains(string(body["input"]), "input_text") {
			t.Errorf("not a native Codex request: %s %v", r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	defer server.Close()
	result := (&Service{}).probeCompatibilityCell(context.Background(), compatibilityTestOutbound{client: server.Client()}, Channel{BaseURL: server.URL}, compatibilityProbe{Model: Model{ModelID: "m", UpstreamModel: "up"}, Protocol: "codex", Feature: "inference", Scope: "channel"}, "synthetic-secret")
	if !result.OK {
		t.Fatal(result)
	}
}
