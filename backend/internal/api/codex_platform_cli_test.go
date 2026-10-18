package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func codexPlatformStream(w http.ResponseWriter, items []any) {
	w.Header().Set("Content-Type", "text/event-stream")
	event := func(kind string, fields map[string]any) {
		fields["type"] = kind
		raw, _ := json.Marshal(fields)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
	}
	response := map[string]any{"id": "resp_synthetic", "object": "response", "created_at": 1, "model": "native/model", "status": "completed", "output": items, "usage": map[string]int{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11}}
	event("response.created", map[string]any{"response": map[string]any{"id": "resp_synthetic", "object": "response", "status": "in_progress", "output": []any{}}})
	for i, item := range items {
		event("response.output_item.done", map[string]any{"output_index": i, "item": item})
	}
	event("response.completed", map[string]any{"response": response})
}

func codexPlatformText(text string) map[string]any {
	return map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func TestOfficialCodexOpaqueStateCrossesProductionChannels(t *testing.T) {
	if os.Getenv("AIHUB_TEST_CODEX_BINARY") == "" || os.Getenv("AIHUB_TEST_CODEX_PROBE") == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	for _, profile := range []string{"encrypted", "compact"} {
		t.Run(profile, func(t *testing.T) {
			var mu sync.Mutex
			var credentials []string
			code, session := "", ""
			replayed, compacted := false, false
			marker := regexp.MustCompile(`AIHUB-(?:REASON|COMPACT)-[0-9a-f-]{36}`)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path != "/backend-api/codex/responses" {
					t.Error("fixed CLI escaped the native inference entry", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				credentials = append(credentials, r.Header.Get("Authorization"))
				raw, _ := io.ReadAll(r.Body)
				var body struct {
					Model  string                       `json:"model"`
					Input  []map[string]json.RawMessage `json:"input"`
					Client map[string]string            `json:"client_metadata"`
				}
				if json.Unmarshal(raw, &body) != nil || body.Model != "native/model" {
					t.Error("native inference body/model lost")
				}
				if session == "" {
					session = body.Client["session_id"]
				}
				if len(session) != 36 || body.Client["session_id"] != session || session[14] != '8' {
					t.Error("channel switch changed the platform context namespace")
				}
				trigger, retained := false, false
				for _, item := range body.Input {
					var kind, cipher string
					_ = json.Unmarshal(item["type"], &kind)
					_ = json.Unmarshal(item["encrypted_content"], &cipher)
					trigger = trigger || kind == "compaction_trigger"
					if kind == "reasoning" || kind == "compaction" {
						replayed = replayed || cipher == "synthetic-opaque+/=="
					}
					if found := marker.Find(item["content"]); len(found) > 0 {
						if code == "" {
							code = string(found)
						}
						retained = retained || string(found) == code
					}
				}
				if len(credentials) == 1 {
					items := []any{codexPlatformText("OK")}
					if profile == "encrypted" {
						items = []any{map[string]any{"id": "rs_unregistered", "type": "reasoning", "summary": []any{}, "encrypted_content": "synthetic-opaque+/=="}, codexPlatformText("1591")}
					}
					codexPlatformStream(w, items)
				} else if trigger {
					compacted = true
					codexPlatformStream(w, []any{map[string]any{"id": "cmp_unregistered", "type": "compaction", "encrypted_content": "synthetic-opaque+/=="}})
				} else if replayed && retained && code != "" {
					codexPlatformStream(w, []any{codexPlatformText(code)})
				} else {
					codexPlatformStream(w, []any{codexPlatformText("WRONG")})
				}
			}))
			defer up.Close()
			handler, store, records := newCodexContextFixture(t, up)
			store.key.Aliases = map[string]string{"gpt-6.1-sol": "platform/model:2026"}
			var requests atomic.Int32
			bridge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Successive actual CLI inference requests see alternating valid
				// channel snapshots. No source-channel map is installed.
				id := "channel-a"
				if r.URL.Path == "/backend-api/codex/responses" && requests.Add(1)%2 == 0 {
					id = "channel-b"
				}
				selected := codexTestSnapshot{ChannelID: id}
				handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), codexTestSnapshotKey{}, selected)))
			})
			result := runProductionCodexProbe(t, bridge, profile)
			mu.Lock()
			defer mu.Unlock()
			want := 2
			if profile == "compact" {
				want = 3
			}
			if !result.MarkerMatched || len(credentials) != want || credentials[0] == credentials[1] || !replayed || (profile == "compact" && !compacted) || records.writes != 0 {
				t.Fatal("official client borrowed a local answer or failed to cross channels", profile, result, len(credentials), replayed, compacted)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.finished) != want {
				t.Fatal("cross-channel inference was not settled exactly once per call")
			}
			for _, call := range store.finished {
				if strings.Contains(fmt.Sprintf("%+v", call), "synthetic-opaque") || strings.Contains(fmt.Sprintf("%+v", call), codexprobe.Marker) {
					t.Fatal("opaque or message content persisted in call metadata")
				}
			}
		})
	}
}
