package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

const (
	probeResponseLimit = int64(1 << 20)
	probeErrorLimit    = 4096
)

func (s *Service) probe(ctx context.Context, target ValidationTarget, credential string) ValidationAttempt {
	started := time.Now()
	probeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := probeRequestBody(target.Protocol, target.UpstreamModelID)
	if err != nil {
		return failedProbeAttemptWithDuration(target.Attempt, ErrorConfiguration, err.Error(), time.Since(started), credential)
	}
	endpoint, err := s.outbound.Endpoint(target.NormalizedBaseURL, target.Protocol, target.UpstreamModelID, false)
	if err != nil {
		return failedProbeAttemptWithDuration(target.Attempt, ErrorConfiguration, err.Error(), time.Since(started), credential)
	}
	client, err := s.outbound.ClientFor(probeContext, target.NormalizedBaseURL, 0)
	if err != nil {
		return failedProbeAttemptWithDuration(target.Attempt, ErrorConfiguration, err.Error(), time.Since(started), credential)
	}
	request, err := http.NewRequestWithContext(probeContext, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return failedProbeAttemptWithDuration(target.Attempt, ErrorConfiguration, err.Error(), time.Since(started), credential)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	ApplyAuthentication(request, target.Protocol, credential)
	response, err := client.Do(request)
	if err != nil {
		category := ErrorTransport
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			category = ErrorTimeout
		}
		return failedProbeAttemptWithDuration(target.Attempt, category, err.Error(), time.Since(started), credential)
	}
	defer response.Body.Close()
	target.Attempt.HTTPStatus = response.StatusCode
	responseBytes, tooLarge, err := readLimited(response.Body, probeResponseLimit)
	if failed, terminal := probeHTTPFailure(target.Attempt, response.StatusCode, response.Status, responseBytes, tooLarge, err, credential, time.Since(started)); terminal {
		return failed
	}
	if err := validateProbeResponse(target.Protocol, responseBytes); err != nil {
		return failedProbeAttemptWithDuration(target.Attempt, ErrorInvalid, err.Error(), time.Since(started), credential)
	}
	completed := time.Now().UTC()
	target.Attempt.Status = ValidationPassed
	target.Attempt.Duration = time.Since(started)
	target.Attempt.CompletedAt = &completed
	return target.Attempt
}

func probeHTTPFailure(attempt ValidationAttempt, statusCode int, status string, body []byte, tooLarge bool, readErr error, credential string, duration time.Duration) (ValidationAttempt, bool) {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		raw := string(body)
		if raw == "" {
			raw = status
		}
		raw = protectProbeError(raw, credential)
		return failedAttemptWithDuration(attempt, ErrorAuth, raw, duration), true
	}
	if readErr != nil {
		return failedProbeAttemptWithDuration(attempt, ErrorTransport, readErr.Error(), duration, credential), true
	}
	if tooLarge {
		return failedAttemptWithDuration(attempt, ErrorTooLarge, "upstream response exceeded 1048576 bytes", duration), true
	}
	if statusCode < 200 || statusCode >= 300 {
		raw := string(body)
		if raw == "" {
			raw = status
		}
		raw = protectProbeError(raw, credential)
		return failedAttemptWithDuration(attempt, ErrorUpstream, raw, duration), true
	}
	return ValidationAttempt{}, false
}

func protectProbeError(raw, credential string) string {
	if secretguard.ContainsExactOrJSONEscaped(raw, credential) || secretguard.ContainsExactInJSON([]byte(raw), credential) {
		return secretguard.CredentialErrorMessage
	}
	return raw
}

func failedProbeAttemptWithDuration(attempt ValidationAttempt, category ErrorCategory, raw string, duration time.Duration, credential string) ValidationAttempt {
	return failedAttemptWithDuration(attempt, category, protectProbeError(raw, credential), duration)
}

func probeRequestBody(protocol Protocol, model string) ([]byte, error) {
	var payload any
	switch protocol {
	case ProtocolOpenAIChat:
		payload = map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 1, "stream": false}
	case ProtocolOpenAIResponse:
		payload = map[string]any{"model": model, "input": "ping", "max_output_tokens": 1, "stream": false}
	case ProtocolAnthropic:
		payload = map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "stream": false}
	case ProtocolGemini:
		payload = map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []map[string]string{{"text": "ping"}}}},
			"generationConfig": map[string]int{"maxOutputTokens": 1},
		}
	default:
		return nil, ErrInvalidInput
	}
	return json.Marshal(payload)
}

func readLimited(reader io.Reader, limit int64) ([]byte, bool, error) {
	value, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return value, false, err
	}
	if int64(len(value)) > limit {
		return value[:limit], true, nil
	}
	return value, false, nil
}

func validateProbeResponse(protocol Protocol, body []byte) error {
	switch protocol {
	case ProtocolOpenAIChat:
		var response struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
			return errors.New("upstream response has no choices")
		}
		for _, choice := range response.Choices {
			if strings.TrimSpace(choice.FinishReason) != "" {
				return nil
			}
		}
		return errors.New("upstream response has no finish reason")
	case ProtocolOpenAIResponse:
		var response struct {
			Status string            `json:"status"`
			Output []json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.Status != "completed" || response.Output == nil {
			return errors.New("upstream response is not completed output")
		}
		return nil
	case ProtocolAnthropic:
		var response struct {
			Type       string            `json:"type"`
			Content    []json.RawMessage `json:"content"`
			StopReason string            `json:"stop_reason"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.Type != "message" || response.Content == nil || strings.TrimSpace(response.StopReason) == "" {
			return errors.New("upstream response is not a completed message")
		}
		return nil
	case ProtocolGemini:
		var response struct {
			Candidates []struct {
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(body, &response); err != nil || len(response.Candidates) == 0 {
			return errors.New("upstream response has no candidates")
		}
		for _, candidate := range response.Candidates {
			if strings.TrimSpace(candidate.FinishReason) != "" {
				return nil
			}
		}
		return errors.New("upstream response has no finish reason")
	default:
		return ErrInvalidInput
	}
}

func failedAttempt(attempt ValidationAttempt, category ErrorCategory, raw string) ValidationAttempt {
	return failedAttemptWithDuration(attempt, category, raw, time.Since(attempt.StartedAt))
}

func failedAttemptWithDuration(attempt ValidationAttempt, category ErrorCategory, raw string, duration time.Duration) ValidationAttempt {
	completed := time.Now().UTC()
	if duration < 0 {
		duration = 0
	}
	raw, truncated := normalizeRawError(raw)
	attempt.Status = ValidationFailed
	attempt.ErrorCategory = category
	attempt.RawError = raw
	attempt.RawErrorTruncated = truncated
	attempt.Duration = duration
	attempt.CompletedAt = &completed
	return attempt
}

func normalizeRawError(raw string) (string, bool) {
	normalized := strings.ToValidUTF8(strings.ReplaceAll(raw, "\x00", "�"), "�")
	changed := normalized != raw
	if len(normalized) <= probeErrorLimit {
		return normalized, changed
	}
	cutoff := probeErrorLimit
	for cutoff > 0 && !utf8.ValidString(normalized[:cutoff]) {
		cutoff--
	}
	return normalized[:cutoff], true
}

func validationErrorMessage(attempt ValidationAttempt) string {
	if attempt.Status == ValidationPassed {
		return ""
	}
	return fmt.Sprintf("%s: %s", attempt.ErrorCategory, attempt.RawError)
}
