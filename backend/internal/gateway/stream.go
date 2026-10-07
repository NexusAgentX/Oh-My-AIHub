package gateway

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

type streamResult struct {
	committed bool
	succeeded bool
	code      string
	message   string
	err       error
}

func (s *Service) proxyStreamingAttempt(w http.ResponseWriter, r *http.Request, callContext context.Context, callID, publicCallID string, protocol channel.Protocol, canonicalModelID string, expectedChoices int, credentials []string, candidate Candidate, attempt Attempt, response *http.Response, attemptStarted time.Time) streamResult {
	reader := bufio.NewReaderSize(response.Body, 64*1024)
	precommit := make([]bufferedStreamFrame, 0)
	terminal := make([]bufferedStreamFrame, 0)
	precommitBytes, terminalBytes, streamBytes := 0, 0, 0
	terminalStarted := false
	usageInvalid := false
	terminalUsage := UsageObservation{}
	terminalUsageState := UsageAbsent
	downstreamStarted, semanticDelivered := false, false
	ttft := time.Duration(0)
	observation := UsageObservation{}
	credentialGuard := newStreamingCredentialGuard(credentials...)
	pendingDelivery := false
	maxTerminalFrames := adapterFor(protocol).maxTerminalFrames(expectedChoices)
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
	// Settle delivers buffered frames at EOF, with or without protocol end markers.
	settle := func() streamResult {
		usage, settleable := settlementUsage(observation, usageInvalid, terminalUsage, terminalUsageState)
		code := "missing_settlement_usage"
		if usageInvalid {
			code = "unpriceable_usage"
		}
		successResult := AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration, Status: AttemptSucceeded,
			HTTPStatus: response.StatusCode, SemanticCommitted: semanticDelivered,
			MeasureTPS: semanticDelivered, TTFTObserved: semanticDelivered, TTFT: ttft,
			Duration: time.Since(attemptStarted), Usage: usage,
		}
		if !settleable {
			// The upstream completed but the platform cannot price the call. The
			// body is still the client's answer, so deliver it and settle as
			// incomplete with no charge (ADR-0014) instead of hiding a valid
			// response; nothing was delivered yet still fails over.
			deliveryErr := writeFrames(precommit)
			if deliveryErr == nil {
				deliveryErr = writeFrames(terminal)
			}
			if deliveryErr != nil {
				return fail("downstream_write_failed", deliveryErr.Error(), deliveryErr)
			}
			return fail(code, ErrNoUsage.Error(), ErrNoUsage)
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
			if errors.Is(readErr, io.EOF) {
				if !downstreamStarted && len(precommit) == 0 && len(terminal) == 0 {
					// The upstream closed without any frames: an empty stream is a
					// failure the caller may retry on the next candidate.
					return fail("stream_incomplete", readErr.Error(), readErr)
				}
				// Normal EOF after real output: deliver it and settle instead of
				// discarding the response and failing over.
				_ = response.Body.Close()
				return settle()
			}
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
		// Usage validity is recorded before any branch that skips settlement, so
		// an unpriceable snapshot in the terminal zone cannot be masked by an
		// earlier valid one.
		if analysis.UsageState == UsageInvalid {
			usageInvalid = true
		}
		if !terminalStarted {
			if analysis.UsageState != UsageAbsent {
				// The latest record wins until the first terminal event: relays send
				// partial usage early and the authoritative snapshot last.
				terminalUsage, terminalUsageState = analysis.Observation, analysis.UsageState
			}
			observation.Merge(analysis.Observation)
		}
		if analysis.Terminal {
			terminalStarted = true
		}
		if terminalStarted {
			// Terminal wind-down: the first terminal event freezes settlement and
			// every later frame — including repeated terminal frames — is only
			// delivered. Frames stay bounded by count and byte budget.
			terminalBytes += len(analysis.Frame)
			if len(terminal) >= maxTerminalFrames || terminalBytes > MaxTerminalBytes {
				return fail("terminal_flood", ErrResponseTooBig.Error(), ErrResponseTooBig)
			}
			terminal = append(terminal, bufferedStreamFrame{data: analysis.Frame, semantic: analysis.Semantic})
			// Protocol end markers freeze usage, but do not truncate vendor
			// trailers. Read through EOF with the same limits and safety checks.
			continue
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

// settlementUsage returns the billable usage for a completed stream.
//
// The terminal frame is preferred because relays commonly report partial usage
// in earlier frames; the accumulated observation is the fallback. Any frame that
// reported unpriceable or contradictory usage — including one in the terminal
// zone — makes the whole attempt unsettleable.
func settlementUsage(accumulated UsageObservation, accumulatedInvalid bool, terminal UsageObservation, terminalState UsageState) (*ledger.UsageV1, bool) {
	if accumulatedInvalid || terminalState == UsageInvalid {
		return nil, false
	}
	if terminalState == UsageValid {
		if usage, complete := terminal.Complete(); complete {
			return usage, true
		}
	}
	if usage, complete := accumulated.Complete(); complete {
		return usage, true
	}
	return nil, false
}
