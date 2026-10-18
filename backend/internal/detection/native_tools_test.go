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

func nativeFixtureWithCalls(protocol, text string, calls []nativeCall) nativeObject {
	o, _ := decodeNative([]byte(nativeJSONFixtures[protocol]))
	args := nativeObject{"label": "violet", "meta": nativeObject{"ok": true}, "items": []any{nativeObject{"value": 7}}}
	argJSON, _ := json.Marshal(args)
	switch protocol {
	case "openai-chat-completions":
		c := object(list(o["choices"])[0])
		message := object(c["message"])
		message["content"] = text
		if len(calls) > 0 {
			message["content"] = nil
			c["finish_reason"] = "tool_calls"
			tools := []any{}
			for _, call := range calls {
				tool := nativeObject{"id": call.ID, "type": "function", "function": nativeObject{"name": call.Name, "arguments": string(argJSON)}}
				if call.Custom != "" {
					tool = nativeObject{"id": call.ID, "type": "custom", "custom": nativeObject{"name": call.Name, "input": call.Custom}}
				}
				tools = append(tools, tool)
			}
			message["tool_calls"] = tools
		}
	case "openai-responses":
		if len(calls) == 0 {
			object(list(object(list(o["output"])[0])["content"])[0])["text"] = text
		} else {
			output := []any{}
			for i, call := range calls {
				item := nativeObject{"id": []string{"fc_a", "fc_b"}[i], "type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(argJSON), "status": "completed"}
				if call.Namespace != "" {
					item["namespace"] = call.Namespace
				}
				if call.Custom != "" {
					item = nativeObject{"id": "custom_a", "type": "custom_tool_call", "call_id": call.ID, "name": call.Name, "input": call.Custom}
				}
				output = append(output, item)
			}
			o["output"] = output
		}
	case "native-messages":
		if len(calls) == 0 {
			object(list(o["content"])[0])["text"] = text
		} else {
			blocks := []any{}
			for _, call := range calls {
				blocks = append(blocks, nativeObject{"id": call.ID, "type": "tool_use", "name": call.Name, "input": args})
			}
			o["content"] = blocks
			o["stop_reason"] = "tool_use"
		}
	case "native-generate-content":
		parts := []any{nativeObject{"text": text}}
		if len(calls) > 0 {
			parts = []any{}
			for _, call := range calls {
				parts = append(parts, nativeObject{"functionCall": nativeObject{"id": call.ID, "name": call.Name, "args": args}})
			}
		}
		object(list(o["candidates"])[0])["content"] = nativeObject{"role": "model", "parts": parts}
	}
	return o
}
func TestNativeToolProfilesAndOpaqueResultAssociation(t *testing.T) {
	for protocol := range nativeJSONFixtures {
		t.Run(protocol, func(t *testing.T) {
			requests := 0
			geminiResultIDs := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				raw, _ := io.ReadAll(r.Body)
				body, err := decodeNative(raw)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				text := "No tool selected."
				var calls []nativeCall
				if strings.Contains(string(raw), nativeToolResult) {
					text = nativeToolResult
					if protocol == "native-generate-content" {
						for _, content := range list(body["contents"]) {
							for _, part := range list(object(content)["parts"]) {
								if response := object(object(part)["functionResponse"]); response != nil {
									if str(response["id"]) == "" {
										t.Error("native function response lost original call ID")
									}
									geminiResultIDs++
								}
							}
						}
					}
				} else {
					none := body["tool_choice"] == "none" || str(object(body["tool_choice"])["type"]) == "none" || str(object(object(body["toolConfig"])["functionCallingConfig"])["mode"]) == "NONE"
					if !none {
						call := nativeCall{ID: "call_a", Name: "synthetic_echo"}
						if strings.Contains(string(raw), "synthetic_raw") {
							call.Name = "synthetic_raw"
							call.Custom = "synthetic-input"
						}
						if protocol == "openai-responses" && str(object(list(body["tools"])[0])["type"]) == "namespace" {
							call.Namespace = "synthetic"
						}
						calls = []nativeCall{call}
						if strings.Contains(string(raw), "twice independently") {
							second := call
							second.ID = "call_b"
							calls = append(calls, second)
						}
					}
				}
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls(protocol, text, calls))
			}))
			defer up.Close()
			registry, session, service := nativeTestSession(t, up.URL)
			plan, err := registry.Plan(session.Target)
			if err != nil {
				t.Fatal(err)
			}
			var item Planned
			for _, p := range plan.Items {
				if slices.Contains(p.Requirements, protocol+".tools") {
					item = p
					break
				}
			}
			result := service.run(context.Background(), session, item, nil)
			if result.State != "pass" {
				t.Fatalf("tools failed: %+v", result)
			}
			if requests < 7 || session.usage.HTTP != int64(requests) {
				t.Fatalf("missing native cases or duplicate accounting: %d %+v", requests, session.usage)
			}
			if protocol == "native-generate-content" && geminiResultIDs < 3 {
				t.Fatalf("result IDs not exercised: %d", geminiResultIDs)
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), nativeToolResult) || strings.Contains(string(stored), "synthetic-input") {
				t.Fatal("tool input/result body persisted")
			}
		})
	}
}
