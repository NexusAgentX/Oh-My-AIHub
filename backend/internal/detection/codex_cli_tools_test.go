package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func codexNoopStreamFixture() string {
	final := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "custom-noop", Name: "apply_patch", Custom: codexprobe.NoopPatch}})
	item := object(list(final["output"])[0])
	added := cloneNative(item)
	added["input"] = ""
	return typedEvent("response.created", nativeObject{"sequence_number": 0, "response": nativeObject{"id": "resp_fixture", "object": "response", "status": "in_progress", "output": []any{}}}) +
		typedEvent("response.output_item.added", nativeObject{"sequence_number": 1, "output_index": 0, "item": added}) +
		typedEvent("response.custom_tool_call_input.delta", nativeObject{"sequence_number": 2, "output_index": 0, "item_id": item["id"], "delta": codexprobe.NoopPatch}) +
		typedEvent("response.custom_tool_call_input.done", nativeObject{"sequence_number": 3, "output_index": 0, "item_id": item["id"], "input": codexprobe.NoopPatch}) +
		typedEvent("response.output_item.done", nativeObject{"sequence_number": 4, "output_index": 0, "item": item}) +
		typedEvent("response.completed", nativeObject{"sequence_number": 5, "response": final})
}

func TestOfficialCodexNamespaceAndCustomToolsUseActualCallbacksAndNoop(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	requests, namespaceResults, customResults := 0, 0, 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("tool profile used another endpoint", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		requests++
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		custom, closed := false, false
		for _, raw := range list(body["tools"]) {
			tool := object(raw)
			custom = custom || str(tool["type"]) == "custom" && str(tool["name"]) == "apply_patch"
		}
		for _, raw := range list(body["input"]) {
			item := object(raw)
			if str(item["type"]) == "custom_tool_call_output" && str(item["call_id"]) == "custom-noop" {
				customResults++
				closed = true
			}
			if str(item["type"]) == "function_call_output" && str(item["call_id"]) == "synthetic-cli-call" {
				namespaceResults++
				closed = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if closed {
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
		} else if custom {
			_, _ = io.WriteString(w, codexNoopStreamFixture())
		} else {
			_, _ = io.WriteString(w, codexToolStreamFixture("synthetic"))
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL07", "official_cli"), nil)
	mu.Lock()
	defer mu.Unlock()
	if result.State != "pass" || requests != 4 || namespaceResults != 1 || customResults != 1 || result.Usage.Generate != 4 {
		for _, profile := range []string{"namespace_tools", "custom_tool"} {
			var proof codexCLIProof
			_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:"+profile), &proof)
			t.Logf("semantic %s client=%+v wire=%+v", profile, proof.Client, proof.Wire)
		}
		t.Fatal("actual namespace/custom lifecycle incomplete", result, requests, namespaceResults, customResults)
	}
}

func TestCustomCLIProbeCannotReceiveAFileChangingPatch(t *testing.T) {
	item := nativeObject{"type": "custom_tool_call", "name": "apply_patch", "call_id": "synthetic", "input": codexprobe.NoopPatch}
	if !safeCLINoopOutput(nativeObject{"output": []any{item}}) {
		t.Fatal("canonical empty patch rejected")
	}
	item["input"] = "*** Begin Patch\n*** Add File: private.txt\n+content\n*** End Patch\n"
	if safeCLINoopOutput(nativeObject{"output": []any{item}}) {
		t.Fatal("file-changing patch passed the isolated probe guard")
	}
}
