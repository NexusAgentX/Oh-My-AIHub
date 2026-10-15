package gateway

import (
	"encoding/json"
)

// Inspect only known envelope members. Unknown provider values become "other";
// no arbitrary strings from response content are persisted.
func (o *Observer) observeStop(raw []byte) {
	var value struct {
		Choices []struct {
			Reason string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			Reason string `json:"finishReason"`
		} `json:"candidates"`
		Stop  string `json:"stop_reason"`
		Delta struct {
			Stop string `json:"stop_reason"`
		} `json:"delta"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Object string `json:"object"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	reason := value.Stop
	if value.Delta.Stop != "" {
		reason = value.Delta.Stop
	}
	for _, c := range value.Choices {
		if c.Reason != "" {
			reason = c.Reason
			break
		}
	}
	for _, c := range value.Candidates {
		if c.Reason != "" {
			reason = c.Reason
			break
		}
	}
	if value.Type == "response.completed" || value.Object == "response" && value.Status == "completed" {
		reason = "stop"
	}
	if value.Type == "response.incomplete" || value.Object == "response" && value.Status == "incomplete" {
		reason = "incomplete"
	}
	if reason == "" {
		return
	}
	switch reason {
	case "stop", "end_turn", "stop_sequence", "STOP":
		o.stopReason = "stop"
	case "length", "max_tokens", "MAX_TOKENS":
		o.stopReason = "length"
	case "tool_calls", "function_call", "tool_use":
		o.stopReason = "tool_call"
	case "content_filter", "refusal", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		o.stopReason = "content_filter"
	case "incomplete":
		o.stopReason = "incomplete"
	default:
		o.stopReason = "other"
	}
}
