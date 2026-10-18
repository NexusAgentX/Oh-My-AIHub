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
)

func TestOfficialCodexUsesNativeV2CompactionAndReplaysFullWindow(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	code := ""
	requests, compactions := 0, 0
	retainedInput := false
	// The pinned V2 collector installs exactly one compaction item and retains
	// eligible messages from the request. V1 instead returns a whole window;
	// its ordered siblings are covered by the separate raw replay tests.
	window := []any{nativeObject{"id": "cmp_synthetic", "type": "compaction", "encrypted_content": "synthetic-compact+cipher=="}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("fixed current CLI used an unexpected compact entry", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		requests++
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		trigger, replayed := false, false
		for _, raw := range list(body["input"]) {
			item := object(raw)
			if str(item["type"]) == "compaction_trigger" {
				trigger = true
			}
			if str(item["type"]) == "compaction" {
				replayed = str(item["encrypted_content"]) == "synthetic-compact+cipher=="
			}
			for _, rawPart := range list(item["content"]) {
				text := str(object(rawPart)["text"])
				if start := strings.Index(text, "AIHUB-COMPACT-"); start >= 0 {
					retainedInput = retainedInput || replayed && code != "" && strings.Contains(text, code)
					code = strings.Split(text[start:], ".")[0]
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if trigger {
			compactions++
			final := nativeObject{"id": "resp_compact", "object": "response", "status": "completed", "output": window, "usage": nativeObject{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11}}
			stream := typedEvent("response.created", nativeObject{"sequence_number": 0, "response": nativeObject{"id": "resp_compact", "object": "response", "status": "in_progress", "output": []any{}}})
			for i, raw := range window {
				stream += typedEvent("response.output_item.added", nativeObject{"sequence_number": 2*i + 1, "output_index": i, "item": raw})
				stream += typedEvent("response.output_item.done", nativeObject{"sequence_number": 2*i + 2, "output_index": i, "item": raw})
			}
			stream += typedEvent("response.completed", nativeObject{"sequence_number": 7, "response": final})
			_, _ = io.WriteString(w, stream)
			return
		}
		text := "OK"
		if replayed {
			for _, raw := range list(body["input"]) {
				for _, part := range list(object(raw)["content"]) {
					retainedInput = retainedInput || code != "" && strings.Contains(str(object(part)["text"]), code)
				}
			}
			text = code
		}
		encoded, _ := json.Marshal(text)
		_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", string(encoded[1:len(encoded)-1])))
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL21", "official_cli"), nil)
	if result.State != "pass" {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:compact"), &proof)
		t.Logf("semantic compact metadata client=%+v wire=%+v", proof.Client, proof.Wire)
		t.Fatalf("actual fixed CLI compaction failed: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 3 || compactions != 1 || !retainedInput || result.Usage.Generate != 3 || result.Usage.InputTokens != 28 || result.Usage.OutputTokens != 11 {
		t.Fatal("native compact profile duplicated or omitted a call/usage", requests, compactions, result.Usage)
	}
}
