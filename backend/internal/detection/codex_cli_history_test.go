package detection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func returnedHistoryValue(body nativeObject) any {
	input := list(body["input"])
	for i := len(input) - 1; i >= 0; i-- {
		item := object(input[i])
		if str(item["type"]) != "function_call_output" {
			continue
		}
		for _, raw := range list(item["output"]) {
			part := object(raw)
			if str(part["type"]) != "encrypted_content" {
				continue
			}
			plain, _ := base64.StdEncoding.DecodeString(str(part["encrypted_content"]))
			value, _ := decodeNativeValue(plain)
			return value
		}
	}
	return nil
}

func TestOfficialCodexHistoryActuallyReadsSearchesResumesAndIsolates(t *testing.T) {
	for _, mode := range []string{"isolated", "shared", "not_yet_observed", "foreign_denied"} {
		t.Run(mode, func(t *testing.T) { testOfficialCodexHistory(t, mode) })
	}
}

func testOfficialCodexHistory(t *testing.T, mode string) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	owner, window, pending := "", "", ""
	steps := map[string]int{}
	operations := map[string]int{}
	marker := regexp.MustCompile(`AIHUB-HISTORY-[0-9a-f-]{36}`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		if r.URL.Path == "/backend-api/codex/alpha/notes/v2/thread_hint" {
			_, _ = io.WriteString(w, `{"text":""}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		if r.URL.Path == "/backend-api/codex/responses" {
			metadata := object(body["client_metadata"])
			session := str(metadata["session_id"])
			prompt := ""
			for _, raw := range list(body["input"]) {
				item := object(raw)
				if str(item["role"]) != "user" {
					continue
				}
				for _, part := range list(item["content"]) {
					if text := str(object(part)["text"]); text != "" {
						prompt = text
					}
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if owner == "" {
				owner, window, pending = session, str(metadata["x-codex-window-id"]), prompt
				turn, _ := decodeNative([]byte(str(metadata["x-codex-turn-metadata"])))
				if window == "" || turn["history_ingest_requested"] != true || marker.FindString(pending) == "" {
					t.Error("actual CLI did not ingest the known synthetic history window")
				}
				if contextWindow := str(turn["context_window_id"]); contextWindow != "" {
					window = contextWindow
				} else {
					t.Error("actual CLI omitted the distinct context-window identifier")
				}
				_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", "OK"))
				return
			}
			kind := "primary"
			if strings.Contains(prompt, "with only window ") {
				kind = "isolation"
			} else if strings.Contains(prompt, "read_item again") {
				kind = "reopen"
			}
			key := session + ":" + kind
			step := steps[key]
			steps[key]++
			if step > 8 {
				t.Error("synthetic history model did not close")
				w.WriteHeader(400)
				return
			}
			ops := []string{"list_windows", "list_items", "read_item", "search_contents"}
			if kind == "reopen" {
				ops = []string{"list_items", "read_item"}
			}
			if mode == "not_yet_observed" {
				ops = []string{"list_windows", "list_items", "search_contents"}
				if kind == "reopen" {
					ops = []string{"list_items"}
				}
			}
			if kind == "isolation" {
				ops = []string{"list_items"}
				if mode == "shared" {
					ops = append(ops, "read_item")
				}
			}
			if step < len(ops) {
				op := ops[step]
				args := nativeObject{"limit": 8}
				if op != "list_windows" {
					args["window_id"] = window
				}
				if op == "list_items" {
					args["role"], args["max_chars_per_item"] = "user", 4096
				}
				if op == "search_contents" {
					args["query"] = "sealed:AIHUB-HISTORY-"
				}
				if op == "read_item" {
					rows := list(returnedHistoryValue(body))
					if len(rows) != 1 {
						t.Error("fixture attempted to guess an item ID without an actual listing")
						w.WriteHeader(400)
						return
					}
					args = nativeObject{"window_id": window, "item_id": str(object(rows[0])["item_id"]), "offset_chars": 0, "limit_chars": 4096}
				}
				_, _ = io.WriteString(w, codexCallStreamFixture(nativeCall{ID: kind + "-" + op, Name: op, Namespace: "history"}, args))
				return
			}
			answer := marker.FindString(str(object(returnedHistoryValue(body))["content"]))
			if answer == "" {
				answer = "AIHUB-SYNTHETIC-HISTORY-MISSING"
			}
			_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", answer))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/backend-api/codex/alpha/history/v2/") {
			t.Error("history CLI escaped fixed context interfaces", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		operations[op]++
		foreign := str(object(body["context"])["session_id"]) != owner
		if foreign && mode == "foreign_denied" {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"type":"not_found","message":"Synthetic context is not accessible"}}`)
			return
		}
		allowed := (!foreign || mode == "shared") && mode != "not_yet_observed"
		var value any = []any{}
		switch op {
		case "list_windows":
			value = []any{nativeObject{"window_id": window, "item_count": 1}}
		case "list_items":
			if allowed {
				value = []any{nativeObject{"window_id": window, "item_id": "owned-actual-item", "content": pending}}
			}
		case "read_item":
			if str(body["window_id"]) != window || str(body["item_id"]) != "owned-actual-item" {
				t.Error("history read did not use unchanged actual IDs")
			}
			if allowed {
				value = nativeObject{"content": pending}
			}
		case "search_contents":
			if body["query"] != "sealed:AIHUB-HISTORY-" || r.Header.Get("X-Openai-Encrypted-Tool-Arguments") != "true" {
				t.Error("actual history encrypted query was changed")
			}
			if allowed {
				value = nativeObject{"content": pending}
			}
		default:
			t.Error("unknown history operation", op)
		}
		if op != "list_windows" && str(body["window_id"]) != window {
			t.Error("CLI followed an unowned window")
		}
		_ = json.NewEncoder(w).Encode(nativeObject{"encrypted_output": base64.StdEncoding.EncodeToString(mustNativeJSON(value))})
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
	s.Target.Model, s.Target.ModelType = "", ""
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL19", "official_cli"), nil)
	correct := result.State == "pass"
	if mode == "shared" {
		correct = result.Assertions[5].State == "fail"
	}
	if mode == "not_yet_observed" {
		correct = result.Assertions[2].State == "inconclusive" && operations["read_item"] == 0
	}
	if !correct {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:history_lifecycle"), &proof)
		t.Logf("semantic history client=%+v phases=%v denied=%v", proof.Client, proof.ContextPhases, proof.ContextDenied)
		t.Fatal("actual history lifecycle was misclassified", mode, result)
	}
}

func TestHistoryCLIRejectsOtherReturnedWindowsAndUnboundedReads(t *testing.T) {
	root := nativeObject{"output": []any{nativeObject{"type": "function_call", "namespace": "history", "name": "read_item", "call_id": "synthetic", "arguments": `{"window_id":"owned:0","item_id":"actual-item","limit_chars":4096}`}}}
	if !safeCLIHistoryOutput(root, "owned:0", "context-window") {
		t.Fatal("owned wire window rejected")
	}
	for _, args := range []string{`{"window_id":"foreign:0","item_id":"actual-item","limit_chars":4096}`, `{"window_id":"owned:0","item_id":"actual-item","limit_chars":1000000}`, `{"window_id":"owned:0","item_id":"actual-item","agent_name":"/someone-else"}`} {
		item := object(list(root["output"])[0])
		item["arguments"] = args
		if safeCLIHistoryOutput(root, "owned:0", "context-window") {
			t.Fatal("unowned/unbounded history request passed private execution guard")
		}
	}
}
