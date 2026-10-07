package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

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
	adapter := adapterFor(protocol)
	if adapter.modelInBody() {
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
	adapter.defaultRequestHeaders(r.Header)
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
		if endpoint, err = mergeUpstreamQuery(endpoint, r.URL.RawQuery, protocol, stream); err != nil {
			code, message, _ := secretguard.ProtectUpstreamError("invalid_query", err.Error(), publicCredentials...)
			_ = s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, code, message, 0, false, attemptStarted, publicCredentials...)
			lastFailure = lastUpstreamFailure{code: code, message: message}
			continue
		}
		if adapter.modelInBody() {
			requestBody, err = RewriteRequest(protocol, body, candidate.Lease.UpstreamModelID, stream)
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
		// Foreign credentials are detected before the platform injects its own
		// authentication: candidates may legitimately share one upstream key, so
		// scanning afterwards would reject its own credential.
		if outboundRequestCarriesForeignCredential(upstreamRequest, foreignCredentials...) {
			_ = s.completeFailedAttempt(
				r.Context(), attempt.ID, attempt.LeaseGeneration,
				"credential_in_request_headers", "request carried a credential belonging to the platform or another candidate",
				0, false, attemptStarted, publicCredentials...,
			)
			lastFailure = lastUpstreamFailure{code: "credential_in_request_headers", message: "request carried a protected credential"}
			continue
		}
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
			// Go decodes gzip itself, so a residual encoding is one the gateway
			// cannot decode; forwarding those bytes would corrupt the response.
			response.Body.Close()
			if completeErr := s.completeFailedAttempt(r.Context(), attempt.ID, attempt.LeaseGeneration, "unsupported_content_encoding", "upstream content encoding cannot be decoded", response.StatusCode, false, attemptStarted, publicCredentials...); completeErr != nil {
				s.finalizePlatformFailure(r.Context(), plan.Call.ID, publicCallID, plan.Call.LeaseGeneration, protocol, w, "attempt_persistence_failed", "无法保存上游错误")
				return
			}
			lastFailure = lastUpstreamFailure{code: "unsupported_content_encoding", message: "upstream content encoding cannot be decoded"}
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
		rewritten, usage, rewriteErr := RewriteNonStreamingResponse(protocol, rawResponse, canonicalModelID)
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
		if usage == nil {
			// The upstream answered successfully but the platform cannot price it.
			// Deliver the body and settle the call as incomplete with no charge
			// (ADR-0014) instead of failing over and hiding a valid response.
			unsettled := AttemptResult{
				LeaseGeneration: attempt.LeaseGeneration, Status: AttemptIncomplete, HTTPStatus: response.StatusCode,
				Duration: time.Since(attemptStarted), ErrorCode: "missing_settlement_usage", RawError: ErrNoUsage.Error(),
			}
			if err := s.abortUnsettledSuccess(r.Context(), plan.Call.ID, attempt.ID, candidate.Lease.OfferID, response.StatusCode, unsettled, false, "missing_settlement_usage", ErrNoUsage.Error()); err != nil {
				writeProtocolError(w, protocol, http.StatusServiceUnavailable, "settlement_failed", "结算未完成", publicCallID)
				return
			}
			_ = writeSanitizedResponse(callContext, w, response.StatusCode, response.Header, rewritten)
			return
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

func chooseAttemptStatus(committed bool) AttemptStatus {
	if committed {
		return AttemptIncomplete
	}
	return AttemptFailed
}
