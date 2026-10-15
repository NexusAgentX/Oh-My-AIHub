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
	if o.protocolError {
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
	switch o.format {
	case channel.FormatOpenAIChat, channel.FormatGemini:
		o.protocolError = errorObject
	case channel.FormatAnthropic:
		o.protocolError = envelope.Type == "error" && errorObject
	case channel.FormatOpenAIResponses:
		// A Responses error event carries code/message directly; response.failed
		// carries the failed Response under response. Neither requires us to
		// retain that arbitrary content. A failed tool item alone is not a
		// failed response.
		o.protocolError = envelope.Type == "error" || envelope.Type == "response.failed" || envelope.Object == "response" && envelope.Status == "failed"
	}
}
