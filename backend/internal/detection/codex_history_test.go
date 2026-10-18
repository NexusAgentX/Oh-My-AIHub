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

func TestCodexHistoryReadsOnlyOwnedSeededItemAndDetectsCrossSessionLeak(t *testing.T) {
	for _, mode := range []string{"isolated", "shared", "not_yet_observed"} {
		for _, layer := range []string{"upstream_protocol", "gateway_proxy"} {
			t.Run(layer+"/"+mode, func(t *testing.T) {
				owner, window, marker := "", "", ""
				reads, searches := 0, 0
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/backend-api/codex/models" {
						_, _ = io.WriteString(w, `{"models":[{"slug":"synthetic-model"}]}`)
						return
					}
					raw, _ := io.ReadAll(r.Body)
					body, _ := decodeNative(raw)
					if r.URL.Path == "/backend-api/codex/responses" {
						if tools := list(body["tools"]); len(tools) > 0 {
							prompt := str(object(list(body["input"])[0])["content"])
							args, _ := decodeNative([]byte(prompt[strings.Index(prompt, "{"):]))
							args["query"] = "sealed:" + str(args["query"])
							out := nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "history-query-call", Name: "search_contents", Namespace: "history"}})
							object(list(out["output"])[0])["arguments"] = string(mustNativeJSON(args))
							_ = json.NewEncoder(w).Encode(out)
							return
						}
						input := list(body["input"])
						if len(input) == 1 {
							if owner != "" {
								_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", "OK", nil))
								return
							}
							marker = str(object(input[0])["content"])
							metadata := object(body["client_metadata"])
							owner, window = str(metadata["session_id"]), str(metadata["x-codex-window-id"])
							turn, _ := decodeNative([]byte(str(metadata["x-codex-turn-metadata"])))
							if body["store"] != false || turn["history_ingest_requested"] != true || str(turn["session_id"]) != owner || str(turn["thread_id"]) != owner || str(turn["window_id"]) != window {
								t.Error("actual history ingest metadata was missing or lost namespace consistency")
							}
							if window != owner+":0" || str(turn["context_window_id"]) == "" || str(turn["context_window_id"]) == window {
								t.Error("logical and stored context window identifiers were conflated")
							}
							window = str(turn["context_window_id"])
							_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", "OK", nil))
							return
						}
						call, output := object(input[1]), object(input[2])["output"]
						var value any
						if str(output) != "" {
							_ = json.Unmarshal([]byte(str(output)), &value)
						} else {
							cipher := str(object(list(output)[0])["encrypted_content"])
							plain, _ := base64.StdEncoding.DecodeString(cipher)
							_ = json.Unmarshal(plain, &value)
						}
						text := "{}"
						switch str(call["name"]) {
						case "list_windows":
							for _, row := range list(value) {
								text += str(object(row)["window_id"]) + "\n"
							}
						case "list_items":
							if rows := list(value); len(rows) > 0 {
								text = string(mustNativeJSON(rows[0]))
							}
						default:
							if content := str(object(value)["content"]); content != "" {
								text = content
							}
						}
						_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", strings.TrimSpace(text), nil))
						return
					}
					if !strings.HasPrefix(r.URL.Path, "/backend-api/codex/alpha/history/v2/") {
						t.Error("history detector escaped fixed context operations")
					}
					operation := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					var value any = nativeObject{}
					switch operation {
					case "list_windows":
						value = []any{nativeObject{"window_id": window}}
					case "list_items":
						if str(body["window_id"]) != window {
							t.Error("detector selected an unowned window from shared history")
						}
						value = []any{}
						if mode != "not_yet_observed" {
							value = []any{nativeObject{"item_id": "owned-seeded-item", "window_id": window, "content": marker}}
						}
					case "search_contents":
						searches++
						if body["query"] != "sealed:AIHUB-HISTORY-" || r.Header.Get("X-Openai-Encrypted-Tool-Arguments") != "true" {
							t.Error("history query replaced actual model-produced encrypted arguments")
						}
						value = nativeObject{"content": marker}
					case "read_item":
						reads++
						if str(body["window_id"]) != window || str(body["item_id"]) != "owned-seeded-item" || mode == "not_yet_observed" {
							t.Error("history detector guessed or accessed an unowned item")
						}
						if str(object(body["context"])["session_id"]) == owner || mode == "shared" {
							value = nativeObject{"content": marker}
						}
					default:
						t.Error("unknown history operation")
					}
					_ = json.NewEncoder(w).Encode(nativeObject{"encrypted_output": base64.StdEncoding.EncodeToString(mustNativeJSON(value))})
				}))
				defer up.Close()
				r, s, service := codexTestSession(t, up.URL)
				s.Target.Binding = market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}
				s.Target.Model, s.Target.ModelType = "", ""
				if err := r.RegisterCodexGateway(); err != nil {
					t.Fatal(err)
				}
				result := service.run(context.Background(), s, codexLayerItem(t, r, s, "codex-special.RL19", layer), nil)
				if result.State == "invalid" || len(result.Assertions) != 6 || searches != 1 {
					t.Fatal("history operations or assertions were lost", result)
				}
				if mode == "not_yet_observed" {
					if reads != 0 || result.Assertions[2].Reason != "prerequisite_failed" || result.State == "pass" {
						t.Fatal("missing item evidence was replaced with a guessed read", result)
					}
				} else if reads != 2 || (result.State == "pass") != (mode == "isolated") {
					t.Fatal("history remote resume/isolation verdict wrong", result)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), marker) || strings.Contains(string(encoded), window) || strings.Contains(string(encoded), "owned-seeded-item") {
					t.Fatal("history content or resource IDs entered report")
				}
			})
		}
	}
}
