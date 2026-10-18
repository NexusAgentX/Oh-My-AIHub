package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPFixtureHasOnlyAuthenticatedSyntheticToolAndRevokesImmediately(t *testing.T) {
	f, err := NewMCPFixture("https://gateway.synthetic.example")
	if err != nil {
		t.Fatal(err)
	}
	p, closeProbe, err := f.open()
	if err != nil {
		t.Fatal(err)
	}
	request := func(token, method string, params any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(nativeObject{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		r := httptest.NewRequest("POST", mcpFixturePath, strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w
	}
	if request("platform-or-foreign-key", "tools/list", nativeObject{}).Code != 404 {
		t.Fatal("unknown credential accepted")
	}
	if request(p.token, "initialize", nativeObject{"protocolVersion": "2025-06-18"}).Code != 200 {
		t.Fatal("initialization failed")
	}
	w := request(p.token, "tools/list", nativeObject{})
	if w.Code != 200 || strings.Contains(w.Body.String(), p.marker) {
		t.Fatal("list leaked unknown test answer")
	}
	if request(p.token, "tools/call", nativeObject{"name": mcpTool, "arguments": nativeObject{"url": "https://example.com"}}).Code != 400 {
		t.Fatal("arbitrary arguments accepted")
	}
	w = request(p.token, "tools/call", nativeObject{"name": mcpTool, "arguments": nativeObject{}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), p.marker) || p.calls.Load() != 1 {
		t.Fatal("missing actual synthetic call")
	}
	closeProbe()
	if request(p.token, "tools/list", nativeObject{}).Code != 404 {
		t.Fatal("completed task token still active")
	}
	for _, origin := range []string{"http://example.com", "https://example.com/path", "https://user:pass@example.com", "https://example.com/?token=secret"} {
		if _, err = NewMCPFixture(origin); err == nil {
			t.Fatal("unsafe configured origin accepted")
		}
	}
}

func TestNativeMCPRequiresActualAuthorizedCallbackAndApproval(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "native-messages"} {
		t.Run(protocol, func(t *testing.T) {
			f, _ := NewMCPFixture("https://gateway.synthetic.example")
			callback := httptest.NewServer(f)
			defer callback.Close()
			f.origin = callback.URL
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				token := ""
				response := nativeFixtureWithCalls(protocol, "", nil)
				if protocol == "openai-responses" {
					tool := object(list(body["tools"])[0])
					token = str(tool["authorization"])
					if str(tool["require_approval"]) != "always" {
						t.Error("approval disabled")
					}
					if calls == 1 {
						response["output"] = []any{nativeObject{"type": "mcp_approval_request", "id": "approval_synthetic", "name": mcpTool, "server_label": mcpServer, "arguments": "{}"}}
						_ = json.NewEncoder(w).Encode(response)
						return
					}
					if !strings.Contains(string(raw), `"approval_request_id":"approval_synthetic"`) {
						t.Error("native approval reference absent")
					}
				} else {
					if r.Header.Get("anthropic-beta") != "mcp-client-2025-11-20" {
						t.Error("beta not pinned")
					}
					token = str(object(list(body["mcp_servers"])[0])["authorization_token"])
				}
				payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + mcpTool + `","arguments":{}}}`
				req, _ := http.NewRequest("POST", callback.URL+mcpFixturePath, strings.NewReader(payload))
				req.Header.Set("Authorization", "Bearer "+token)
				resp, e := callback.Client().Do(req)
				if e != nil {
					t.Error(e)
					return
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				o, _ := decodeNative(b)
				marker := str(object(list(object(o["result"])["content"])[0])["text"])
				if marker == "" {
					t.Error("fixture did not return marker")
				}
				response = nativeFixtureWithCalls(protocol, marker, nil)
				if protocol == "openai-responses" {
					response["output"] = append([]any{nativeObject{"type": "mcp_call", "id": "call_synthetic", "name": mcpTool, "server_label": mcpServer, "arguments": "{}", "output": marker, "error": nil}}, list(response["output"])...)
				} else {
					response["content"] = append([]any{nativeObject{"type": "mcp_tool_use", "id": "call_synthetic", "name": mcpTool, "server_name": mcpServer, "input": nativeObject{}}, nativeObject{"type": "mcp_tool_result", "tool_use_id": "call_synthetic", "is_error": false, "content": []any{nativeObject{"type": "text", "text": marker}}}}, list(response["content"])...)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			if err := r.RegisterMCP(f); err != nil {
				t.Fatal(err)
			}
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+".mcp"), nil)
			if result.State != "pass" {
				t.Fatalf("MCP result: %+v", result)
			}
			if calls != map[string]int{"openai-responses": 2, "native-messages": 1}[protocol] {
				t.Fatal("unbounded MCP round trip")
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), "AIHub-MCP-") || strings.Contains(string(stored), "Bearer") {
				t.Fatal("MCP private content persisted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.probes) != 0 {
				t.Fatal("probe leaked")
			}
		})
	}
}
