package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// A consumer's resource stream is an authorized read, never another inference
// or settlement. It uses the current matching credential; private background
// terminal observation continues separately on its frozen original snapshot.
func (e *Engine) serveOwnedResponseStream(w http.ResponseWriter, r *http.Request, ep gatewaycontract.Endpoint, c Candidate, key KeyAuth, location authLocation, policy channel.Outbound, defaults settings.Settings, pref routing.Pref, owned *resourcestate.Record, budget *int) error {
	ctx, stop := context.WithTimeoutCause(r.Context(), e.CallTimeLimit, errCallDeadline)
	defer stop()
	call := &callState{id: owned.CreationID, started: e.Now(), key: key, format: channel.FormatOpenAIResponses, modelID: owned.ModelID, stream: true, dialect: DialectOf(r), authLocation: location, auxiliaryEndpoint: &ep, resourceObserver: &resourceObserver{format: channel.FormatOpenAIResponses, store: true, responseID: owned.ID, expectedResponseID: owned.ID}}
	entries := c.Entrances
	if len(entries) == 0 {
		entries = []Entrance{{BaseURL: c.BaseURL}}
	}
	for _, entry := range entries {
		if *budget <= 0 {
			break
		}
		*budget--
		c.BaseURL = entry.BaseURL
		result := e.attempt(ctx, r, call, c, EffectiveLimits(c.Advanced, defaults), defaults, policy, nil, objectScan{}, location, resolveTimeouts(defaults, pref))
		if result.passthrough != nil {
			result.passthrough.write(w, "")
			return nil
		}
		if result.response == nil {
			if result.clientGone || result.expired || result.rateLimited {
				break
			}
			continue
		}
		defer result.response.Body.Close()
		defer result.cancel(nil)
		defer putChunk(result.chunk)
		if !result.watch.commit() {
			return context.Canceled
		}
		if !isResourceSSE(result.response.Header.Get("Content-Type")) {
			payload, err := stageResourceJSON(&result)
			if err != nil {
				return err
			}
			if err = e.registerResources(call, c, payload); err != nil {
				return err
			}
			for k, v := range ClientResponseHeaders(result.response.Header, result.response.Uncompressed) {
				w.Header()[k] = v
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(result.response.StatusCode)
			_, err = w.Write(payload)
			return err
		}
		for k, v := range ClientResponseHeaders(result.response.Header, result.response.Uncompressed) {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		// Delay headers until the first authorized event. If the metadata write
		// fails before output, the caller can still receive a meaningful 502.
		sent := false
		writeErr := error(nil)
		gate := resourceEventGate{observe: func(raw []byte) error { return e.registerResources(call, c, raw) }, send: func(raw []byte) {
			if writeErr != nil {
				return
			}
			if !sent {
				w.WriteHeader(result.response.StatusCode)
				sent = true
			}
			_ = http.NewResponseController(w).SetWriteDeadline(e.Now().Add(clientWriteLimit))
			_, writeErr = w.Write(raw)
			if writeErr == nil {
				err := http.NewResponseController(w).Flush()
				if !errors.Is(err, http.ErrNotSupported) {
					writeErr = err
				}
			}
		}}
		finishError := func(err error) error {
			if sent {
				return nil
			}
			return err
		}
		if err := gate.write(result.first); err != nil {
			return finishError(err)
		}
		readErr := result.firstErr
		for readErr == nil && writeErr == nil {
			var n int
			n, readErr = result.response.Body.Read(*result.chunk)
			if n > 0 {
				if result.idle != nil {
					result.idle.Reset(result.idleAfter)
				}
				if err := gate.write((*result.chunk)[:n]); err != nil {
					return finishError(err)
				}
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return finishError(readErr)
		}
		if !answerEnded(result.upstream, readErr) {
			return finishError(context.Canceled)
		}
		if err := gate.finish(); err != nil {
			return finishError(err)
		}
		return nil
	}
	return errors.New("owned response stream unavailable")
}
