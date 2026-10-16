package gatewaybench

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// RequestSpec describes the request a client makes. Build turns it into the
// request of that client's SDK would send: for the gateway, with the platform
// key where the format keeps it; for a fake upstream (the direct runs that the
// gateway's added latency is measured against), the same request without a key.
type RequestSpec struct {
	Format channel.Format
	Stream bool
	Model  string
	// APIKey is the platform key; empty for a direct call to a fake upstream.
	APIKey string
	// MaxTokens is the output limit the client sets; it only shapes the body.
	MaxTokens int
	// PreviousResponseID continues an OpenAI Responses conversation.
	PreviousResponseID string
}

// Build returns the request for the given base URL (scheme, host and port).
func (s RequestSpec) Build(baseURL string) Request {
	model := s.Model
	if model == "" {
		model = "gpt-test"
	}
	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64
	}
	limit := strconv.Itoa(maxTokens)
	header := http.Header{"Content-Type": {"application/json"}, "User-Agent": {"gatewaybench/1"}}
	var path string
	var body bytes.Buffer
	switch s.Format {
	case channel.FormatOpenAIResponses:
		path = "/v1/responses"
		body.WriteString(`{"model":"` + model + `","input":"Say hello.","max_output_tokens":` + limit)
		if s.PreviousResponseID != "" {
			body.WriteString(`,"previous_response_id":"` + s.PreviousResponseID + `"`)
		}
		if s.Stream {
			body.WriteString(`,"stream":true`)
		}
		body.WriteString("}")
		if s.APIKey != "" {
			header.Set("Authorization", "Bearer "+s.APIKey)
		}
	case channel.FormatAnthropic:
		path = "/v1/messages"
		body.WriteString(`{"model":"` + model + `","max_tokens":` + limit + `,"messages":[{"role":"user","content":"Say hello."}]`)
		if s.Stream {
			body.WriteString(`,"stream":true`)
		}
		body.WriteString("}")
		header.Set("anthropic-version", "2023-06-01")
		if s.APIKey != "" {
			header.Set("x-api-key", s.APIKey)
		}
	case channel.FormatGemini:
		path = "/v1beta/models/" + model + ":generateContent"
		if s.Stream {
			path = "/v1beta/models/" + model + ":streamGenerateContent?alt=sse"
		}
		body.WriteString(`{"contents":[{"role":"user","parts":[{"text":"Say hello."}]}],"generationConfig":{"maxOutputTokens":` + limit + `}}`)
		if s.APIKey != "" {
			header.Set("x-goog-api-key", s.APIKey)
		}
	default:
		// A client that does not set stream_options: the gateway turns usage on
		// for it, which is what most OpenAI SDK users get.
		path = "/v1/chat/completions"
		body.WriteString(`{"model":"` + model + `","messages":[{"role":"user","content":"Say hello."}],"max_tokens":` + limit)
		if s.Stream {
			body.WriteString(`,"stream":true`)
		}
		body.WriteString("}")
		if s.APIKey != "" {
			header.Set("Authorization", "Bearer "+s.APIKey)
		}
	}
	return Request{Method: http.MethodPost, URL: baseURL + path, Header: header, Body: body.Bytes()}
}

// ResponseID returns the ID of an OpenAI Responses answer body ("resp_...").
func ResponseID(answer []byte) string {
	const marker = `"id":"resp_`
	start := bytes.Index(answer, []byte(marker))
	if start < 0 {
		return ""
	}
	start += len(`"id":"`)
	end := bytes.IndexByte(answer[start:], '"')
	if end < 0 {
		return ""
	}
	return string(answer[start : start+end])
}

// Complete reports whether the tail of a successful answer carries the marker
// that ends a stream of the format, so a stream cut short counts as an error.
// A non-streamed answer is complete when its body is, which the HTTP framing
// already guarantees.
func Complete(format channel.Format, stream bool, tail []byte) bool {
	if !stream {
		return true
	}
	switch format {
	case channel.FormatOpenAIResponses:
		return bytes.Contains(tail, []byte(`"type":"response.completed"`))
	case channel.FormatAnthropic:
		return bytes.Contains(tail, []byte(`"type":"message_stop"`))
	case channel.FormatGemini:
		return bytes.Contains(tail, []byte(`"finishReason":"STOP"`))
	}
	return bytes.Contains(tail, []byte("data: [DONE]"))
}
