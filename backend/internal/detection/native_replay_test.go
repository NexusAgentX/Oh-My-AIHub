package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func nativeItem(t *testing.T, r *Registry, s *Session, requirement string) Planned {
	t.Helper()
	plan, err := r.Plan(s.Target)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Items {
		if slices.Contains(p.Requirements, requirement) {
			return p
		}
	}
	t.Fatalf("missing requirement %s", requirement)
	return Planned{}
}

func TestNativeReasoningReplaysCompleteOpaquePartsWithoutRemoteReference(t *testing.T) {
	const opaque = "synthetic-signed+cipher=="
	for _, protocol := range []string{"openai-responses", "native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				text := "1591"
				if calls > 1 {
					if !strings.Contains(string(raw), opaque) || body["previous_response_id"] != nil || body["conversation"] != nil {
						t.Error("opaque replay lost data or acquired remote state")
					}
					text = replayCode
				}
				o := nativeFixtureWithCalls(protocol, text, nil)
				if calls == 1 {
					switch protocol {
					case "openai-responses":
						o["output"] = append([]any{nativeObject{"type": "reasoning", "id": "rs_unregistered", "encrypted_content": opaque, "summary": []any{nativeObject{"type": "summary_text", "text": "Synthetic reasoning"}}}}, list(o["output"])...)
					case "native-messages":
						o["content"] = append([]any{nativeObject{"type": "thinking", "thinking": "Synthetic reasoning", "signature": opaque}}, list(o["content"])...)
					case "native-generate-content":
						content := object(object(list(o["candidates"])[0])["content"])
						content["parts"] = append([]any{nativeObject{"text": "Synthetic reasoning", "thought": true, "thoughtSignature": opaque}}, list(content["parts"])...)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(o)
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+".reasoning"), nil)
			if result.State != "pass" || calls != 2 {
				t.Fatalf("opaque replay: calls=%d result=%+v", calls, result)
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), opaque) || strings.Contains(string(stored), "Synthetic reasoning") {
				t.Fatal("opaque or reasoning body persisted")
			}
		})
	}
}

func TestNativeCompactionReplaysReturnedWindowInsteadOfOriginalHistory(t *testing.T) {
	const signature = "synthetic-compaction-signature"
	for _, protocol := range []string{"openai-responses", "native-messages"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "input_tokens") || strings.HasSuffix(r.URL.Path, "count_tokens") {
					_ = json.NewEncoder(w).Encode(nativeObject{"input_tokens": 10})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/compact") {
					_ = json.NewEncoder(w).Encode(nativeObject{"object": "response.compaction", "output": []any{nativeObject{"type": "message", "role": "user", "content": "before-window"}, nativeObject{"type": "compaction", "id": "cmp_unregistered", "encrypted_content": signature}, nativeObject{"type": "message", "role": "assistant", "content": "after-window"}}})
					return
				}
				if body["compaction"] != nil {
					if r.Header.Get("anthropic-beta") != compactionBeta {
						t.Error("wrong native compaction beta")
					}
					o := nativeFixtureWithCalls(protocol, "", nil)
					o["stop_reason"] = "compaction"
					o["content"] = []any{nativeObject{"type": "compaction", "content": "Synthetic summary with code " + replayCode, "signature": signature}}
					o["usage"] = nativeObject{"input_tokens": 0, "output_tokens": 0, "iterations": []any{nativeObject{"type": "compaction", "input_tokens": 12, "output_tokens": 5}}}
					_ = json.NewEncoder(w).Encode(o)
					return
				}
				if !strings.Contains(string(raw), signature) || strings.Contains(string(raw), "Remember this synthetic code") {
					t.Error("compaction dropped opaque window or sent summarized plaintext again")
				}
				if protocol == "openai-responses" {
					input := list(body["input"])
					if len(input) != 4 || str(object(input[0])["content"]) != "before-window" || str(object(input[1])["type"]) != "compaction" || str(object(input[2])["content"]) != "after-window" {
						t.Error("complete window reordered or trimmed")
					}
				} else if r.Header.Get("anthropic-beta") != compactionBeta {
					t.Error("beta missing on signed continuation")
				}
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls(protocol, replayCode, nil))
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+".count_compact"), nil)
			if result.State != "pass" || calls != 3 {
				t.Fatalf("compact: calls=%d result=%+v", calls, result)
			}
			if protocol == "native-messages" && (s.usage.InputTokens != 22 || s.usage.OutputTokens != 9) {
				t.Fatalf("iteration accounting lost or duplicated: %+v", s.usage)
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), signature) || strings.Contains(string(stored), replayCode) {
				t.Fatal("compaction body persisted")
			}
		})
	}
}

func TestNativeCountingDoesNotProveCompactionSupport(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "input_tokens") {
			_, _ = w.Write([]byte(`{"input_tokens":10}`))
			return
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"unsupported_parameter","message":"Unsupported compaction"}}`))
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	result := service.run(context.Background(), s, nativeItem(t, r, s, "openai-responses.count_compact"), nil)
	if result.Assertions[0].State != "pass" || result.Assertions[1].State != "unsupported" || result.Assertions[2].State != "unsupported" || s.usage.HTTP != 2 {
		t.Fatalf("count borrowed as compact proof: %+v", result)
	}
}
