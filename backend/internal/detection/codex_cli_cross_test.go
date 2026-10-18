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

func TestOfficialCodexReplaysNativeHistoryAcrossStandardAndLiteBothWays(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	requests := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("cross-profile request escaped native inference", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		requests++
		raw, _ := io.ReadAll(r.Body)
		root, _ := decodeNative(raw)
		if str(root["model"]) != "synthetic-model" {
			t.Error("local shape alias created another upstream model")
		}
		closed, namespace := false, ""
		for _, raw := range list(root["input"]) {
			item := object(raw)
			if str(item["type"]) == "additional_tools" {
				for _, raw := range list(item["tools"]) {
					tool := object(raw)
					if str(tool["type"]) == "namespace" && syntheticToolDeclarations(list(tool["tools"])) > 0 {
						namespace = str(tool["name"])
					}
				}
			}
			if str(item["type"]) == "function_call_output" && strings.HasPrefix(str(item["call_id"]), "cross-call-") {
				closed = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if closed {
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
		} else {
			id := "cross-call-" + str(object(root["client_metadata"])["session_id"])
			_, _ = io.WriteString(w, codexCallStreamFixture(nativeCall{ID: id, Name: "synthetic_echo", Namespace: namespace}, nativeObject{"label": "violet", "meta": nativeObject{"ok": true}, "items": []any{nativeObject{"value": 7}}}))
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL12", "official_cli"), nil)
	mu.Lock()
	defer mu.Unlock()
	if result.State != "pass" || requests != 10 || result.Usage.Generate != 10 {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:cross_tools"), &proof)
		t.Logf("semantic cross-shape metadata client=%+v wire=%+v", proof.Client, proof.Wire)
		t.Fatal("actual bidirectional standard/Lite replay failed", result, requests)
	}
}
