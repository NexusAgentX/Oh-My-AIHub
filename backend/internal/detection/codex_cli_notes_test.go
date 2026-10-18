package detection

import (
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
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestOfficialCodexNotesActuallyWriteReadSearchReopenAndIsolate(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "shared_incorrectly"}[shared], func(t *testing.T) { testOfficialCodexNotesLifecycle(t, shared) })
	}
}

func testOfficialCodexNotesLifecycle(t *testing.T, shared bool) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	var mu sync.Mutex
	phases, notes := map[string]int{}, map[string]string{}
	seen := map[string]int{}
	quoted := regexp.MustCompile(`(?:write|append) exactly ("(?:\\.|[^"\\])*")`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen[r.URL.Path]++
		if r.URL.Path == "/backend-api/codex/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		if r.URL.Path == "/backend-api/codex/responses" {
			session := str(object(body["client_metadata"])["session_id"])
			if session == "" {
				t.Error("official notes inference lost context identity")
			}
			prompt := ""
			for _, raw := range list(body["input"]) {
				item := object(raw)
				if str(item["role"]) != "user" {
					continue
				}
				if text := str(item["content"]); text != "" {
					prompt = text
				} else {
					for _, rawPart := range list(item["content"]) {
						if text := str(object(rawPart)["text"]); text != "" {
							prompt = text
						}
					}
				}
			}
			phase := phases[session]
			phases[session]++
			operation, args, final := "", nativeObject{}, ""
			if strings.Contains(prompt, "write exactly") && phase < 6 {
				matches := quoted.FindAllStringSubmatch(prompt, -1)
				if len(matches) != 2 {
					t.Error("fixed synthetic note prompt missing bounded values")
					w.WriteHeader(500)
					return
				}
				var note, appended string
				_ = json.Unmarshal([]byte(matches[0][1]), &note)
				_ = json.Unmarshal([]byte(matches[1][1]), &appended)
				operation = []string{"write_file", "read_file", "append_to_file", "read_file", "list_files_by_prefix", "search_contents"}[phase]
				args = nativeObject{"path": "aihub-probe-notes.txt"}
				if operation == "write_file" {
					args["text"] = "sealed:" + note
				}
				if operation == "append_to_file" {
					args["text"] = "sealed:" + appended
				}
				if operation == "list_files_by_prefix" {
					args = nativeObject{"prefix": "aihub-probe-notes.txt"}
				}
				if operation == "search_contents" {
					args = nativeObject{"path_prefix": "aihub-probe-notes.txt", "query": "sealed:AIHUB-NOTE-"}
				}
			} else if strings.Contains(prompt, "write exactly") {
				final = codexReturnedContextText(body)
			} else {
				// A new user turn must actually issue another read, including the
				// resumed original thread. Prior answers cannot satisfy that phase.
				lastOutput := false
				for i := len(list(body["input"])) - 1; i >= 0; i-- {
					item := object(list(body["input"])[i])
					if str(item["role"]) == "user" {
						break
					}
					if str(item["type"]) == "function_call_output" {
						lastOutput = true
					}
				}
				if !lastOutput {
					operation, args = "read_file", nativeObject{"path": "aihub-probe-notes.txt"}
				} else {
					final = codexReturnedContextText(body)
					if final == "" {
						final = codexMissingNote
					}
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if operation != "" {
				_, _ = io.WriteString(w, codexCallStreamFixture(nativeCall{ID: fmt.Sprintf("note-call-%s-%d", session, phase), Name: operation, Namespace: "notes"}, args))
			} else {
				encoded, _ := json.Marshal(final)
				_, _ = io.WriteString(w, strings.ReplaceAll(responseStreamFixture(), "你好 🌍", string(encoded[1:len(encoded)-1])))
			}
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/backend-api/codex/alpha/notes/v2/") {
			t.Error("official client escaped context endpoints", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		session := str(object(body["context"])["session_id"])
		if shared {
			session = "incorrect-shared-storage"
		}
		operation := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var value any = nativeObject{"ok": true}
		switch operation {
		case "thread_hint":
			_, _ = io.WriteString(w, `{"text":""}`)
			return
		case "write_file":
			notes[session] = strings.TrimPrefix(str(body["text"]), "sealed:")
		case "append_to_file":
			notes[session] += strings.TrimPrefix(str(body["text"]), "sealed:")
		case "read_file":
			value = nativeObject{"content": notes[session]}
			if notes[session] == "" {
				value = nativeObject{"missing": true}
			}
		case "list_files_by_prefix":
			value = []any{nativeObject{"path": "aihub-probe-notes.txt"}}
		case "search_contents":
			value = nativeObject{"content": notes[session]}
		default:
			t.Error("unknown note operation", operation)
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
	result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL18", "official_cli"), nil)
	if shared {
		if len(result.Assertions) != 7 || result.Assertions[6].State != "fail" {
			t.Fatal("official CLI failed to detect the actual foreign note result", result)
		}
		for _, assertion := range result.Assertions[:6] {
			if assertion.State != "pass" {
				t.Fatal("isolation failure erased independent actual tool/reopen support", result)
			}
		}
		return
	}
	if result.State != "pass" || len(result.Assertions) != 7 {
		var proof codexCLIProof
		_ = json.Unmarshal(s.Recall("codex-cli-profile-v1:notes_lifecycle"), &proof)
		t.Logf("semantic metadata client=%+v observed=%v continued=%v phases=%v", proof.Client, proof.Observed, proof.ContextContinued, proof.ContextPhases)
		t.Fatalf("official note lifecycle did not complete: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/backend-api/codex/alpha/notes/v2/read_file"] != 4 || len(notes) != 1 {
		t.Fatal("resume/isolation used local answers or another note", seen)
	}
}

func codexReturnedContextText(body nativeObject) string {
	input := list(body["input"])
	for i := len(input) - 1; i >= 0; i-- {
		item := object(input[i])
		if str(item["type"]) != "function_call_output" {
			continue
		}
		for _, raw := range list(item["output"]) {
			content := object(raw)
			if str(content["type"]) != "encrypted_content" {
				continue
			}
			plain, _ := base64.StdEncoding.DecodeString(str(content["encrypted_content"]))
			value, _ := decodeNative(plain)
			if value["missing"] == true {
				return codexMissingNote
			}
			return str(value["content"])
		}
	}
	return ""
}
