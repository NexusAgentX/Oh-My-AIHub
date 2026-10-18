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

func TestOfficialCodexEncryptedReasoningActuallyContinues(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	code, requests, continued := "", 0, false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Error("encrypted profile escaped native inference", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		requests++
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		for _, raw := range list(body["input"]) {
			item := object(raw)
			continued = continued || str(item["type"]) == "reasoning" && str(item["encrypted_content"]) == "synthetic-reason+cipher=="
			for _, part := range list(item["content"]) {
				text := str(object(part)["text"])
				if start := strings.Index(text, "AIHUB-REASON-"); start >= 0 {
					code = strings.Split(text[start:], ".")[0]
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			final := nativeFixtureWithCalls("openai-responses", "1591", nil)
			reason := nativeObject{"id": "rs_synthetic", "type": "reasoning", "summary": []any{}, "encrypted_content": "synthetic-reason+cipher=="}
			final["output"] = append([]any{reason}, list(final["output"])...)
			stream := typedEvent("response.created", nativeObject{"response": nativeObject{"id": "resp_fixture", "object": "response", "status": "in_progress", "output": []any{}}})
			for i, item := range list(final["output"]) {
				stream += typedEvent("response.output_item.done", nativeObject{"output_index": i, "item": item})
			}
			_, _ = io.WriteString(w, stream+typedEvent("response.completed", nativeObject{"response": final}))
		} else {
			encoded, _ := json.Marshal(code)
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", string(encoded[1:len(encoded)-1])))
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL09", "official_cli"), nil)
	if result.State != "pass" || requests != 2 || !continued || result.Usage.Generate != 2 {
		t.Fatal("official reasoning was not actually replayed", result, requests, continued)
	}
}

func TestOfficialCodexSearchUsesIndependentHTTPAndActualOutput(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	inferences, searches := 0, 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/backend-api/codex/models":
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
		case "/backend-api/codex/alpha/search":
			searches++
			_, _ = io.WriteString(w, `{"encrypted_output":"synthetic-search+cipher==","output":"Official API documentation","results":[{"type":"text_result","url":"https://platform.openai.com/docs","title":"API documentation"}]}`)
		case "/backend-api/codex/responses":
			inferences++
			w.Header().Set("Content-Type", "text/event-stream")
			if inferences == 1 {
				_, _ = io.WriteString(w, codexCallStreamFixture(nativeCall{ID: "search-actual", Namespace: "web", Name: "run"}, nativeObject{"search_query": []any{nativeObject{"q": "OpenAI API documentation"}}, "response_length": "short"}))
			} else {
				_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", codexprobe.Marker))
			}
		default:
			t.Error("search fetched a result URL or another endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL16", "official_cli"), nil)
	if result.State != "pass" || inferences != 2 || searches != 1 || result.Usage.Generate != 3 {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:search"), &proof)
		t.Logf("semantic search metadata client=%+v wire=%+v continued=%v", proof.Client, proof.Wire, proof.ContextContinued)
		t.Fatal("official search borrowed a response or never called its endpoint", result, inferences, searches)
	}
}
