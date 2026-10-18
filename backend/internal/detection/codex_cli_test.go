package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func codexToolStreamFixture(namespace string) string {
	final := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "synthetic-cli-call", Name: "synthetic_echo", Namespace: namespace}})
	item := object(list(final["output"])[0])
	initial := nativeObject{"id": "resp_fixture", "object": "response", "status": "in_progress", "output": []any{}}
	added := cloneNative(item)
	added["arguments"], added["status"] = "", "in_progress"
	return typedEvent("response.created", nativeObject{"sequence_number": 0, "response": initial}) +
		typedEvent("response.output_item.added", nativeObject{"sequence_number": 1, "output_index": 0, "item": added}) +
		typedEvent("response.function_call_arguments.delta", nativeObject{"sequence_number": 2, "output_index": 0, "item_id": item["id"], "delta": item["arguments"]}) +
		typedEvent("response.function_call_arguments.done", nativeObject{"sequence_number": 3, "output_index": 0, "item_id": item["id"], "arguments": item["arguments"]}) +
		typedEvent("response.output_item.done", nativeObject{"sequence_number": 4, "output_index": 0, "item": item}) +
		typedEvent("response.completed", nativeObject{"sequence_number": 5, "response": final})
}

func TestOfficialCodexStandardAndLiteExecuteActualToolCallbacks(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	requests := 0
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		requests++
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		root, err := decodeNative(body)
		if err != nil {
			t.Error("invalid native request", err)
		}
		closed, namespace := false, ""
		for _, raw := range list(root["input"]) {
			item := object(raw)
			if str(item["type"]) == "additional_tools" {
				for _, rawTool := range list(item["tools"]) {
					tool := object(rawTool)
					if str(tool["type"]) == "namespace" && syntheticToolDeclarations(list(tool["tools"])) > 0 {
						namespace = str(tool["name"])
					}
				}
			}
			if str(item["type"]) == "function_call_output" && str(item["call_id"]) == "synthetic-cli-call" {
				data, _ := json.Marshal(item["output"])
				closed = strings.Contains(string(data), codexprobe.Marker)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if closed {
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
		} else {
			_, _ = io.WriteString(w, codexToolStreamFixture(namespace))
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"RL01", "RL02", "RL03"} {
		result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special."+id, "official_cli"), nil)
		if result.State != "pass" || result.Layer != "official_cli" {
			t.Fatalf("official %s tool callback failed: %+v", id, result)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 4 || s.usage.Generate != 4 || s.usage.InputTokens != 40 || s.usage.OutputTokens != 16 {
		t.Fatal("profile reuse duplicated actual calls or metering", requests, s.usage)
	}
}

func TestMissingCodexArtifactsKeepOfficialLayerPending(t *testing.T) {
	r, s, _ := codexTestSession(t, "https://fixture.example")
	if err := r.RegisterCodexCLI(context.Background(), filepath.Join(t.TempDir(), "missing-probe"), filepath.Join(t.TempDir(), "missing-cli")); err != nil {
		t.Fatal(err)
	}
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	item := codexLayerItem(t, r, s, "codex-special.RL13", "official_cli")
	if item.Implemented {
		t.Fatal("missing official artifact became executable")
	}
}

func TestOfficialCodexDetectorBridgeRecordsIndependentCLIProof(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	seen := map[string]int{}
	var seenMu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		defer seenMu.Unlock()
		seen[r.URL.Path]++
		if r.Header.Get("Authorization") != "Bearer synthetic-private-detector-key" {
			t.Error("private test Key boundary failed")
		}
		switch r.URL.Path {
		case "/backend-api/codex/models":
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model","display_name":"Synthetic model","description":"Isolated fixture","model_messages":{"instructions_template":"Use only the declared synthetic tools."},"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],"input_modalities":["text"],"supports_experimental_context":true,"context_window":32768}]}`)
		case "/backend-api/codex/alpha/notes/v2/thread_hint":
			_, _ = io.WriteString(w, `{"text":""}`)
		case "/backend-api/codex/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
		default:
			t.Error("CLI escaped fixed detector paths", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	s.Target.Model, s.Target.ModelType = "", ""
	if err := r.RegisterCodexGateway(); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"RL13", "RL15"} {
		item := codexLayerItem(t, r, s, "codex-special."+id, "official_cli")
		result := service.run(context.Background(), s, item, nil)
		if result.State != "pass" || result.Layer != "official_cli" || result.Source != "synthetic" {
			t.Fatalf("official CLI did not supply its own scoped proof: %+v", result)
		}
		if id == "RL13" && (result.Usage.Generate != 1 || result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 4) {
			t.Fatal("actual CLI inference was omitted or counted twice", result.Usage)
		}
		if id == "RL15" && (result.Usage.Generate != 0 || result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0) {
			t.Fatal("catalog-only profile invented inference usage", result.Usage)
		}
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen["/backend-api/codex/alpha/notes/v2/thread_hint"] == 0 || seen["/backend-api/codex/responses"] != 1 {
		t.Fatal("notes did not activate, or catalog profile added inference calls", seen)
	}
}
