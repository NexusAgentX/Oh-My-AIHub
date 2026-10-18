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
)

func builtinFixture(protocol, kind string) nativeObject {
	text := toolPageTitle
	if kind == "code" {
		text = codeAnswer
	}
	if kind == "image_tool" {
		text = "Generated synthetic image."
	}
	o := nativeFixtureWithCalls(protocol, text, nil)
	image := base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))
	if protocol == "openai-responses" {
		var item nativeObject
		switch kind {
		case "search":
			item = nativeObject{"type": "web_search_call", "id": "search_synthetic", "status": "completed", "action": nativeObject{"type": "search", "query": "IANA example.com page title", "sources": []any{nativeObject{"type": "url", "url": toolPageURL, "title": toolPageTitle}}}}
		case "code":
			item = nativeObject{"type": "code_interpreter_call", "id": "code_synthetic", "status": "completed", "container_id": "synthetic-container", "code": "print(137 * 29)", "outputs": []any{nativeObject{"type": "logs", "logs": codeAnswer + "\n"}}}
		case "image_tool":
			item = nativeObject{"type": "image_generation_call", "id": "image_synthetic", "status": "completed", "result": image}
		}
		o["output"] = append([]any{item}, list(o["output"])...)
	} else if protocol == "native-messages" {
		name, typ := "web_search", "web_search_tool_result"
		var result any = []any{nativeObject{"type": "web_search_result", "url": toolPageURL, "title": toolPageTitle, "encrypted_content": "synthetic-encrypted-search"}}
		if kind == "code" {
			name, typ = "bash_code_execution", "bash_code_execution_tool_result"
			result = nativeObject{"type": "bash_code_execution_result", "stdout": codeAnswer + "\n", "stderr": "", "return_code": 0, "content": []any{}}
		}
		o["content"] = append([]any{nativeObject{"type": "server_tool_use", "id": "server_synthetic", "name": name, "input": nativeObject{"query": "synthetic task"}}, nativeObject{"type": typ, "tool_use_id": "server_synthetic", "content": result}}, list(o["content"])...)
	} else {
		candidate := object(list(o["candidates"])[0])
		content := object(candidate["content"])
		parts := list(content["parts"])
		switch kind {
		case "search", "url_context":
			typ := "GOOGLE_SEARCH_WEB"
			if kind == "search" {
				candidate["groundingMetadata"] = nativeObject{"webSearchQueries": []any{"IANA example.com"}, "groundingChunks": []any{nativeObject{"web": nativeObject{"uri": toolPageURL, "title": toolPageTitle}}}}
			} else {
				typ = "URL_CONTEXT"
				candidate["urlContextMetadata"] = nativeObject{"urlMetadata": []any{nativeObject{"retrievedUrl": toolPageURL, "urlRetrievalStatus": "URL_RETRIEVAL_STATUS_SUCCESS"}}}
			}
			parts = append([]any{nativeObject{"toolCall": nativeObject{"id": "server_synthetic", "toolType": typ, "args": nativeObject{"query": "synthetic"}}, "thoughtSignature": "synthetic-opaque-signature"}, nativeObject{"toolResponse": nativeObject{"id": "server_synthetic", "toolType": typ, "response": nativeObject{"title": toolPageTitle}}}}, parts...)
		case "code":
			parts = append([]any{nativeObject{"executableCode": nativeObject{"id": "code_synthetic", "language": "PYTHON", "code": "print(137*29)"}}, nativeObject{"codeExecutionResult": nativeObject{"id": "code_synthetic", "outcome": "OUTCOME_OK", "output": codeAnswer + "\n"}}}, parts...)
		case "image_tool":
			parts = append(parts, nativeObject{"inlineData": nativeObject{"mimeType": "image/png", "data": image}})
		}
		content["parts"] = parts
	}
	return o
}

func TestNativeBuiltinsRequireActualResultsAndShareEchoRequests(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls, echoes := 0, 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				kind := "search"
				if strings.Contains(string(raw), "compute and print") {
					kind = "code"
				}
				if strings.Contains(string(raw), "blue circle") {
					kind = "image_tool"
				}
				if strings.Contains(string(raw), "URL context tool") {
					kind = "url_context"
				}
				if protocol == "native-messages" {
					typ := str(object(list(body["tools"])[0])["type"])
					if typ != "web_search_20250305" && typ != "code_execution_20250825" {
						t.Error("fixed named server tool version changed")
					}
				}
				response := builtinFixture(protocol, kind)
				if strings.Contains(string(raw), "Repeat only the page title") {
					echoes++
					if !strings.Contains(string(raw), "server_synthetic") || !strings.Contains(string(raw), "synthetic-opaque-signature") || !strings.Contains(string(raw), "toolResponse") {
						t.Error("server-native tool pair or signature lost in echo")
					}
					response = nativeFixtureWithCalls(protocol, toolPageTitle, nil)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			families := []string{"search", "code", "image_tool"}
			if protocol == "native-messages" {
				families = []string{"server_tools"}
			}
			if protocol == "native-generate-content" {
				families = append(families, "url_context", "server_echo")
			}
			for _, family := range families {
				result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+"."+family), nil)
				want := "pass"
				if family == "code" {
					want = "inconclusive"
				}
				if result.State != want {
					t.Fatalf("%s native built-in: %+v", family, result)
				}
				if family == "code" && result.Assertions[0].State != "pass" {
					t.Fatal("missing session receipt hid verified execution")
				}
				stored, _ := json.Marshal(result)
				for _, secret := range []string{"synthetic-encrypted-search", "synthetic-opaque-signature", "print(137", base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))} {
					if strings.Contains(string(stored), secret) {
						t.Fatal("in-call tool content persisted")
					}
				}
			}
			want := map[string]int{"openai-responses": 3, "native-messages": 2, "native-generate-content": 6}[protocol]
			if calls != want || protocol == "native-generate-content" && echoes != 2 {
				t.Fatalf("server echo repeated search or omitted real replay: calls=%d echoes=%d", calls, echoes)
			}
		})
	}
}

func TestBuiltinValidatorsRejectInventedCallsWrongAssociationsAndBadProducts(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "native-generate-content", "native-messages"} {
		for _, kind := range []string{"search", "code"} {
			fake := nativeFixtureWithCalls(protocol, map[string]string{"search": toolPageTitle, "code": codeAnswer}[kind], nil)
			out := nativeOutcome{Reply: parseNative(protocol, fake)}
			observed, valid := responseCode(out)
			if protocol == "native-generate-content" {
				observed, valid = geminiCode(out)
			}
			if protocol == "native-messages" {
				observed, valid = messageServerResult(out, kind)
			}
			if kind == "search" && protocol == "openai-responses" {
				observed, valid = responseSearch(out)
			}
			if kind == "search" && protocol == "native-generate-content" {
				observed, valid = geminiSearch(out)
			}
			if observed || valid {
				t.Fatal("text-only answer became executed tool proof")
			}
		}
	}
	o := builtinFixture("native-generate-content", "search")
	parts := list(object(object(list(o["candidates"])[0])["content"])["parts"])
	object(object(parts[1])["toolResponse"])["id"] = "different"
	if geminiEchoParts(nativeOutcome{Reply: parseNative("native-generate-content", o)}, "GOOGLE_SEARCH_WEB") {
		t.Fatal("mismatched server tool pair passed")
	}
}

func TestMessagesServerToolPauseReplaysOpaqueBlocksAndKeepsEveryWitness(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		o := builtinFixture("native-messages", "search")
		if calls == 1 {
			o["stop_reason"] = "pause_turn"
			o["content"] = list(o["content"])[:2]
		} else if calls == 2 {
			if !strings.Contains(string(raw), "synthetic-encrypted-search") || !strings.Contains(string(raw), "web_search_20250305") {
				t.Error("pause continuation lost native state")
			}
			o = nativeFixtureWithCalls("native-messages", toolPageTitle, nil)
		} else {
			o = builtinFixture("native-messages", "code")
		}
		_ = json.NewEncoder(w).Encode(o)
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	result := service.run(context.Background(), s, nativeItem(t, r, s, "native-messages.server_tools"), nil)
	if result.State != "pass" || calls != 3 || len(result.Assertions[0].AttemptIDs) != 2 {
		t.Fatalf("pause result: %+v calls=%d", result, calls)
	}
}
