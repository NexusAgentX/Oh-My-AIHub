package gateway

import (
	"errors"
	"io"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
)

// The frozen task remains the producer chain for child identities. Reads never
// settle it or create another call; its worker remains the only finalizer.
func (e *Engine) forwardBackgroundResource(w http.ResponseWriter, r *http.Request, response *http.Response, key KeyAuth, metadata backgroundMetadata, job BackgroundJob, owned *resourcestate.Record, cancel bool) error {
	copyHeaders := func() {
		for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
			w.Header()[name] = values
		}
		w.Header().Del("Content-Length")
		w.Header().Set("X-AIHub-Request-Id", job.CallID)
	}
	if !isResourceSSE(response.Header.Get("Content-Type")) {
		payload, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes+1))
		if err != nil || len(payload) > maxRequestBytes {
			return errors.New("background resource response unavailable")
		}
		endpoint := "responses.get"
		if cancel {
			endpoint = "responses.cancel"
		}
		if err := e.observeAuxiliary(r.Context(), gatewaycontract.Endpoint{ID: endpoint}, metadata.Candidate, key, metadata.ModelID, job.CallID, "", payload, owned); err != nil {
			return err
		}
		copyHeaders()
		_ = http.NewResponseController(w).SetWriteDeadline(e.Now().Add(clientWriteLimit))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(payload)
		return nil
	}
	copyHeaders()
	call := &callState{id: job.CallID, key: key, modelID: metadata.ModelID, resourceObserver: &resourceObserver{format: channel.FormatOpenAIResponses, store: true, responseID: owned.ID, expectedResponseID: owned.ID}}
	sent := false
	var writeErr error
	gate := resourceEventGate{observe: func(raw []byte) error { return e.registerResources(call, metadata.Candidate, raw) }, send: func(raw []byte) {
		if writeErr != nil {
			return
		}
		if !sent {
			w.WriteHeader(response.StatusCode)
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
	buffer := getChunk()
	defer putChunk(buffer)
	for {
		n, err := response.Body.Read(*buffer)
		if n > 0 {
			if err := gate.write((*buffer)[:n]); err != nil {
				return finishError(err)
			}
		}
		if writeErr != nil {
			return nil
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return finishError(err)
			}
			if err := gate.finish(); err != nil {
				return finishError(err)
			}
			return nil
		}
	}
}
