package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// protocolMatrixCase pins the per-protocol behavior every adapter must keep:
// native success delivery, canonical model restoration, fallback before the
// commit point, and usage contamination after it.
type protocolMatrixCase struct {
	name            string
	protocol        channel.Protocol
	authHeader      string
	authPrefix      string
	nonStreamPath   string
	streamPath      string
	pathModel       string
	nonStreamBody   string
	streamBody      string
	nonStreamOK     string
	streamOK        string
	streamEarlyFail string
	streamBadUsage  string
}

func protocolMatrixCases() []protocolMatrixCase {
	return []protocolMatrixCase{
		{
			name: "chat", protocol: channel.ProtocolOpenAIChat, authHeader: "Authorization", authPrefix: "Bearer ",
			nonStreamPath: "/v1/chat/completions", streamPath: "/v1/chat/completions",
			nonStreamBody: `{"model":"canonical/model","messages":[]}`, streamBody: `{"model":"canonical/model","stream":true,"messages":[]}`,
			nonStreamOK:     `{"model":"vendor-model","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
			streamOK:        validChatStream("vendor-model", "hi"),
			streamEarlyFail: `data: {"error":{"code":"upstream_failed","message":"failed"}}` + "\n\n",
			streamBadUsage: `data: {"model":"vendor-model","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
				`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":{"audio_tokens":1}}}` + "\n\ndata: [DONE]\n\n",
		},
		{
			name: "responses", protocol: channel.ProtocolOpenAIResponse, authHeader: "Authorization", authPrefix: "Bearer ",
			nonStreamPath: "/v1/responses", streamPath: "/v1/responses",
			nonStreamBody: `{"model":"canonical/model","input":"hi"}`, streamBody: `{"model":"canonical/model","stream":true,"input":"hi"}`,
			nonStreamOK: `{"id":"r1","model":"vendor-model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
			streamOK: `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"model":"vendor-model","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}` + "\n\n",
			streamEarlyFail: `data: {"type":"response.failed","response":{"error":{"code":"upstream_failed","message":"failed"}}}` + "\n\n",
			streamBadUsage: `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"model":"vendor-model","usage":{"input_tokens":2,"output_tokens":1,"input_tokens_details":{"audio_tokens":1}}}}` + "\n\n",
		},
		{
			name: "anthropic", protocol: channel.ProtocolAnthropic, authHeader: "x-api-key", authPrefix: "",
			nonStreamPath: "/v1/messages", streamPath: "/v1/messages",
			nonStreamBody: `{"model":"canonical/model","max_tokens":8,"messages":[]}`, streamBody: `{"model":"canonical/model","max_tokens":8,"stream":true,"messages":[]}`,
			nonStreamOK: `{"type":"message","model":"vendor-model","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":3,"output_tokens":4}}`,
			streamOK: "event: message_start\n" + `data: {"type":"message_start","message":{"model":"vendor-model","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n" +
				"event: content_block_delta\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
				"event: message_delta\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}` + "\n\n" +
				"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n",
			streamEarlyFail: "event: error\n" + `data: {"type":"error","error":{"type":"overloaded_error","message":"failed"}}` + "\n\n",
			streamBadUsage: "event: message_start\n" + `data: {"type":"message_start","message":{"model":"vendor-model","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n" +
				"event: content_block_delta\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
				"event: message_delta\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4,"server_tool_use":{"web_search_requests":1}}}` + "\n\n" +
				"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n",
		},
		{
			name: "gemini", protocol: channel.ProtocolGemini, authHeader: "x-goog-api-key", authPrefix: "", pathModel: "canonical/model",
			nonStreamPath: "/v1beta/models/canonical/model:generateContent", streamPath: "/v1beta/models/canonical/model:streamGenerateContent?alt=sse",
			nonStreamBody: `{"contents":[]}`, streamBody: `{"contents":[]}`,
			nonStreamOK:     `{"modelVersion":"vendor-model","candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
			streamOK:        `data: {"modelVersion":"vendor-model","candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}` + "\n\n",
			streamEarlyFail: `data: {"error":{"code":500,"message":"failed","status":"INTERNAL"}}` + "\n\n",
			streamBadUsage: `data: {"modelVersion":"vendor-model","candidates":[{"content":{"parts":[{"text":"hi"}]}}]}` + "\n\n" +
				`data: {"modelVersion":"vendor-model","candidates":[{"content":{"parts":[{"text":"."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":2}]}}` + "\n\n",
		},
	}
}

func (c protocolMatrixCase) candidate(priority int, offerID string) Candidate {
	candidate := proxyCandidate(priority, offerID, "vendor-model", "key-"+offerID)
	candidate.Lease.Protocol = c.protocol
	return candidate
}

func (c protocolMatrixCase) request(stream bool) *http.Request {
	path, body := c.nonStreamPath, c.nonStreamBody
	if stream {
		path, body = c.streamPath, c.streamBody
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set(c.authHeader, c.authPrefix+"oma_live_"+strings.Repeat("m", 43))
	return request
}

func (c protocolMatrixCase) serve(service *Service, stream bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, c.request(stream), c.protocol, c.pathModel, stream && c.protocol == channel.ProtocolGemini)
	return recorder
}

func protocolMatrixOutbound(handlers map[string]func(*http.Request) (*http.Response, error)) *proxyOutbound {
	return &proxyOutbound{handlers: handlers}
}

func TestProtocolMatrixDeliversNativeSuccessAndRestoresModel(t *testing.T) {
	for _, c := range protocolMatrixCases() {
		for _, stream := range []bool{false, true} {
			name := c.name + "/non-stream"
			upstream, header := c.nonStreamOK, jsonHeader()
			if stream {
				name, upstream, header = c.name+"/stream", c.streamOK, sseHeader()
			}
			t.Run(name, func(t *testing.T) {
				store := newProxyStore([]Candidate{c.candidate(1, "offer-one")})
				outbound := protocolMatrixOutbound(map[string]func(*http.Request) (*http.Response, error){
					"offer-one": func(*http.Request) (*http.Response, error) {
						return proxyResponse(http.StatusOK, header, upstream), nil
					},
				})
				service, _ := NewService(store, outbound)
				recorder := c.serve(service, stream)
				body := recorder.Body.String()
				if recorder.Code != http.StatusOK || !strings.Contains(body, "canonical/model") || strings.Contains(body, "vendor-model") || !strings.Contains(body, "hi") {
					t.Fatalf("response = %d %s", recorder.Code, body)
				}
				requests := outbound.snapshotRequests()
				// Gemini carries the model in the URL path; the others rewrite the body.
				if len(requests) != 1 || (c.protocol != channel.ProtocolGemini && !strings.Contains(requests[0].body, "vendor-model")) {
					t.Fatalf("upstream request = %+v", requests)
				}
				store.mu.Lock()
				defer store.mu.Unlock()
				if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || store.completed[0].Usage == nil ||
					len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded || store.finalized[0].Usage == nil {
					t.Fatalf("facts = attempts:%+v final:%+v", store.completed, store.finalized)
				}
			})
		}
	}
}

func TestProtocolMatrixFallsBackBeforeCommitPoint(t *testing.T) {
	for _, c := range protocolMatrixCases() {
		t.Run(c.name, func(t *testing.T) {
			store := newProxyStore([]Candidate{c.candidate(1, "offer-one"), c.candidate(2, "offer-two")})
			outbound := protocolMatrixOutbound(map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), c.streamEarlyFail), nil
				},
				"offer-two": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), c.streamOK), nil
				},
			})
			service, _ := NewService(store, outbound)
			recorder := c.serve(service, true)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hi") || strings.Contains(recorder.Body.String(), "upstream_failed") {
				t.Fatalf("fallback response = %d %s", recorder.Code, recorder.Body.String())
			}
			if len(outbound.snapshotRequests()) != 2 {
				t.Fatalf("upstream requests = %d, want 2", len(outbound.snapshotRequests()))
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 2 || store.completed[0].Status != AttemptFailed || store.completed[0].SemanticCommitted ||
				store.completed[1].Status != AttemptSucceeded || len(store.finalized) != 1 || store.finalized[0].FinalOfferID != "offer-two" {
				t.Fatalf("fallback facts = attempts:%+v final:%+v", store.completed, store.finalized)
			}
		})
	}
}

func TestProtocolMatrixUnpriceableUsagePoisonsTheWholeCall(t *testing.T) {
	for _, c := range protocolMatrixCases() {
		t.Run(c.name, func(t *testing.T) {
			store := newProxyStore([]Candidate{c.candidate(1, "offer-one"), c.candidate(2, "offer-two")})
			outbound := protocolMatrixOutbound(map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), c.streamBadUsage), nil
				},
				"offer-two": func(*http.Request) (*http.Response, error) {
					t.Fatal("second candidate was called after the first delivered content")
					return nil, nil
				},
			})
			service, _ := NewService(store, outbound)
			recorder := c.serve(service, true)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hi") {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].Usage != nil ||
				len(store.finalized) != 1 || store.finalized[0].Status != CallIncomplete || store.finalized[0].Usage != nil {
				t.Fatalf("poisoned facts = attempts:%+v final:%+v", store.completed, store.finalized)
			}
		})
	}
}
