package detection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestCodexContextArgumentsDeclaresNamespaceDescription(t *testing.T) {
	for _, namespace := range []string{"notes", "history"} {
		t.Run(namespace, func(t *testing.T) {
			requests := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				metadata := object(body["client_metadata"])
				if r.Header.Get("Session-Id") != "synthetic-session" || r.Header.Get("Thread-Id") != "synthetic-session" || r.Header.Get("Originator") != "aihub-codex-probe" || r.Header.Get("Version") != "0.163.0" || r.Header.Get("X-Codex-Beta-Features") != "remote_compaction_v2" {
					t.Error("context initialization omitted the pinned client headers")
				}
				if requests == 1 {
					turn, err := decodeNative([]byte(str(metadata["x-codex-turn-metadata"])))
					if err != nil || turn["history_initialization"] != "new" || turn["history_ingest_requested"] != true || turn["request_kind"] != "turn" || turn["model"] != "synthetic-model" || turn["agent_name"] != "/root" || str(turn["installation_id"]) == "" || str(turn["turn_id"]) == "" || turn["turn_id"] != turn["root_turn_id"] || str(turn["context_window_id"]) == "" || str(turn["window_id"]) != "synthetic-session:0" || metadata["x-codex-window-id"] != turn["window_id"] || str(metadata["x-codex-turn-metadata"]) != r.Header.Get("X-Codex-Turn-Metadata") || r.Header.Get("X-Codex-Window-Id") != "synthetic-session:0" {
						t.Error("synthetic context used incomplete turn initialization")
					}
				} else if metadata["x-codex-turn-metadata"] != nil || r.Header.Get("X-Codex-Turn-Metadata") != "" {
					t.Error("a later tool request overwrote the known seeded history")
				}
				tools := list(body["tools"])
				if len(tools) != 1 || str(object(tools[0])["description"]) == "" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"Missing required parameter: tools[0].description"}}`)
					return
				}
				parameters := object(object(list(object(tools[0])["tools"])[0])["parameters"])
				if object(list(object(tools[0])["tools"])[0])["strict"] != false {
					t.Error("reserved context tool must preserve the pinned non-strict mode")
				}
				properties := object(parameters["properties"])
				for _, raw := range object(parameters["properties"]) {
					if _, leaked := object(raw)["minimum"]; leaked {
						t.Error("raw-source minimum leaked into the pinned CLI wire schema")
					}
				}
				filter, count := "path_prefix", 5
				if namespace == "history" {
					filter, count = "window_id", 8
				}
				if len(properties) != count || len(list(object(properties[filter])["anyOf"])) != 2 || len(list(parameters["required"])) != 1 || list(parameters["required"])[0] != "query" {
					t.Error("reserved search tool schema lost optional nullable fields")
				}
				out := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "synthetic-context-call", Name: "search_contents", Namespace: namespace}})
				args := nativeObject{"query": "sealed:synthetic-query", "max_files": 2, "recent_file_first": true}
				if namespace == "history" {
					args = nativeObject{"query": "sealed:synthetic-query", "limit": 2, "recent_first": true, "agent_name": nil}
				}
				object(list(out["output"])[0])["arguments"] = string(mustNativeJSON(args))
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer up.Close()
			_, s, _ := codexTestSession(t, up.URL)
			s.allowed = map[string]bool{"codex.responses": true}
			n := nativeScene{s: s, protocol: "openai-responses"}
			for range 2 {
				args, out := n.codexContextArgumentsFor(context.Background(), "synthetic-model", "synthetic-session", namespace, "search_contents", nativeObject{"query": "synthetic-query"})
				if out.State != "pass" || str(args["query"]) != "sealed:synthetic-query" {
					t.Fatal("malformed namespace declaration prevented the real context probe", out.State, out.Reason)
				}
			}
		})
	}
}

func TestCodexEmptyThreadHintsCannotProveContextIsolation(t *testing.T) {
	for _, mode := range []string{"isolated", "empty", "shared"} {
		t.Run(mode, func(t *testing.T) {
			owner, marker := "", ""
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/backend-api/codex/models" {
					_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				switch r.URL.Path {
				case "/backend-api/codex/responses":
					prompt := str(object(list(body["input"])[0])["content"])
					args, _ := decodeNative([]byte(prompt[strings.Index(prompt, "{"):]))
					out := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "hint-seed-call", Name: "write_file", Namespace: "notes"}})
					object(list(out["output"])[0])["arguments"] = string(mustNativeJSON(args))
					_ = json.NewEncoder(w).Encode(out)
				case "/backend-api/codex/alpha/notes/v2/write_file":
					owner, marker = str(object(body["context"])["session_id"]), str(body["text"])
					_, _ = io.WriteString(w, `{"ok":true}`)
				case "/backend-api/codex/alpha/notes/v2/thread_hint":
					text := ""
					if mode != "empty" && (str(object(body["context"])["session_id"]) == owner || mode == "shared") {
						text = marker
					}
					_ = json.NewEncoder(w).Encode(nativeObject{"text": text})
				default:
					t.Error("unexpected prerequisite endpoint", r.URL.Path)
				}
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
			s.Target.Model, s.Target.ModelType = "", ""
			result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special.RL17"), nil)
			if result.State == "invalid" || len(result.Assertions) != 2 || result.Assertions[0].State != "pass" {
				t.Fatal("thread hint requests/shape were not independently observed", result)
			}
			want := map[string]string{"isolated": "pass", "empty": "inconclusive", "shared": "fail"}[mode]
			if result.Assertions[1].State != want {
				t.Fatal("empty or shared hints became isolation proof", result)
			}
		})
	}
}

func TestContextJSONKeepsStrictDuplicateAndTrailingDataRejection(t *testing.T) {
	for _, raw := range []string{`[{"same":1,"same":2}]`, `[] true`, `["invalid` + string([]byte{0xff}) + `"]`} {
		if _, err := decodeNativeValue([]byte(raw)); err == nil {
			t.Fatal("context RPC JSON accepted invalid members/encoding/trailing data")
		}
	}
	if _, err := decodeNativeValue([]byte(`["opaque",{"future":true}]`)); err != nil {
		t.Fatal("valid native context array rejected", err)
	}
	if _, err := decodeNative([]byte(`[]`)); err == nil {
		t.Fatal("inference object requirement weakened by context support")
	}
}

func TestContextOutputKeepsActualEncryptedContentAndInlineAttachments(t *testing.T) {
	input := nativeObject{"encrypted_output": "opaque-context+==", "future": true, "images": []any{nativeObject{"mime_type": "image/png", "data": "synthetic-base64", "detail": "high"}}}
	output, valid := codexContextOutput(input)
	items := list(output)
	if !valid || len(items) != 2 || str(object(items[0])["encrypted_content"]) != "opaque-context+==" || str(object(items[1])["image_url"]) != "data:image/png;base64,synthetic-base64" || object(items[1])["detail"] != "high" || input["images"] == nil {
		t.Fatal("native context attachments/cipher were removed or the retained reply was mutated")
	}
	if _, valid := codexContextOutput(nativeObject{"images": "wrong-type"}); valid {
		t.Fatal("invalid attachments silently disappeared")
	}
	if _, valid := codexContextOutput(nativeObject{"images": nil}); valid {
		t.Fatal("explicit null attachments bypassed pinned client validation")
	}
}

func TestCodexNotesRequiresRealReadAppendReopenAndDifferentSessionIsolation(t *testing.T) {
	for _, brokenIsolation := range []bool{false, true} {
		for _, layer := range []string{"upstream_protocol", "gateway_proxy"} {
			t.Run(layer+map[bool]string{false: "/isolated", true: "/shared"}[brokenIsolation], func(t *testing.T) {
				notes := map[string]string{}
				initialized := map[string]bool{}
				seen := map[string]int{}
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen[r.URL.Path]++
					if r.URL.Path == "/backend-api/codex/models" {
						_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
						return
					}
					raw, _ := io.ReadAll(r.Body)
					body, _ := decodeNative(raw)
					if r.URL.Path == "/backend-api/codex/responses" {
						metadata := object(body["client_metadata"])
						if str(metadata["x-codex-turn-metadata"]) != "" {
							id := str(metadata["session_id"])
							if id != r.Header.Get("Session-Id") || body["prompt_cache_key"] != id {
								t.Error("context initialization and cache/session identifiers disagree")
							} else {
								initialized[id] = true
							}
						}
						if declarations := list(body["tools"]); len(declarations) > 0 {
							tool := object(list(object(declarations[0])["tools"])[0])
							operation := str(tool["name"])
							prompt := str(object(list(body["input"])[0])["content"])
							args, err := decodeNative([]byte(prompt[strings.Index(prompt, "{"):]))
							if err != nil {
								t.Error(err)
							}
							for _, field := range []string{"text", "query"} {
								if text := str(args[field]); text != "" {
									if object(object(object(tool["parameters"])["properties"])[field])["encrypted"] != true {
										t.Error("native encrypted-argument schema annotation removed")
									}
									args[field] = "sealed:" + text
								}
							}
							out := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "context-generated-call", Name: operation, Namespace: "notes"}})
							object(list(out["output"])[0])["arguments"] = string(mustNativeJSON(args))
							_ = json.NewEncoder(w).Encode(out)
							return
						}
						input := list(body["input"])
						if len(input) == 1 {
							_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", "OK", nil))
							return
						}
						output := object(input[len(input)-1])["output"]
						var value any
						if str(output) != "" {
							_ = json.Unmarshal([]byte(str(output)), &value)
						} else {
							encoded := str(object(list(output)[0])["encrypted_content"])
							plain, _ := base64.StdEncoding.DecodeString(encoded)
							_ = json.Unmarshal(plain, &value)
						}
						text := str(object(value)["content"])
						if object(value)["missing"] == true {
							text = codexMissingNote
						} else if rows := list(value); rows != nil {
							for _, row := range rows {
								text += str(object(row)["path"]) + "\n"
							}
						}
						_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", strings.TrimSpace(text), nil))
						return
					}
					if !strings.HasPrefix(r.URL.Path, "/backend-api/codex/alpha/notes/v2/") || r.Header.Get("X-Openai-Tool-Output-Truncation-Policy") != `{"mode":"bytes","limit":4000}` {
						t.Error("notes escaped its fixed context RPC or omitted truncation policy")
					}
					session := str(object(body["context"])["session_id"])
					if !initialized[session] {
						t.Error("notes accessed a context not initialized by its own actual model turn")
						_, _ = io.WriteString(w, `{"error":"no stored context"}`)
						return
					}
					if brokenIsolation {
						session = "shared-incorrectly"
					}
					key := session + ":" + str(body["path"])
					operation := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					var value any = nativeObject{"ok": true}
					switch operation {
					case "write_file", "append_to_file":
						text := str(body["text"])
						if !strings.HasPrefix(text, "sealed:") || r.Header.Get("X-Openai-Encrypted-Tool-Arguments") != "true" {
							t.Error("fabricated plaintext replaced actual encrypted model arguments")
						}
						if operation == "write_file" {
							notes[key] = ""
						}
						notes[key] += strings.TrimPrefix(text, "sealed:")
					case "read_file":
						value = nativeObject{"missing": true}
						if text, exists := notes[key]; exists {
							value = nativeObject{"content": text}
						}
					case "list_files_by_prefix":
						rows := []any{}
						for key := range notes {
							if strings.HasPrefix(key, session+":"+str(body["prefix"])) {
								rows = append(rows, nativeObject{"path": strings.TrimPrefix(key, session+":")})
							}
						}
						_ = json.NewEncoder(w).Encode(rows)
						return
					case "search_contents":
						value = nativeObject{"content": notes[session+":"+str(body["path_prefix"])]}
					default:
						t.Error("unknown note operation")
					}
					cipher := base64.StdEncoding.EncodeToString(mustNativeJSON(value))
					_ = json.NewEncoder(w).Encode(nativeObject{"encrypted_output": cipher})
				}))
				defer up.Close()
				r, s, service := codexTestSession(t, up.URL)
				s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
				s.Target.Model, s.Target.ModelType = "", ""
				if err := r.RegisterCodexGateway(); err != nil {
					t.Fatal(err)
				}
				result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL18", layer), nil)
				if brokenIsolation {
					if result.State == "pass" || len(result.Assertions) != 7 || result.Assertions[6].State == "pass" {
						t.Fatal("shared upstream note collision counted as isolation", result)
					}
					return
				}
				if result.State != "pass" || len(result.Assertions) != 7 || seen["/backend-api/codex/alpha/notes/v2/read_file"] != 4 || len(notes) != 1 {
					t.Fatal("note lifecycle did not perform all actual operations", result)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "sealed:") || strings.Contains(string(encoded), "AIHUB synthetic note") || strings.Contains(string(encoded), "aihub-probe-") {
					t.Fatal("note content, ciphertext or virtual path entered report")
				}
			})
		}
	}
}
