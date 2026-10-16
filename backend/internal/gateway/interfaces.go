package gateway

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"strings"
)

// Protocol formats describe the wire contract. Operations remain internal to
// the gateway: advanced operations declare their required capabilities separately.
type operation uint8

const (
	operationGenerate operation = iota
	operationCompact
	operationCount
	operationConversation
	operationSearch
)

func operationOf(path string) operation {
	path = strings.Replace(path, "/backend-api/codex/", "/v1/", 1)
	switch path {
	case "/v1/alpha/search":
		return operationSearch
	case "/v1/responses/compact":
		return operationCompact
	case "/v1/responses/input_tokens":
		return operationCount
	case "/v1/conversations":
		return operationConversation
	}
	return operationGenerate
}

func observationFormat(f channel.Format) channel.Format {
	if f == channel.FormatOpenAIImages {
		return channel.FormatOpenAIResponses
	}
	return f
}
func imageFormat(f channel.Format) bool { return f == channel.FormatOpenAIImages }

func (call *callState) observer(stream bool, contentType string) *Observer {
	o := NewObserver(call.format, stream, contentType)
	if call.operation == operationCompact {
		o.media = &mediaProjection{}
	}
	return o
}
