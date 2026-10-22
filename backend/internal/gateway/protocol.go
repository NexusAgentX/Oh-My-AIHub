package gateway

import (
	"errors"
	"io"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func nativeProtocol(f channel.Format) string {
	switch f {
	case channel.FormatOpenAIChat:
		return "openai-chat-completions"
	case channel.FormatOpenAIResponses:
		return "openai-responses"
	case channel.FormatAnthropic:
		return "native-messages"
	case channel.FormatGemini:
		return "native-generate-content"
	case channel.FormatOpenAIImages:
		return "openai-images"
	}
	return ""
}

// isSSE reports whether an upstream answer is a server-sent event stream.
func isSSE(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

func stageResponseJSON(result *attemptResult) ([]byte, error) {
	payload := append([]byte(nil), result.first...)
	if len(payload) > maxRequestBytes {
		return nil, errors.New("response exceeds limit")
	}
	if result.firstErr != nil && !errors.Is(result.firstErr, io.EOF) {
		return nil, result.firstErr
	}
	if result.firstErr == nil {
		rest, err := io.ReadAll(io.LimitReader(result.response.Body, int64(maxRequestBytes-len(payload)+1)))
		if err != nil {
			return nil, err
		}
		payload = append(payload, rest...)
	}
	if len(payload) > maxRequestBytes {
		return nil, errors.New("response exceeds limit")
	}
	result.first = payload
	result.firstErr = io.EOF
	return payload, nil
}
