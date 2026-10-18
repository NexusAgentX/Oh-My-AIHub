package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCodexStandardAndLiteToolsShareExactRequestsButKeepIndependentResults(t *testing.T) {
	for _, wrongLiteNamespace := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "wrong_namespace"}[wrongLiteNamespace], func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				if r.URL.Path != "/backend-api/codex/responses" || body["store"] != false {
					t.Error("native tool detector used another endpoint or remote state")
				}
				namespace := ""
				for _, item := range list(body["input"]) {
					if str(object(item)["type"]) == "additional_tools" {
						if len(list(body["tools"])) != 0 || str(object(item)["role"]) != "developer" {
							t.Error("Lite declaration was converted to standard tools")
						}
						namespace = "functions"
						if wrongLiteNamespace {
							namespace = "incorrect"
						}
					}
				}
				resultIDs := map[string]bool{}
				callIDs := map[string]bool{}
				for _, item := range list(body["input"]) {
					p := object(item)
					if str(p["type"]) == "function_call_output" {
						resultIDs[str(p["call_id"])] = true
					}
					if str(p["type"]) == "function_call" {
						callIDs[str(p["call_id"])] = true
					}
				}
				if len(resultIDs) > 0 {
					if !reflect.DeepEqual(callIDs, resultIDs) {
						t.Error("call/result identity lost during native continuation")
					}
					_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", nativeToolResult, nil))
					return
				}
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", "", []nativeCall{{ID: "synthetic-call", Name: "synthetic_echo", Namespace: namespace}}))
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			for _, id := range []string{"RL01", "RL02", "RL03", "RL12"} {
				result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special."+id), nil)
				if !wrongLiteNamespace && result.State != "pass" {
					t.Fatalf("native tool chain %s failed: %+v", id, result)
				}
				if wrongLiteNamespace && result.State == "pass" {
					t.Fatal("Lite namespace mismatch accepted", result)
				}
				if wrongLiteNamespace && id == "RL03" && result.Assertions[0].State != "pass" {
					t.Fatal("Lite failure erased independent standard support", result)
				}
			}
			if !wrongLiteNamespace && (calls != 6 || s.usage.HTTP != 6) {
				t.Fatal("shared request was duplicated or cross-shape replays were skipped", calls)
			}
		})
	}
}

func TestCodexEncryptedReasoningPreservesEntireOpaqueWindow(t *testing.T) {
	const opaque = "synthetic-encrypted+reasoning=="
	calls := 0
	var produced []any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		if body["store"] != false || body["previous_response_id"] != nil {
			t.Error("self-contained reasoning became a remote reference")
		}
		o := nativeFixtureWithCalls("openai-responses", "1591", nil)
		if calls == 1 {
			produced = append([]any{nativeObject{"type": "reasoning", "id": "opaque-unregistered-id", "summary": []any{}, "encrypted_content": opaque, "unknown": nativeObject{"retain": true}}}, list(o["output"])...)
			o["output"] = produced
		} else {
			input := list(body["input"])
			want, _ := json.Marshal(produced)
			got, _ := json.Marshal(input[1 : len(input)-1])
			if string(want) != string(got) {
				t.Error("opaque output window was truncated, reordered or rewritten")
			}
			o = nativeFixtureWithCalls("openai-responses", replayCode, nil)
		}
		_ = json.NewEncoder(w).Encode(o)
	}))
	defer up.Close()
	r, s, service := codexTestSession(t, up.URL)
	result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special.RL09"), nil)
	if result.State != "pass" || calls != 2 {
		t.Fatal("native encrypted continuation failed", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), opaque) || strings.Contains(string(encoded), replayCode) || strings.Contains(string(encoded), "opaque-unregistered-id") {
		t.Fatal("opaque content entered report")
	}
}

func TestCodexCompactionV1AndV2PreserveDifferentNativeWindows(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1", true: "v2"}[v2], func(t *testing.T) {
			calls := 0
			kind, id, firstPath := "compaction", "RL20", "/backend-api/codex/responses/compact"
			if v2 {
				kind, id, firstPath = "context_compaction", "RL21", "/backend-api/codex/responses"
			}
			window := []any{nativeObject{"type": "message", "role": "user", "content": []any{nativeObject{"type": "input_text", "text": "retained-prefix"}}}, nativeObject{"type": kind, "id": "opaque-compaction-id", "encrypted_content": "synthetic-compaction+cipher==", "unknown": nativeObject{"retain": true}}, nativeObject{"type": "message", "role": "assistant", "content": []any{nativeObject{"type": "output_text", "text": "retained-tail"}}}}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				if calls == 1 {
					if r.URL.Path != firstPath || v2 && str(object(list(body["input"])[1])["type"]) != "compaction_trigger" {
						t.Error("standalone and in-band compaction paths conflated")
					}
					o := nativeObject{"object": "response.compaction", "output": window, "usage": nativeObject{"input_tokens": 8, "output_tokens": 3}}
					if v2 {
						o["object"], o["status"], o["id"] = "response", "completed", "resp_fixture"
					}
					_ = json.NewEncoder(w).Encode(o)
					return
				}
				if r.URL.Path != "/backend-api/codex/responses" || body["store"] != false || body["previous_response_id"] != nil {
					t.Error("compaction continuation used a remote resource")
				}
				input := list(body["input"])
				want, _ := json.Marshal(window)
				got, _ := json.Marshal(input[:len(input)-1])
				if string(got) != string(want) {
					t.Error("complete compacted window not replayed")
				}
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-responses", replayCode, nil))
			}))
			defer up.Close()
			r, s, service := codexTestSession(t, up.URL)
			result := service.run(context.Background(), s, nativeItem(t, r, s, "codex-special."+id), nil)
			if result.State != "pass" || calls != 2 {
				t.Fatal("native compaction/replay failed", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "synthetic-compaction+cipher") || strings.Contains(string(encoded), "retained-prefix") {
				t.Fatal("compaction body entered report")
			}
		})
	}
}
