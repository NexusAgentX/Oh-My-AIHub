package api

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func platformObject(v any) map[string]any { value, _ := v.(map[string]any); return value }
func platformArray(v any) []any           { value, _ := v.([]any); return value }
func platformString(v any) string         { value, _ := v.(string); return value }

func platformReturnedContext(body map[string]any) any {
	input := platformArray(body["input"])
	for i := len(input) - 1; i >= 0; i-- {
		item := platformObject(input[i])
		if item["type"] != "function_call_output" {
			continue
		}
		for _, part := range platformArray(item["output"]) {
			value := platformObject(part)
			if value["type"] == "encrypted_content" {
				plain, _ := base64.StdEncoding.DecodeString(platformString(value["encrypted_content"]))
				var decoded any
				_ = json.Unmarshal(plain, &decoded)
				return decoded
			}
		}
	}
	return nil
}

type platformContextBackend struct {
	mu                     sync.Mutex
	profile                string
	notes                  map[string]string
	steps                  map[string]int
	owner, window, pending string
	operations             map[string]int
	credentials            map[string]string
}

func (f *platformContextBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	f.operations[r.URL.Path]++
	f.credentials[r.URL.Path] = r.Header.Get("Authorization")
	if r.URL.Path == "/backend-api/codex/responses" {
		f.inference(w, body)
		return
	}
	if r.URL.Path == "/backend-api/codex/alpha/notes/v2/thread_hint" {
		_ = json.NewEncoder(w).Encode(map[string]string{"text": ""})
		return
	}
	session := platformString(platformObject(body["context"])["session_id"])
	op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var value any = map[string]any{"ok": true}
	if strings.Contains(r.URL.Path, "/notes/v2/") {
		switch op {
		case "write_file":
			f.notes[session] = strings.TrimPrefix(platformString(body["text"]), "sealed:")
		case "append_to_file":
			f.notes[session] += strings.TrimPrefix(platformString(body["text"]), "sealed:")
		case "read_file", "search_contents":
			value = map[string]any{"content": f.notes[session]}
		case "list_files_by_prefix":
			value = []any{map[string]string{"path": "aihub-probe-notes.txt"}}
		default:
			w.WriteHeader(404)
			return
		}
	} else if strings.Contains(r.URL.Path, "/history/v2/") {
		allowed := session == f.owner && platformString(body["window_id"]) == f.window
		value = []any{}
		switch op {
		case "list_windows":
			if session == f.owner {
				value = []any{map[string]any{"window_id": f.window, "item_count": 1}}
			}
		case "list_items":
			if allowed {
				value = []any{map[string]string{"window_id": f.window, "item_id": "owned-actual-item", "content": f.pending}}
			}
		case "read_item":
			if allowed && body["item_id"] == "owned-actual-item" {
				value = map[string]string{"content": f.pending}
			}
		case "search_contents":
			if allowed && body["query"] == "sealed:AIHUB-HISTORY-" {
				value = map[string]string{"content": f.pending}
			}
		default:
			w.WriteHeader(404)
			return
		}
	} else {
		w.WriteHeader(404)
		return
	}
	raw, _ := json.Marshal(value)
	_ = json.NewEncoder(w).Encode(map[string]string{"encrypted_output": base64.StdEncoding.EncodeToString(raw)})
}

func (f *platformContextBackend) inference(w http.ResponseWriter, body map[string]any) {
	session := platformString(platformObject(body["client_metadata"])["session_id"])
	prompt := ""
	input := platformArray(body["input"])
	for _, raw := range input {
		item := platformObject(raw)
		if item["role"] == "user" {
			for _, part := range platformArray(item["content"]) {
				if text := platformString(platformObject(part)["text"]); text != "" {
					prompt = text
				}
			}
		}
	}
	operation, namespace, args := "", "notes", map[string]any{}
	answer := ""
	if f.profile == "notes_lifecycle" {
		phase := f.steps[session]
		f.steps[session]++
		if strings.Contains(prompt, "write exactly") && phase < 6 {
			matches := regexp.MustCompile(`(?:write|append) exactly ("(?:\\.|[^"\\])*")`).FindAllStringSubmatch(prompt, -1)
			if len(matches) != 2 {
				w.WriteHeader(400)
				return
			}
			var note, update string
			_ = json.Unmarshal([]byte(matches[0][1]), &note)
			_ = json.Unmarshal([]byte(matches[1][1]), &update)
			operation = []string{"write_file", "read_file", "append_to_file", "read_file", "list_files_by_prefix", "search_contents"}[phase]
			args["path"] = "aihub-probe-notes.txt"
			switch operation {
			case "write_file":
				args["text"] = "sealed:" + note
			case "append_to_file":
				args["text"] = "sealed:" + update
			case "list_files_by_prefix":
				args = map[string]any{"prefix": "aihub-probe-notes.txt"}
			case "search_contents":
				args = map[string]any{"path_prefix": "aihub-probe-notes.txt", "query": "sealed:AIHUB-NOTE-"}
			}
		} else {
			newRead := !strings.Contains(prompt, "write exactly")
			for i := len(input) - 1; i >= 0; i-- {
				item := platformObject(input[i])
				if item["role"] == "user" {
					break
				}
				if item["type"] == "function_call_output" {
					newRead = false
				}
			}
			if newRead {
				operation, args = "read_file", map[string]any{"path": "aihub-probe-notes.txt"}
			} else {
				content := platformString(platformObject(platformReturnedContext(body))["content"])
				note := regexp.MustCompile(`AIHUB-NOTE-[0-9a-f-]{36}`).FindString(content)
				update := regexp.MustCompile(`APPEND-[0-9a-f-]{36}`).FindString(content)
				answer = "AIHUB-SYNTHETIC-NOTE-MISSING"
				if note != "" && update != "" {
					answer = note + " " + update
				}
			}
		}
	} else {
		namespace = "history"
		if f.owner == "" {
			f.owner, f.pending = session, prompt
			var turn map[string]any
			_ = json.Unmarshal([]byte(platformString(platformObject(body["client_metadata"])["x-codex-turn-metadata"])), &turn)
			f.window = platformString(turn["context_window_id"])
			codexPlatformStream(w, []any{codexPlatformText("OK")})
			return
		}
		kind := "primary"
		if strings.Contains(prompt, "with only window ") {
			kind = "isolation"
		} else if strings.Contains(prompt, "read_item again") {
			kind = "reopen"
		}
		key := session + ":" + kind
		phase := f.steps[key]
		f.steps[key]++
		ops := []string{"list_windows", "list_items", "read_item", "search_contents"}
		if kind == "isolation" {
			ops = []string{"list_items"}
		} else if kind == "reopen" {
			ops = []string{"list_items", "read_item"}
		}
		if phase < len(ops) {
			operation, args = ops[phase], map[string]any{"limit": 8}
			if operation != "list_windows" {
				args["window_id"] = f.window
			}
			if operation == "list_items" {
				args["role"], args["max_chars_per_item"] = "user", 4096
			}
			if operation == "search_contents" {
				args["query"] = "sealed:AIHUB-HISTORY-"
			}
			if operation == "read_item" {
				rows := platformArray(platformReturnedContext(body))
				if len(rows) != 1 {
					w.WriteHeader(400)
					return
				}
				args = map[string]any{"window_id": f.window, "item_id": platformString(platformObject(rows[0])["item_id"]), "offset_chars": 0, "limit_chars": 4096}
			}
		} else {
			answer = regexp.MustCompile(`AIHUB-HISTORY-[0-9a-f-]{36}`).FindString(platformString(platformObject(platformReturnedContext(body))["content"]))
			if answer == "" {
				answer = "AIHUB-SYNTHETIC-HISTORY-MISSING"
			}
		}
	}
	if operation != "" {
		raw, _ := json.Marshal(args)
		codexPlatformStream(w, []any{map[string]any{"id": "call_fixture", "type": "function_call", "call_id": fmt.Sprintf("call-%d", f.operations["/backend-api/codex/responses"]), "namespace": namespace, "name": operation, "arguments": string(raw)}})
	} else {
		codexPlatformStream(w, []any{codexPlatformText(answer)})
	}
}

func TestOfficialCodexContextCrossesProductionChannelsAndRestores(t *testing.T) {
	if os.Getenv("AIHUB_TEST_CODEX_BINARY") == "" || os.Getenv("AIHUB_TEST_CODEX_PROBE") == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	for _, profile := range []string{"notes_lifecycle", "history_lifecycle"} {
		t.Run(profile, func(t *testing.T) {
			backend := &platformContextBackend{profile: profile, notes: map[string]string{}, steps: map[string]int{}, operations: map[string]int{}, credentials: map[string]string{}}
			up := httptest.NewServer(backend)
			defer up.Close()
			handler, store, records := newCodexContextFixture(t, up)
			store.key.Aliases = map[string]string{"gpt-6.1-sol": "platform/model:2026"}
			keyring, _ := channel.ParseKeyring("test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "test")
			rotated, err := keyring.Encrypt("channel-b", 1, "rotated-synthetic-upstream")
			if err != nil {
				t.Fatal(err)
			}
			var reads atomic.Int32
			var originalMu sync.Mutex
			originalSession := ""
			bridge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/probe/history-window" {
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer sk-aih-synthetic" {
						w.WriteHeader(401)
						return
					}
					backend.mu.Lock()
					defer backend.mu.Unlock()
					_ = json.NewEncoder(w).Encode(map[string]string{"window": backend.window})
					return
				}
				id := "channel-b"
				if r.URL.Path == "/backend-api/codex/responses" || strings.HasSuffix(r.URL.Path, "/write_file") || strings.HasSuffix(r.URL.Path, "/append_to_file") {
					id = "channel-a"
				}
				selected := codexTestSnapshot{ChannelID: id}
				if strings.HasSuffix(r.URL.Path, "/read_file") || strings.HasSuffix(r.URL.Path, "/read_item") {
					if reads.Add(1) > 1 {
						selected.Rotated = &rotated
					}
				}
				if r.URL.Path == "/backend-api/codex/responses" {
					raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var body map[string]any
					_ = json.Unmarshal(raw, &body)
					originalMu.Lock()
					if originalSession == "" {
						originalSession = platformString(platformObject(body["client_metadata"])["session_id"])
					}
					originalMu.Unlock()
				}
				handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), codexTestSnapshotKey{}, selected)))
			})
			result := runProductionCodexProbe(t, bridge, profile)
			if !result.ContextVerified || !result.IsolationVerified || !result.ReopenVerified || records.writes != 0 {
				t.Fatal("CLI restored from local state or lost isolation", result)
			}
			backend.mu.Lock()
			read := "/backend-api/codex/alpha/notes/v2/read_file"
			if profile == "history_lifecycle" {
				read = "/backend-api/codex/alpha/history/v2/read_item"
			}
			if backend.operations[read] < 2 || backend.credentials[read] != "Bearer rotated-synthetic-upstream" || backend.credentials[read] == backend.credentials["/backend-api/codex/responses"] {
				backend.mu.Unlock()
				t.Fatal("actual restored reads did not cross credentials")
			}
			window := backend.window
			backend.mu.Unlock()
			originalMu.Lock()
			session := originalSession
			originalMu.Unlock()
			if len(session) != 36 {
				t.Fatal("actual CLI context was not observed")
			}
			// Adversarial HTTP probes use the actual fixed CLI's identifiers.
			// They are gateway security evidence, not claims that the CLI can
			// manufacture another user's session or a previous_response_id.
			for _, actor := range []string{"owner", "other_key", "other_account"} {
				key := store.key
				if actor == "other_key" {
					key.KeyID = "foreign-key"
				} else if actor == "other_account" {
					key.OwnerID = "foreign-account"
				}
				body, _ := json.Marshal(map[string]any{"context": map[string]string{"session_id": session, "current_agent_name": "/root"}, "path": "aihub-probe-notes.txt", "window_id": window, "item_id": "owned-actual-item", "limit_chars": 4096})
				response := codexRequest(codexSelectedHandler(handler, codexTestSnapshot{ChannelID: "channel-b", Key: &key, Rotated: &rotated}), "POST", read, string(body))
				var envelope map[string]string
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
					t.Fatal("actual CLI context security probe failed", actor, response.Code)
				}
				plain, _ := base64.StdEncoding.DecodeString(envelope["encrypted_output"])
				found := strings.Contains(string(plain), "AIHUB-NOTE-") || strings.Contains(string(plain), "AIHUB-HISTORY-")
				if found != (actor == "owner") {
					t.Fatal("actual CLI context escaped account/Key isolation", actor)
				}
			}
		})
	}
}

func TestOfficialCodexRevokedKeyCannotReachUpstream(t *testing.T) {
	if os.Getenv("AIHUB_TEST_CODEX_BINARY") == "" || os.Getenv("AIHUB_TEST_CODEX_PROBE") == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var reached atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached.Add(1); w.WriteHeader(500) }))
	defer up.Close()
	handler, store, _ := newCodexContextFixture(t, up)
	store.key.Aliases = map[string]string{"gpt-6.1-sol": "platform/model:2026"}
	key := store.key
	key.Enabled = false
	var requests atomic.Int32
	selected := codexSelectedHandler(handler, codexTestSnapshot{Key: &key})
	bridge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); selected.ServeHTTP(w, r) })
	result := runProductionCodexProbeOutcome(t, bridge, "notes", false)
	// A local PAT config can still make this CLI attempt a turn after identity
	// rejection. Every actual platform request must reject the revoked Key;
	// the client-side auth label is not authorization to contact the upstream.
	if result.Stage == "setup" || result.Stage == "initialize" || requests.Load() == 0 || reached.Load() != 0 {
		t.Fatal("revoked Key reached upstream or never exercised admission", result.Stage, reached.Load())
	}
}
