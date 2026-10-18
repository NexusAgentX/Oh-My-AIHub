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

func TestOfficialCodexBackgroundCompactionOverlapsReusesAndCancels(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	inferences, compacts := 0, 0
	code := ""
	reused := false
	foreground, compactDone := make(chan struct{}), make(chan struct{})
	fixtureCtx, cancelFixture := context.WithCancel(context.Background())
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("async client used a different endpoint", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		trigger := false
		for _, item := range list(body["input"]) {
			trigger = trigger || str(object(item)["type"]) == "compaction_trigger"
		}
		mu.Lock()
		ordinal := 0
		if trigger {
			compacts++
			ordinal = compacts
		} else {
			inferences++
			ordinal = inferences
		}
		mu.Unlock()
		t.Logf("observed synthetic async request compact=%t ordinal=%d", trigger, ordinal)
		w.Header().Set("Content-Type", "text/event-stream")
		if trigger {
			if ordinal == 1 {
				select {
				case <-foreground:
				case <-r.Context().Done():
					return
				case <-fixtureCtx.Done():
					return
				}
				item := nativeObject{"id": "cmp_async", "type": "compaction", "encrypted_content": "synthetic-async+cipher=="}
				response := nativeObject{"id": "resp_compact", "object": "response", "status": "completed", "output": []any{item}, "usage": nativeObject{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11}}
				_, _ = io.WriteString(w, typedEvent("response.created", nativeObject{"response": nativeObject{"id": "resp_compact", "object": "response", "status": "in_progress", "output": []any{}}})+typedEvent("response.output_item.done", nativeObject{"output_index": 0, "item": item})+typedEvent("response.completed", nativeObject{"response": response}))
				close(compactDone)
			} else {
				// The upstream completes normally; the private client harness
				// gates delivery for shutdown. Production drains and settles the
				// reliable result after the consumer disconnects.
				item := nativeObject{"id": "cmp_cancel", "type": "compaction", "encrypted_content": "synthetic-cancel+cipher=="}
				response := nativeObject{"id": "resp_cancel", "object": "response", "status": "completed", "output": []any{item}, "usage": nativeObject{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11}}
				_, _ = io.WriteString(w, typedEvent("response.created", nativeObject{"response": nativeObject{"id": "resp_cancel", "object": "response", "status": "in_progress", "output": []any{}}})+typedEvent("response.output_item.done", nativeObject{"output_index": 0, "item": item})+typedEvent("response.completed", nativeObject{"response": response}))
			}
			return
		}
		answer := "OK"
		if ordinal == 1 {
			mu.Lock()
			for _, raw := range list(body["input"]) {
				for _, part := range list(object(raw)["content"]) {
					text := str(object(part)["text"])
					if start := strings.Index(text, "AIHUB-ASYNC-"); start >= 0 {
						code = strings.Split(text[start:], ".")[0]
					}
				}
			}
			mu.Unlock()
		}
		if ordinal == 2 {
			close(foreground)
			select {
			case <-compactDone:
			case <-r.Context().Done():
				return
			case <-fixtureCtx.Done():
				return
			}
		}
		if ordinal == 3 {
			encoded := string(raw)
			mu.Lock()
			reused = strings.Contains(encoded, "synthetic-async+cipher==") && strings.Contains(encoded, "Also remember the synthetic suffix AIHUB-ASYNC-SUFFIX")
			if reused {
				answer = code + " AIHUB-ASYNC-SUFFIX"
			}
			mu.Unlock()
		}
		text, _ := json.Marshal(answer)
		stream := strings.ReplaceAll(responseStreamFixture(), "你好 🌍", string(text[1:len(text)-1]))
		if ordinal == 1 || ordinal == 4 {
			stream = strings.ReplaceAll(stream, `"input_tokens":10`, `"input_tokens":1000`)
			stream = strings.ReplaceAll(stream, `"total_tokens":14`, `"total_tokens":1004`)
		}
		_, _ = io.WriteString(w, stream)
	}))
	defer up.Close()
	defer cancelFixture()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL22", "official_cli"), nil)
	gateway := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL22", "gateway_proxy"), nil)
	mu.Lock()
	defer mu.Unlock()
	if result.State != "pass" || gateway.State != "pass" || gateway.Usage.HTTP != 0 || inferences != 4 || compacts != 2 || !reused || result.Usage.Generate != 6 || result.Usage.InputTokens != 2036 || result.Usage.OutputTokens != 22 {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:async_compact"), &proof)
		t.Logf("semantic async metadata client=%+v wire=%+v", proof.Client, proof.Wire)
		t.Fatal("actual asynchronous V2 lifecycle incomplete", result, gateway, inferences, compacts, reused)
	}
}
