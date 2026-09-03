package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

type lastUpstreamFailure struct {
	status  int
	body    []byte
	header  http.Header
	code    string
	message string
}

type streamResult struct {
	committed bool
	succeeded bool
	code      string
	message   string
	err       error
}

func (s *Service) ServeProtocol(w http.ResponseWriter, r *http.Request, protocol channel.Protocol, canonicalFromPath string, streamFromPath bool) {
	if r.Method != http.MethodPost || !validProtocol(protocol) {
		writeProtocolError(w, protocol, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST", "")
		return
	}
	if err := validateProtocolQuery(protocol, streamFromPath, r.URL.RawQuery); err != nil {
		writeProtocolError(w, protocol, http.StatusBadRequest, "invalid_query", "请求查询参数无效", "")
		return
	}
	secret, err := extractPlatformCredential(r, protocol)
	if err != nil {
		writeProtocolError(w, protocol, http.StatusUnauthorized, "invalid_api_key", "API Key 无效", "")
		return
	}
	authenticated, err := s.Authenticate(r.Context(), secret)
	if err != nil {
		if errors.Is(err, ErrInvalidAPIKey) {
			writeProtocolError(w, protocol, http.StatusUnauthorized, "invalid_api_key", "API Key 无效", "")
			return
		}
		writeProtocolError(w, protocol, http.StatusServiceUnavailable, "gateway_unavailable", "网关暂不可用", "")
		return
	}
	body, err := readBounded(r.Body, MaxRequestBytes)
	if err != nil {
		writeProtocolError(w, protocol, http.StatusRequestEntityTooLarge, "request_too_large", "请求正文超过 32 MiB", "")
		return
	}
	if secretguard.ContainsExactOrJSONEscaped(string(body), secret) || secretguard.ContainsExactInJSON(body, secret) {
		writeProtocolError(w, protocol, http.StatusBadRequest, "credential_in_request_body", "请求正文不能包含当前 API Key", "")
		return
	}
	canonicalModelID, stream := canonicalFromPath, streamFromPath
	expectedChoices := 0
	if protocol != channel.ProtocolGemini {
		parsed, parseErr := ParseRequest(protocol, body)
		if parseErr != nil {
			writeProtocolError(w, protocol, http.StatusBadRequest, "invalid_request", "请求缺少有效的 canonical model", "")
			return
		}
		canonicalModelID, stream, expectedChoices = parsed.CanonicalModelID, parsed.Stream, parsed.ExpectedChoices
	} else if !validCanonicalModelID(canonicalModelID) {
		writeProtocolError(w, protocol, http.StatusBadRequest, "invalid_model_path", "模型路径无效", "")
		return
	}
	if err := validateV1BillableRequest(protocol, body); err != nil {
		writeProtocolError(w, protocol, http.StatusBadRequest, "unsupported_billing_shape", "首版仅支持单候选与客户端执行的函数工具", "")
		return
	}
	if protocol == channel.ProtocolAnthropic {
		if err := validateAnthropicHeaders(r.Header); err != nil {
			writeProtocolError(w, protocol, http.StatusBadRequest, "invalid_anthropic_headers", "anthropic-version 无效", "")
			return
		}
	}
	plan, err := s.BeginCall(r.Context(), authenticated, protocol, canonicalModelID)
	if err != nil {
		if errors.Is(err, ErrRejected) {
			status := http.StatusUnprocessableEntity
			if plan.Call.DecisionCode == "insufficient_spending_power" {
				status = http.StatusPaymentRequired
			}
			writeProtocolError(w, protocol, status, plan.Call.DecisionCode, "请求未通过调用前检查", plan.Call.ID)
			return
		}
		if errors.Is(err, ErrInvalidAPIKey) {
			writeProtocolError(w, protocol, http.StatusUnauthorized, "invalid_api_key", "API Key 已失效", "")
			return
		}
		writeProtocolError(w, protocol, http.StatusServiceUnavailable, "gateway_unavailable", "无法建立调用快照", "")
		return
	}
	publicCredentials := make([]string, 0, len(plan.Candidates)+1)
	publicCredentials = append(publicCredentials, secret)
	for _, candidate := range plan.Candidates {
		publicCredentials = append(publicCredentials, candidate.Lease.Credential)
	}
	publicCallID := safePublicCallID(plan.Call.ID, publicCredentials...)

	totalTimeout := NonStreamingTotalTimeout
	if stream {
		totalTimeout = StreamingTotalTimeout
	}
	callContext, cancelCall := context.WithTimeout(r.Context(), totalTimeout)
	defer cancelCall()
	stopHeartbeat := make(chan struct{})
	go s.heartbeatLoop(callContext, plan.Call.ID, plan.Call.LeaseGeneration, cancelCall, stopHeartbeat)
	defer close(stopHeartbeat)

	var lastFailure lastUpstreamFailure
	for candidateIndex, candidate := range plan.Candidates {
		attempt, err := s.StartAttempt(callContext, plan.Call.ID, candidate)
		if err != nil {
			s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法记录上游尝试")
			return
		}
		attemptStarted := time.Now()
		client, endpoint, err := s.outbound.ProxyTarget(callContext, candidate.Lease, stream, totalTimeout)
		if err != nil {
			code, message, _ := secretguard.ProtectUpstreamError("unsafe_upstream", err.Error(), publicCredentials...)
			if completeErr := s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, 0, false, attemptStarted, publicCredentials...); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		requestBody := body
		if protocol != channel.ProtocolGemini {
			requestBody, err = RewriteRequest(protocol, body, candidate.Lease.UpstreamModelID, stream)
			if err != nil {
				code, message, _ := secretguard.ProtectUpstreamError("request_rewrite_failed", err.Error(), publicCredentials...)
				_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, 0, false, attemptStarted, publicCredentials...)
				lastFailure = lastUpstreamFailure{code: code, message: message}
				continue
			}
		} else if stream {
			endpoint, err = withGeminiSSEQuery(endpoint)
			if err != nil {
				code, message, _ := secretguard.ProtectUpstreamError("request_rewrite_failed", err.Error(), publicCredentials...)
				_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, 0, false, attemptStarted, publicCredentials...)
				lastFailure = lastUpstreamFailure{code: code, message: message}
				continue
			}
		}
		foreignCredentials := make([]string, 0, len(plan.Candidates))
		foreignCredentials = append(foreignCredentials, secret)
		for otherIndex, other := range plan.Candidates {
			if otherIndex != candidateIndex {
				foreignCredentials = append(foreignCredentials, other.Lease.Credential)
			}
		}
		if secretguard.ContainsExactOrJSONEscaped(string(requestBody), foreignCredentials...) || secretguard.ContainsExactInJSON(requestBody, foreignCredentials...) {
			_ = s.completeFailedAttempt(
				r.Context(), attempt.ID, attempt.LeaseGeneration,
				"credential_in_request_body", "request body contained a credential belonging to the platform or another candidate",
				0, false, attemptStarted, publicCredentials...,
			)
			lastFailure = lastUpstreamFailure{code: "credential_in_request_body", message: "request body contained a protected credential"}
			continue
		}
		upstreamRequest, err := http.NewRequestWithContext(callContext, http.MethodPost, endpoint, bytes.NewReader(requestBody))
		if err != nil {
			code, message, _ := secretguard.ProtectUpstreamError("request_build_failed", err.Error(), publicCredentials...)
			_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, 0, false, attemptStarted, publicCredentials...)
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		upstreamRequest.GetBody = nil
		upstreamRequest.Close = true
		copyOutboundHeaders(upstreamRequest.Header, r.Header, protocol, stream)
		injectUpstreamAuthentication(upstreamRequest.Header, protocol, candidate.Lease.Credential)
		response, err := client.Do(upstreamRequest)
		if err != nil {
			status, code := AttemptFailed, "upstream_transport_error"
			terminalStatus := CallStatus("")
			terminalHTTPStatus := 0
			if r.Context().Err() != nil {
				status, code, terminalStatus = AttemptCancelled, "client_cancelled", CallCancelled
			} else if errors.Is(callContext.Err(), context.DeadlineExceeded) {
				status, code, terminalStatus, terminalHTTPStatus = AttemptFailed, "gateway_timeout", CallFailed, http.StatusGatewayTimeout
			} else if callContext.Err() != nil {
				status, code, terminalStatus, terminalHTTPStatus = AttemptIncomplete, "delivery_lease_lost", CallIncomplete, http.StatusServiceUnavailable
			}
			code, message, _ := secretguard.ProtectUpstreamError(code, err.Error(), publicCredentials...)
			_, completeErr := s.persistCompleteAttempt(r.Context(), attempt.ID, AttemptResult{
				LeaseGeneration: attempt.LeaseGeneration, Status: status, ErrorCode: code, RawError: message, Duration: time.Since(attemptStarted),
			})
			if completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			if terminalStatus != "" {
				_, _ = s.persistFinalize(r.Context(), plan.Call.ID, FinalizeOutcome{LeaseGeneration: plan.Call.LeaseGeneration, Status: terminalStatus, CompletionReason: code})
				if terminalHTTPStatus != 0 {
					writeProtocolError(w, protocol, terminalHTTPStatus, code, "网关调用未能继续", publicCallID)
				}
				return
			}
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		if !supportedContentEncoding(response.Header) {
			response.Body.Close()
			if completeErr := s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, "unsupported_content_encoding", "upstream content encoding must be identity", response.StatusCode, false, attemptStarted, publicCredentials...); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			lastFailure = lastUpstreamFailure{code: "unsupported_content_encoding", message: "upstream content encoding must be identity"}
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 && !validSuccessContentType(response.Header, stream) {
			response.Body.Close()
			if completeErr := s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, "invalid_content_type", "upstream success response has an invalid content type", response.StatusCode, false, attemptStarted, publicCredentials...); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			lastFailure = lastUpstreamFailure{code: "invalid_content_type", message: "upstream success response has an invalid content type"}
			continue
		}
		if responseHeaderContainsCredential(response.Header, publicCredentials...) {
			response.Body.Close()
			if completeErr := s.completeFailedAttempt(
				r.Context(), attempt.ID, attempt.LeaseGeneration,
				secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage,
				response.StatusCode, false, attemptStarted, publicCredentials...,
			); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			lastFailure = lastUpstreamFailure{code: secretguard.CredentialErrorCode, message: secretguard.CredentialErrorMessage}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			rawError, readErr := readBounded(response.Body, MaxUpstreamErrorBytes)
			response.Body.Close()
			if readErr != nil {
				code, message, _ := secretguard.ProtectUpstreamError("upstream_error_too_large", readErr.Error(), publicCredentials...)
				_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, response.StatusCode, false, attemptStarted, publicCredentials...)
				lastFailure = lastUpstreamFailure{code: code, message: message}
				continue
			}
			code, rawMessage := UpstreamErrorDetails(protocol, rawError)
			code, rawMessage, credentialEcho := secretguard.ProtectUpstreamError(code, rawMessage, publicCredentials...)
			safeErrorBody, safeEnvelope, bodyCredentialEcho := normalizeUpstreamErrorBody(protocol, rawError, publicCredentials...)
			if bodyCredentialEcho {
				code, rawMessage, credentialEcho = secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, true
			}
			if completeErr := s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, rawMessage, response.StatusCode, false, attemptStarted, publicCredentials...); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			if credentialEcho || !safeEnvelope {
				rawError = nil
			} else {
				rawError = safeErrorBody
			}
			lastFailure = lastUpstreamFailure{status: response.StatusCode, body: rawError, header: sanitizedResponseHeaders(response.Header), code: code, message: rawMessage}
			continue
		}
		if stream {
			result := s.proxyStreamingAttempt(w, r, callContext, plan.Call.ID, publicCallID, protocol, canonicalModelID, expectedChoices, publicCredentials, candidate, attempt, response, attemptStarted)
			response.Body.Close()
			if result.committed || result.succeeded {
				return
			}
			failureCode, failureMessage, _ := secretguard.ProtectUpstreamError(
				coalesce(result.code, "invalid_stream"), coalesce(result.message, result.err.Error()), publicCredentials...,
			)
			lastFailure = lastUpstreamFailure{code: failureCode, message: failureMessage}
			continue
		}
		rawResponse, readErr := readBounded(response.Body, MaxNonStreamingBytes)
		response.Body.Close()
		if readErr != nil {
			code, message, _ := secretguard.ProtectUpstreamError("response_too_large", readErr.Error(), publicCredentials...)
			_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, response.StatusCode, false, attemptStarted, publicCredentials...)
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		if secretguard.ContainsExactOrJSONEscaped(string(rawResponse), publicCredentials...) || secretguard.ContainsExactInJSON(rawResponse, publicCredentials...) {
			_ = s.completeFailedAttempt(
				r.Context(), attempt.ID, attempt.LeaseGeneration,
				secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage,
				response.StatusCode, false, attemptStarted, publicCredentials...,
			)
			lastFailure = lastUpstreamFailure{code: secretguard.CredentialErrorCode, message: secretguard.CredentialErrorMessage}
			continue
		}
		rewritten, usage, rewriteErr := RewriteNonStreamingResponse(protocol, rawResponse, canonicalModelID, expectedChoices)
		if rewriteErr != nil {
			code, message := "invalid_upstream_response", rewriteErr.Error()
			var responseErr *UpstreamResponseError
			if errors.As(rewriteErr, &responseErr) {
				code, message = responseErr.Code, responseErr.Message
			} else if errors.Is(rewriteErr, ErrResponseTooBig) {
				code, message = "response_too_large", ErrResponseTooBig.Error()
			}
			code, message, _ = secretguard.ProtectUpstreamError(code, message, publicCredentials...)
			_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, response.StatusCode, false, attemptStarted, publicCredentials...)
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		if secretguard.ContainsExactOrJSONEscaped(string(rewritten), publicCredentials...) || secretguard.ContainsExactInJSON(rewritten, publicCredentials...) {
			_ = s.completeFailedAttempt(
				r.Context(), attempt.ID, attempt.LeaseGeneration,
				secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage,
				response.StatusCode, false, attemptStarted, publicCredentials...,
			)
			lastFailure = lastUpstreamFailure{code: secretguard.CredentialErrorCode, message: secretguard.CredentialErrorMessage}
			continue
		}
		duration := time.Since(attemptStarted)
		successResult := AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration, Status: AttemptSucceeded, HTTPStatus: response.StatusCode,
			Duration: duration, SemanticCommitted: false, Usage: usage,
		}
		if _, err := s.persistFinalize(r.Context(), plan.Call.ID, FinalizeOutcome{
			LeaseGeneration: plan.Call.LeaseGeneration, Status: CallSucceeded, CompletionReason: "completed", FinalOfferID: candidate.Lease.OfferID,
			HTTPStatus: response.StatusCode, Usage: usage, SuccessAttemptID: attempt.ID, SuccessAttempt: &successResult,
		}); err != nil {
			_ = s.abortUnsettledSuccess(r.Context(), plan.Call.ID, attempt.ID, candidate.Lease.OfferID, response.StatusCode, successResult, false, "settlement_failed", "结算未完成")
			writeProtocolError(w, protocol, http.StatusServiceUnavailable, "settlement_failed", "结算未完成", publicCallID)
			return
		}
		if err := s.persistHeartbeat(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration); err != nil {
			_, _ = s.persistCompensate(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration, "delivery_lease_lost")
			writeProtocolError(w, protocol, http.StatusServiceUnavailable, "delivery_lease_lost", "交付租约已失效", publicCallID)
			return
		}
		if r.Context().Err() != nil {
			_, _ = s.persistCompensate(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration, "client_cancelled")
			return
		}
		if callContext.Err() != nil {
			_, _ = s.persistCompensate(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration, "delivery_lease_lost")
			writeProtocolError(w, protocol, http.StatusServiceUnavailable, "delivery_lease_lost", "交付租约已失效", publicCallID)
			return
		}
		if err := writeSanitizedResponse(callContext, w, response.StatusCode, response.Header, rewritten); err != nil {
			_, _ = s.persistCompensate(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration, "downstream_write_failed")
			return
		}
		if _, err := s.persistConfirm(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration); err != nil {
			_, _ = s.persistCompensate(r.Context(), plan.Call.ID, plan.Call.LeaseGeneration, "delivery_confirmation_failed")
		}
		return
	}

	_, finalizeErr := s.persistFinalize(r.Context(), plan.Call.ID, FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration, Status: CallFailed,
		CompletionReason: coalesce(lastFailure.code, "all_candidates_failed"), HTTPStatus: lastFailure.status,
	})
	if finalizeErr != nil {
		writeProtocolError(w, protocol, http.StatusServiceUnavailable, "settlement_failed", "失败调用未能安全终结", publicCallID)
		return
	}
	if lastFailure.status >= 400 && len(lastFailure.body) > 0 {
		_ = writeSanitizedResponse(callContext, w, lastFailure.status, lastFailure.header, lastFailure.body)
		return
	}
	writeProtocolError(w, protocol, http.StatusBadGateway, coalesce(lastFailure.code, "all_candidates_failed"), "所有候选渠道均失败", publicCallID)
}

func normalizeUpstreamErrorBody(protocol channel.Protocol, raw []byte, credentials ...string) ([]byte, bool, bool) {
	if len(raw) == 0 {
		return nil, false, false
	}
	if secretguard.ContainsExactOrJSONEscaped(string(raw), credentials...) || secretguard.ContainsExactInJSON(raw, credentials...) {
		return nil, false, true
	}
	value, err := decodeJSONObject(raw)
	if err != nil || responseEnvelopeError(protocol, value) == nil {
		return nil, false, false
	}
	if protocol == channel.ProtocolAnthropic && stringField(value, "type") != "error" {
		return nil, false, false
	}
	encoded, err := marshalJSONObjectWithin(value, MaxUpstreamErrorBytes)
	if err != nil {
		return nil, false, false
	}
	if secretguard.ContainsExactOrJSONEscaped(string(encoded), credentials...) || secretguard.ContainsExactInJSON(encoded, credentials...) {
		return nil, false, true
	}
	return encoded, true, false
}

func ParseGeminiRequestPath(r *http.Request) (canonicalModelID string, stream bool, err error) {
	escaped := r.URL.EscapedPath()
	if escaped == "" {
		escaped = r.URL.Path
	}
	lower := strings.ToLower(escaped)
	if strings.Contains(lower, "%") || strings.Contains(escaped, "\\") || !strings.HasPrefix(escaped, "/v1beta/models/") {
		return "", false, ErrInvalidInput
	}
	remaining := strings.TrimPrefix(escaped, "/v1beta/models/")
	operation := ":generateContent"
	if strings.HasSuffix(remaining, ":streamGenerateContent") {
		operation, stream = ":streamGenerateContent", true
	} else if !strings.HasSuffix(remaining, operation) {
		return "", false, ErrInvalidInput
	}
	model := strings.TrimSuffix(remaining, operation)
	if strings.Contains(model, ":") || !validCanonicalModelID(model) {
		return "", false, ErrInvalidInput
	}
	if err := validateProtocolQuery(channel.ProtocolGemini, stream, r.URL.RawQuery); err != nil {
		return "", false, err
	}
	return model, stream, nil
}

type bufferedStreamFrame struct {
	data     []byte
	semantic bool
}

type streamingCredentialGuard struct {
	credentials []string
	tails       map[string]string
	maxTail     int
}

func newStreamingCredentialGuard(credentials ...string) *streamingCredentialGuard {
	filtered := make([]string, 0, len(credentials))
	maximum := 0
	for _, credential := range credentials {
		if credential == "" {
			continue
		}
		filtered = append(filtered, credential)
		maximum = max(maximum, len(credential)-1)
	}
	return &streamingCredentialGuard{credentials: filtered, tails: make(map[string]string), maxTail: maximum}
}

func (g *streamingCredentialGuard) containsFrame(frame []byte) bool {
	return secretguard.ContainsExactOrJSONEscaped(string(frame), g.credentials...)
}

func (g *streamingCredentialGuard) containsFragments(fragments []SSECredentialFragment) (bool, error) {
	for _, fragment := range fragments {
		if _, exists := g.tails[fragment.StreamKey]; !exists && len(g.tails) >= MaxSSECredentialStreams {
			return false, ErrResponseTooBig
		}
		combined := g.tails[fragment.StreamKey] + fragment.Text
		if secretguard.ContainsExact(combined, g.credentials...) {
			return true, nil
		}
		if g.maxTail <= 0 {
			continue
		}
		if len(combined) > g.maxTail {
			combined = combined[len(combined)-g.maxTail:]
		}
		g.tails[fragment.StreamKey] = combined
	}
	return false, nil
}

func (s *Service) proxyStreamingAttempt(w http.ResponseWriter, r *http.Request, callContext context.Context, callID, publicCallID string, protocol channel.Protocol, canonicalModelID string, expectedChoices int, credentials []string, candidate Candidate, attempt Attempt, response *http.Response, attemptStarted time.Time) streamResult {
	reader := bufio.NewReaderSize(response.Body, 64*1024)
	precommit := make([]bufferedStreamFrame, 0)
	terminal := make([]bufferedStreamFrame, 0)
	precommitBytes, terminalBytes, streamBytes := 0, 0, 0
	terminalStarted, finishSeen := false, false
	observedChoices := make(map[int]struct{})
	finishedChoices := make(map[int]struct{})
	downstreamStarted, semanticDelivered := false, false
	ttft := time.Duration(0)
	observation := UsageObservation{}
	credentialGuard := newStreamingCredentialGuard(credentials...)
	pendingDelivery := false
	maxTerminalFrames := MaxTerminalFrames
	if protocol == channel.ProtocolOpenAIChat && expectedChoices+2 > maxTerminalFrames {
		maxTerminalFrames = expectedChoices + 2
	}
	startDownstream := func() {
		if downstreamStarted {
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(StreamingIdleTimeout))
		copyHeader(w.Header(), sanitizedResponseHeaders(response.Header))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.WriteHeader(response.StatusCode)
		downstreamStarted = true
	}
	writeFrames := func(frames []bufferedStreamFrame) error {
		for _, frame := range frames {
			if err := callContext.Err(); err != nil {
				return err
			}
			if pendingDelivery {
				if err := s.persistHeartbeat(r.Context(), callID, attempt.LeaseGeneration); err != nil {
					return err
				}
			}
			startDownstream()
			if err := writeStreamFrame(w, frame.data); err != nil {
				return err
			}
			if frame.semantic && !semanticDelivered {
				semanticDelivered = true
				if ttft == 0 {
					ttft = time.Since(attemptStarted)
				}
				if err := s.persistMarkAttemptCommitted(r.Context(), attempt.ID, AttemptCommitObservation{
					LeaseGeneration: attempt.LeaseGeneration,
					TTFT:            ttft,
					Duration:        time.Since(attemptStarted),
					MeasureTPS:      pendingDelivery,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	fail := func(code, message string, cause error) streamResult {
		_ = response.Body.Close()
		code, message, _ = secretguard.ProtectUpstreamError(code, message, credentials...)
		attemptStatus, callStatus := chooseAttemptStatus(downstreamStarted), CallIncomplete
		if r.Context().Err() != nil {
			attemptStatus, callStatus, code, message = AttemptCancelled, CallCancelled, "client_cancelled", "client cancelled the request"
		} else if !downstreamStarted {
			callStatus = CallFailed
		}
		_, _ = s.persistCompleteAttempt(r.Context(), attempt.ID, AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration, Status: attemptStatus, HTTPStatus: response.StatusCode,
			ErrorCode: code, RawError: message, SemanticCommitted: semanticDelivered,
			TTFTObserved: semanticDelivered, TTFT: ttft, Duration: time.Since(attemptStarted),
		})
		if downstreamStarted || attemptStatus == AttemptCancelled {
			_, _ = s.persistFinalize(r.Context(), callID, FinalizeOutcome{
				LeaseGeneration: attempt.LeaseGeneration, Status: callStatus, CompletionReason: code,
				FinalOfferID: candidate.Lease.OfferID, HTTPStatus: response.StatusCode,
			})
		}
		if cause == nil {
			cause = errors.New(message)
		}
		return streamResult{committed: downstreamStarted, code: code, message: message, err: cause}
	}
	for {
		deadline := StreamingIdleTimeout
		if !downstreamStarted {
			remaining := PrecommitTimeout - time.Since(attemptStarted)
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			deadline = min(deadline, remaining)
		}
		frame, readErr := readSSEFrameWithTimeout(reader, response.Body, deadline)
		if readErr != nil {
			return fail("stream_incomplete", readErr.Error(), readErr)
		}
		streamBytes += len(frame)
		if streamBytes > MaxStreamingBytes {
			return fail("stream_too_large", ErrResponseTooBig.Error(), ErrResponseTooBig)
		}
		if credentialGuard.containsFrame(frame) {
			result := fail(secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, errors.New(secretguard.CredentialErrorMessage))
			if downstreamStarted {
				_ = writeStreamFrame(w, protocolSSEErrorFrame(protocol, http.StatusBadGateway, secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, publicCallID))
			}
			return result
		}
		analysis, analyzeErr := AnalyzeSSEFrame(protocol, frame, canonicalModelID)
		if analyzeErr != nil {
			code, message := "invalid_sse_event", analyzeErr.Error()
			var responseErr *UpstreamResponseError
			if errors.As(analyzeErr, &responseErr) {
				code, message = responseErr.Code, responseErr.Message
			}
			if errors.Is(analyzeErr, ErrResponseTooBig) {
				code, message = "stream_too_large", ErrResponseTooBig.Error()
			}
			return fail(code, message, analyzeErr)
		}
		if len(analysis.Frame) == 0 {
			continue
		}
		if sseFrameContainsDecodedCredential(analysis.Frame, credentials...) {
			result := fail(secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, errors.New(secretguard.CredentialErrorMessage))
			if downstreamStarted {
				_ = writeStreamFrame(w, protocolSSEErrorFrame(protocol, http.StatusBadGateway, secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, publicCallID))
			}
			return result
		}
		if protocol == channel.ProtocolOpenAIChat {
			if progressErr := validateChatChoiceProgress(expectedChoices, analysis.ChoiceIndexes); progressErr != nil {
				return fail("invalid_choice_index", progressErr.Error(), progressErr)
			}
		}
		credentialEcho, credentialErr := credentialGuard.containsFragments(analysis.CredentialFragments)
		if credentialErr != nil {
			return fail("stream_resource_limit", credentialErr.Error(), credentialErr)
		}
		if credentialEcho {
			result := fail(secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, errors.New(secretguard.CredentialErrorMessage))
			if downstreamStarted {
				_ = writeStreamFrame(w, protocolSSEErrorFrame(protocol, http.StatusBadGateway, secretguard.CredentialErrorCode, secretguard.CredentialErrorMessage, publicCallID))
			}
			return result
		}
		if analysis.ErrorCode != "" {
			code, message, credentialEcho := secretguard.ProtectUpstreamError(
				analysis.ErrorCode, analysis.ErrorMessage, credentials...,
			)
			result := fail(code, message, &UpstreamResponseError{Code: code, Message: message})
			if downstreamStarted {
				frame := analysis.Frame
				if credentialEcho {
					frame = protocolSSEErrorFrame(protocol, http.StatusBadGateway, code, message, publicCallID)
				}
				_ = writeStreamFrame(w, frame)
			}
			return result
		}
		if terminalStarted && !analysis.Terminal {
			if !analysis.AfterTerminalAllowed {
				return fail("unexpected_event_after_terminal", "upstream sent data after a terminal event", ErrInvalidInput)
			}
			terminalBytes += len(analysis.Frame)
			if len(terminal) >= maxTerminalFrames || terminalBytes > MaxTerminalBytes {
				return fail("terminal_flood", ErrResponseTooBig.Error(), ErrResponseTooBig)
			}
			terminal = append(terminal, bufferedStreamFrame{data: analysis.Frame, semantic: analysis.Semantic})
			continue
		}
		finishSeen = finishSeen || analysis.FinishObserved
		for _, index := range analysis.ChoiceIndexes {
			observedChoices[index] = struct{}{}
		}
		for _, index := range analysis.FinishedChoiceIndexes {
			finishedChoices[index] = struct{}{}
		}
		observation.Merge(analysis.Observation)
		if analysis.Terminal {
			terminalStarted = true
			terminalBytes += len(analysis.Frame)
			if len(terminal) >= maxTerminalFrames || terminalBytes > MaxTerminalBytes {
				return fail("terminal_flood", ErrResponseTooBig.Error(), ErrResponseTooBig)
			}
			terminal = append(terminal, bufferedStreamFrame{data: analysis.Frame, semantic: analysis.Semantic})
			if !analysis.StreamEnd {
				continue
			}
			_ = response.Body.Close()
			if protocol == channel.ProtocolOpenAIChat {
				if !completeChatChoices(expectedChoices, observedChoices, finishedChoices) {
					return fail("missing_success_terminal", "upstream stream ended before every choice completed", ErrInvalidInput)
				}
			} else if !finishSeen {
				return fail("missing_success_terminal", "upstream stream ended without a protocol success terminal", ErrInvalidInput)
			}
			usageObservation := observation
			if protocol == channel.ProtocolOpenAIResponse || protocol == channel.ProtocolGemini {
				usageObservation = analysis.Observation
			}
			usage, complete := usageObservation.Complete()
			if !complete {
				return fail("missing_terminal_usage", ErrNoUsage.Error(), ErrNoUsage)
			}
			successResult := AttemptResult{
				LeaseGeneration: attempt.LeaseGeneration, Status: AttemptSucceeded,
				HTTPStatus: response.StatusCode, SemanticCommitted: semanticDelivered,
				MeasureTPS: semanticDelivered, TTFTObserved: semanticDelivered, TTFT: ttft,
				Duration: time.Since(attemptStarted), Usage: usage,
			}
			if _, err := s.persistFinalize(r.Context(), callID, FinalizeOutcome{
				LeaseGeneration: attempt.LeaseGeneration, Status: CallSucceeded,
				CompletionReason: "completed", FinalOfferID: candidate.Lease.OfferID,
				HTTPStatus: response.StatusCode, Usage: usage,
				SuccessAttemptID: attempt.ID, SuccessAttempt: &successResult,
			}); err != nil {
				_ = s.abortUnsettledSuccess(r.Context(), callID, attempt.ID, candidate.Lease.OfferID, response.StatusCode, successResult, downstreamStarted, "settlement_failed", "结算未完成")
				if !downstreamStarted {
					writeProtocolError(w, protocol, http.StatusServiceUnavailable, "settlement_failed", "结算未完成", publicCallID)
				}
				return streamResult{committed: true, code: "settlement_failed", message: err.Error(), err: err}
			}
			pendingDelivery = true
			deliveryErr := writeFrames(precommit)
			if deliveryErr == nil {
				deliveryErr = writeFrames(terminal)
			}
			if deliveryErr != nil {
				_, _ = s.persistCompensate(r.Context(), callID, attempt.LeaseGeneration, "downstream_write_failed")
				return streamResult{committed: downstreamStarted, code: "downstream_write_failed", message: deliveryErr.Error(), err: deliveryErr}
			}
			if _, err := s.persistConfirm(r.Context(), callID, attempt.LeaseGeneration); err != nil {
				_, _ = s.persistCompensate(r.Context(), callID, attempt.LeaseGeneration, "delivery_confirmation_failed")
				return streamResult{committed: downstreamStarted, code: "delivery_confirmation_failed", message: err.Error(), err: err}
			}
			return streamResult{committed: downstreamStarted, succeeded: true}
		}
		buffered := bufferedStreamFrame{data: analysis.Frame, semantic: analysis.Semantic}
		if !downstreamStarted {
			precommitBytes += len(analysis.Frame)
			if precommitBytes > MaxPrecommitBytes {
				return fail("precommit_buffer_too_large", ErrResponseTooBig.Error(), ErrResponseTooBig)
			}
			precommit = append(precommit, buffered)
			if !analysis.Semantic {
				continue
			}
			if err := writeFrames(precommit); err != nil {
				return fail("commit_marker_or_write_failed", err.Error(), err)
			}
			precommit = nil
			continue
		}
		if err := writeFrames([]bufferedStreamFrame{buffered}); err != nil {
			return fail("downstream_write_failed", err.Error(), err)
		}
	}
}

func validateChatChoiceProgress(expected int, indexes []int) error {
	if expected <= 0 {
		return ErrInvalidInput
	}
	seen := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= expected {
			return ErrInvalidInput
		}
		if _, duplicate := seen[index]; duplicate {
			return ErrInvalidInput
		}
		seen[index] = struct{}{}
	}
	return nil
}

func sseFrameContainsDecodedCredential(frame []byte, credentials ...string) bool {
	data, _, ok, err := splitSSEData(frame)
	if err != nil || !ok || strings.TrimSpace(string(data)) == "[DONE]" {
		return false
	}
	return secretguard.ContainsExactInJSON(data, credentials...)
}

func readSSEFrameWithTimeout(reader *bufio.Reader, body io.Closer, timeout time.Duration) ([]byte, error) {
	type result struct {
		frame []byte
		err   error
	}
	resultChannel := make(chan result, 1)
	go func() {
		frame, err := readSSEFrame(reader)
		resultChannel <- result{frame: frame, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-resultChannel:
		return value.frame, value.err
	case <-timer.C:
		_ = body.Close()
		return nil, context.DeadlineExceeded
	}
}

func readSSEFrame(reader *bufio.Reader) ([]byte, error) {
	buffer := bytes.Buffer{}
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if buffer.Len() > MaxSSEEventBytes-len(fragment) {
				return nil, ErrResponseTooBig
			}
			buffer.Write(fragment)
			if err == nil && len(bytes.TrimSpace(fragment)) == 0 {
				return buffer.Bytes(), nil
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if buffer.Len() > 0 {
				return buffer.Bytes(), nil
			}
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

func (s *Service) heartbeatLoop(ctx context.Context, callID string, leaseGeneration int64, cancelCall context.CancelFunc, stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			heartbeatContext, cancelPersistence := persistenceContext(ctx)
			err := s.Heartbeat(heartbeatContext, callID, leaseGeneration)
			cancelPersistence()
			if err != nil {
				cancelCall()
				return
			}
		}
	}
}

func (s *Service) completeFailedAttempt(ctx context.Context, attemptID string, leaseGeneration int64, code, raw string, status int, committed bool, started time.Time, credentials ...string) error {
	code, raw, _ = secretguard.ProtectUpstreamError(code, raw, credentials...)
	_, err := s.persistCompleteAttempt(ctx, attemptID, AttemptResult{
		LeaseGeneration: leaseGeneration, Status: AttemptFailed, HTTPStatus: status, ErrorCode: code, RawError: raw,
		SemanticCommitted: committed, Duration: time.Since(started),
	})
	return err
}

func (s *Service) finalizePlatformFailure(ctx context.Context, callID, publicCallID string, leaseGeneration int64, protocol channel.Protocol, w http.ResponseWriter, code, message string) {
	_, _ = s.persistFinalize(ctx, callID, FinalizeOutcome{LeaseGeneration: leaseGeneration, Status: CallIncomplete, CompletionReason: code})
	writeProtocolError(w, protocol, http.StatusServiceUnavailable, code, message, publicCallID)
}

func safePublicCallID(callID string, credentials ...string) string {
	if secretguard.ContainsExactOrJSONEscaped(callID, credentials...) {
		return ""
	}
	return callID
}

func (s *Service) abortUnsettledSuccess(ctx context.Context, callID, attemptID, offerID string, httpStatus int, success AttemptResult, clientCommitted bool, code, message string) error {
	if _, err := s.persistCompleteAttempt(ctx, attemptID, AttemptResult{
		LeaseGeneration: success.LeaseGeneration, Status: AttemptIncomplete, HTTPStatus: httpStatus, ErrorCode: code, RawError: message,
		SemanticCommitted: clientCommitted, TTFTObserved: success.TTFTObserved, TTFT: success.TTFT, Duration: success.Duration,
	}); err != nil {
		return err
	}
	_, err := s.persistFinalize(ctx, callID, FinalizeOutcome{
		LeaseGeneration: success.LeaseGeneration, Status: CallIncomplete, CompletionReason: code, FinalOfferID: offerID, HTTPStatus: httpStatus,
	})
	return err
}

func persistenceContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), PersistenceTimeout)
}

func (s *Service) persistCompleteAttempt(parent context.Context, attemptID string, result AttemptResult) (Attempt, error) {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.CompleteAttempt(ctx, attemptID, result)
}

func (s *Service) persistMarkAttemptCommitted(parent context.Context, attemptID string, observation AttemptCommitObservation) error {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.MarkAttemptCommitted(ctx, attemptID, observation)
}

func (s *Service) persistHeartbeat(parent context.Context, callID string, leaseGeneration int64) error {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.Heartbeat(ctx, callID, leaseGeneration)
}

func (s *Service) persistFinalize(parent context.Context, callID string, outcome FinalizeOutcome) (Call, error) {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.Finalize(ctx, callID, outcome)
}

func (s *Service) persistConfirm(parent context.Context, callID string, leaseGeneration int64) (Call, error) {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.ConfirmDelivery(ctx, callID, leaseGeneration)
}

func (s *Service) persistCompensate(parent context.Context, callID string, leaseGeneration int64, reason string) (Call, error) {
	ctx, cancel := persistenceContext(parent)
	defer cancel()
	return s.CompensateDelivery(ctx, callID, leaseGeneration, reason)
}

func extractPlatformCredential(r *http.Request, protocol channel.Protocol) (string, error) {
	headerNames := []string{"Authorization", "x-api-key", "x-goog-api-key"}
	present := make([]string, 0, 3)
	for _, name := range headerNames {
		if len(r.Header.Values(name)) > 0 {
			present = append(present, name)
		}
	}
	if len(present) != 1 {
		return "", ErrInvalidAPIKey
	}
	expected := "Authorization"
	if protocol == channel.ProtocolAnthropic {
		expected = "x-api-key"
	} else if protocol == channel.ProtocolGemini {
		expected = "x-goog-api-key"
	}
	if !strings.EqualFold(present[0], expected) || len(r.Header.Values(expected)) != 1 {
		return "", ErrInvalidAPIKey
	}
	value := r.Header.Get(expected)
	if expected == "Authorization" {
		if !strings.HasPrefix(value, "Bearer ") || strings.Contains(value[len("Bearer "):], " ") {
			return "", ErrInvalidAPIKey
		}
		value = strings.TrimPrefix(value, "Bearer ")
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", ErrInvalidAPIKey
	}
	return value, nil
}

func validateAnthropicHeaders(header http.Header) error {
	versions := header.Values("anthropic-version")
	if len(versions) != 1 || versions[0] != "2023-06-01" {
		return ErrInvalidInput
	}
	betas := header.Values("anthropic-beta")
	if len(betas) != 0 {
		return ErrInvalidInput
	}
	return nil
}

func safeHeaderValue(value string, maxLength int) bool {
	if strings.TrimSpace(value) == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return false
		}
	}
	return true
}

func copyOutboundHeaders(target, source http.Header, protocol channel.Protocol, stream bool) {
	target.Set("Content-Type", "application/json")
	target.Set("Accept-Encoding", "identity")
	if stream {
		target.Set("Accept", "text/event-stream")
	} else {
		target.Set("Accept", "application/json")
	}
	if protocol == channel.ProtocolAnthropic {
		target.Set("anthropic-version", "2023-06-01")
	}
}

func injectUpstreamAuthentication(header http.Header, protocol channel.Protocol, credential string) {
	header.Del("Authorization")
	header.Del("x-api-key")
	header.Del("x-goog-api-key")
	switch protocol {
	case channel.ProtocolAnthropic:
		header.Set("x-api-key", credential)
	case channel.ProtocolGemini:
		header.Set("x-goog-api-key", credential)
	default:
		header.Set("Authorization", "Bearer "+credential)
	}
}

func sanitizedResponseHeaders(source http.Header) http.Header {
	result := make(http.Header)
	for _, name := range []string{
		"Request-Id", "X-Request-Id",
		"OpenAI-Request-Id", "Anthropic-Request-Id",
	} {
		values := source.Values(name)
		if len(values) != 1 || !safeHeaderValue(values[0], 256) {
			continue
		}
		result.Set(name, values[0])
	}
	return result
}

func responseHeaderContainsCredential(source http.Header, credentials ...string) bool {
	for _, values := range sanitizedResponseHeaders(source) {
		for _, value := range values {
			if secretguard.ContainsExactOrJSONEscaped(value, credentials...) {
				return true
			}
		}
	}
	return false
}

func validSuccessContentType(header http.Header, stream bool) bool {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil {
		return false
	}
	if stream {
		return strings.EqualFold(mediaType, "text/event-stream")
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")
}

func supportedContentEncoding(header http.Header) bool {
	values := header.Values("Content-Encoding")
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		for _, encoding := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(encoding); trimmed != "" && !strings.EqualFold(trimmed, "identity") {
				return false
			}
		}
	}
	return true
}

func validateProtocolQuery(protocol channel.Protocol, stream bool, rawQuery string) error {
	query, err := url.ParseQuery(rawQuery)
	if err != nil || query.Has("key") {
		return ErrInvalidInput
	}
	if protocol != channel.ProtocolGemini {
		if rawQuery != "" {
			return ErrInvalidInput
		}
		return nil
	}
	if !stream {
		if rawQuery == "" {
			return nil
		}
		return ErrInvalidInput
	}
	if rawQuery != "alt=sse" || len(query) != 1 || len(query["alt"]) != 1 || query.Get("alt") != "sse" {
		return ErrInvalidInput
	}
	return nil
}

func completeChatChoices(expected int, observed, finished map[int]struct{}) bool {
	if expected <= 0 || len(observed) != expected || len(finished) != expected {
		return false
	}
	for index := 0; index < expected; index++ {
		if _, ok := observed[index]; !ok {
			return false
		}
		if _, ok := finished[index]; !ok {
			return false
		}
	}
	return true
}

func protocolErrorPayload(protocol channel.Protocol, status int, code, message, callID string) any {
	switch protocol {
	case channel.ProtocolAnthropic:
		errorValue := map[string]any{"type": code, "message": message}
		if callID != "" {
			errorValue["call_id"] = callID
		}
		return map[string]any{"type": "error", "error": errorValue}
	case channel.ProtocolGemini:
		errorValue := map[string]any{"code": status, "message": message, "status": strings.ToUpper(code)}
		if callID != "" {
			errorValue["call_id"] = callID
		}
		return map[string]any{"error": errorValue}
	default:
		errorValue := map[string]any{"message": message, "type": code, "code": code}
		if callID != "" {
			errorValue["call_id"] = callID
		}
		return map[string]any{"error": errorValue}
	}
}

func writeSanitizedResponse(ctx context.Context, w http.ResponseWriter, status int, header http.Header, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(NonStreamingWriteTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	copyHeader(w.Header(), sanitizedResponseHeaders(header))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := ctx.Err(); err != nil {
		return err
	}
	w.WriteHeader(status)
	if written, err := w.Write(body); err != nil {
		return err
	} else if written != len(body) {
		return io.ErrShortWrite
	}
	return controller.Flush()
}

func writeStreamFrame(w http.ResponseWriter, frame []byte) error {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(StreamingIdleTimeout))
	if written, err := w.Write(frame); err != nil {
		return err
	} else if written != len(frame) {
		return io.ErrShortWrite
	}
	return controller.Flush()
}

func copyHeader(target, source http.Header) {
	for name, values := range source {
		if len(values) > 0 {
			target.Set(name, values[0])
		}
	}
}

func readBounded(body io.Reader, maximum int64) ([]byte, error) {
	encoded, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > maximum {
		return nil, ErrResponseTooBig
	}
	return encoded, nil
}

func withGeminiSSEQuery(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("alt", "sse")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func writeProtocolError(w http.ResponseWriter, protocol channel.Protocol, status int, code, message, callID string) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(ProtocolErrorWriteTimeout))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocolErrorPayload(protocol, status, code, message, callID))
}

func protocolSSEErrorFrame(protocol channel.Protocol, status int, code, message, callID string) []byte {
	payload, _ := json.Marshal(protocolErrorPayload(protocol, status, code, message, callID))
	if protocol == channel.ProtocolAnthropic {
		return append(append([]byte("event: error\ndata: "), payload...), []byte("\n\n")...)
	}
	return append(append([]byte("data: "), payload...), []byte("\n\n")...)
}

// WriteProtocolError exposes the SDK-compatible error envelope to the HTTP routing layer.
func WriteProtocolError(w http.ResponseWriter, protocol channel.Protocol, status int, code, message, callID string) {
	writeProtocolError(w, protocol, status, code, message, callID)
}

func chooseAttemptStatus(committed bool) AttemptStatus {
	if committed {
		return AttemptIncomplete
	}
	return AttemptFailed
}

func coalesce(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
