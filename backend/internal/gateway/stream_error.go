package gateway

import (
	"encoding/json"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

const (
	codeProtocolError    = "upstream_protocol_error"
	messageProtocolError = "上游响应报告协议错误"
)

// Protocol errors are facts, not provider-controlled diagnostic strings. Never
// copy their code, message, nested content or credentials into the observation.
// The cheap member scan keeps normal content frames out of encoding/json.
func (o *Observer) observeProtocolError(raw []byte, keys frameKeys) {
	if o.protocolError && !o.protocolClientError {
		return
	}
	if keys&(keyError|keyEnvelope) == 0 {
		return
	}
	var envelope struct {
		Type   string    `json:"type"`
		Object string    `json:"object"`
		Status string    `json:"status"`
		Error  *struct{} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	// Only top-level error objects count: errors in generated text, tool
	// results or other nested content do not describe the gateway request.
	errorObject := envelope.Error != nil
	failed := false
	switch o.format {
	case channel.FormatOpenAIChat, channel.FormatGemini:
		failed = errorObject
	case channel.FormatAnthropic:
		failed = envelope.Type == "error" && errorObject
	case channel.FormatOpenAIResponses:
		// A Responses error event carries code/message directly; response.failed
		// carries the failed Response under response. Neither requires us to
		// retain that arbitrary content. A failed tool item alone is not a
		// failed response.
		failed = envelope.Type == "error" || envelope.Type == "response.failed" || envelope.Object == "response" && envelope.Status == "failed"
	}
	if !failed {
		return
	}
	clientError := protocolRequestError(raw, o.format, envelope.Type)
	// A later channel error dominates an earlier request error, and cannot be
	// erased by a later benign-looking event.
	if !o.protocolError {
		o.protocolClientError = clientError
	} else {
		o.protocolClientError = o.protocolClientError && clientError
	}
	o.protocolError = true
}

// Only protocol-defined, unambiguous request errors leave channel health out of
// the denominator. Do not infer a status from arbitrary provider text or assume
// that OpenAI's broad invalid_request_error type excludes credential failures.
// This path runs only after a failure was recognized, never on ordinary frames.
func protocolRequestError(raw []byte, format channel.Format, kind string) bool {
	body := raw
	if format == channel.FormatOpenAIResponses && kind == "response.failed" {
		var root struct {
			Response struct {
				Error json.RawMessage `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &root) != nil {
			return false
		}
		body = root.Response.Error
	}
	fields := errorFields(body)
	if fields == nil {
		return false
	}
	request := false
	switch format {
	case channel.FormatAnthropic:
		request = fields["type"] == "invalid_request_error" || fields["type"] == "request_too_large"
	case channel.FormatGemini:
		request = fields["status"] == "INVALID_ARGUMENT" && fields["code"] == float64(400)
	case channel.FormatOpenAIChat, channel.FormatOpenAIResponses:
		switch fields["code"] {
		case "invalid_prompt", "context_length_exceeded":
			request = true
		}
	}
	// The model sent upstream belongs to the channel's configuration. Preserve
	// the same model-unavailable exception as the non-2xx error path.
	return request && !ModelUnavailable(body)
}
