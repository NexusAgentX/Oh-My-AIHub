package detection

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

// TrackResource accepts only a resource witnessed as created by this task. It
// stores IDs/cleanup metadata, never the response or any opaque content.
func (s *Session) TrackResource(ctx context.Context, producer, kind, nativeID, endpointID, path string) error {
	endpoint, ok := s.endpoints[endpointID]
	if !ok || endpoint.Method != "DELETE" || endpoint.Scope != "remote_resource" || !matchPath(endpoint.Path, path) || !slices.Contains([]string{"response", "conversation", "cache", "stored_chat"}, kind) || len(nativeID) == 0 || len(nativeID) > 256 || strings.ContainsAny(nativeID, "\x00\r\n") || !strings.Contains(path, url.PathEscape(nativeID)) {
		return ErrInvalid
	}
	attempts, err := s.store.Attempts(ctx, s.jobID, []string{producer})
	if err != nil {
		return err
	}
	createPaths := map[string]string{"response": "/v1/responses", "conversation": "/v1/conversations", "cache": "/v1beta/cachedContents", "stored_chat": "/v1/chat/completions"}
	if len(attempts) != 1 || attempts[0].Method != "POST" || attempts[0].Path != createPaths[kind] || !attempts[0].ResponseReceived || attempts[0].Status < 200 || attempts[0].Status >= 300 {
		return ErrInvalid
	}
	pending, err := s.store.Artifacts(ctx, s.jobID)
	if err != nil {
		return err
	}
	if len(pending) >= 32 {
		return ErrBusy
	}
	for _, a := range pending {
		if a.NativeID == nativeID && a.DeleteEndpoint == endpointID && a.DeletePath == path {
			return nil
		}
	}
	id, err := ids.NewUUID()
	if err != nil {
		return err
	}
	return s.store.AddArtifact(ctx, s.jobID, Artifact{ID: id, Kind: kind, NativeID: nativeID, DeleteEndpoint: endpointID, DeletePath: path, ProducerAttemptID: producer, Reason: "ok"})
}
func (s *Session) Cleanup(ctx context.Context) error {
	pending, err := s.store.Artifacts(ctx, s.jobID)
	if err != nil {
		return err
	}
	if len(pending) > 32 {
		return ErrInvalid
	}
	// At most 32 proven-owned resources, each bounded by five seconds. Repeated
	// cleanup failure remains visible and resumable; never wait for real TTL.
	priorAllowed, priorTimeout := s.allowed, s.limits.RequestTimeout
	defer func() { s.allowed = priorAllowed; s.limits.RequestTimeout = priorTimeout }()
	s.limits.RequestTimeout = min(priorTimeout, 5*time.Second)
	for _, a := range pending {
		if a.Removed {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.allowed = map[string]bool{a.DeleteEndpoint: true}
		response, callErr := s.Do(ctx, Request{EndpointID: a.DeleteEndpoint, Path: a.DeletePath, Category: "cleanup"})
		a.Reason = "external_error"
		if callErr == nil && ((response.Status >= 200 && response.Status < 300) || response.Status == 404) {
			a.Removed = true
			a.Reason = "ok"
		}
		if err = s.store.SaveArtifact(ctx, s.jobID, a); err != nil {
			return err
		}
	}
	return nil
}
