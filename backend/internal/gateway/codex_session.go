package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexsession"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type notesSession struct {
	id    codexsession.Identity
	route *codexsession.Route
}

var errNotesSessionID = errors.New("codex notes require a consistent session identifier")

// The true session in metadata is authoritative. Session-Id can be a cache
// override, and child agents may have a distinct thread ID. Do not merge
// sessions based on prompt_cache_key or a caller's unrelated opaque item IDs.
func codexNotesSessionID(r *http.Request, body []byte) (string, error) {
	var scratch Observer
	root := scratch.objectOf(body)
	contextRaw, metadataRaw := root.get("context"), root.get("client_metadata")
	var identity struct {
		Session string `json:"session_id"`
		Thread  string `json:"thread_id"`
		Turn    string `json:"x-codex-turn-metadata"`
	}
	var direct struct {
		Session string `json:"session_id"`
	}
	if len(contextRaw) > 64<<10 || len(metadataRaw) > 64<<10 {
		return "", errNotesSessionID
	}
	if len(contextRaw) > 0 && json.Unmarshal(contextRaw, &direct) != nil {
		return "", errNotesSessionID
	}
	if len(metadataRaw) > 0 && json.Unmarshal(metadataRaw, &identity) != nil {
		return "", errNotesSessionID
	}
	id := ""
	merge := func(value string) bool {
		if value == "" {
			return true
		}
		if len(value) != 36 || id != "" && id != value {
			return false
		}
		id = value
		return true
	}
	if !merge(direct.Session) || !merge(identity.Session) {
		return "", errNotesSessionID
	}
	for _, raw := range []string{identity.Turn, r.Header.Get("X-Codex-Turn-Metadata")} {
		if raw == "" {
			continue
		}
		var turn struct {
			Session string `json:"session_id"`
		}
		if len(raw) > 64<<10 || json.Unmarshal([]byte(raw), &turn) != nil || !merge(turn.Session) {
			return "", errNotesSessionID
		}
	}
	if id == "" {
		for _, fallback := range []string{identity.Thread, r.Header.Get("Thread-Id"), r.Header.Get("Session-Id")} {
			if fallback != "" {
				if !merge(fallback) {
					return "", errNotesSessionID
				}
				break
			}
		}
	}
	if id == "" {
		return "", errNotesSessionID
	}
	return id, nil
}

func (e *Engine) loadNotesSession(ctx context.Context, r *http.Request, body []byte, key KeyAuth) (*notesSession, error) {
	id, err := codexNotesSessionID(r, body)
	if err != nil {
		return nil, err
	}
	s := &notesSession{id: codexsession.Identity{AccountID: key.OwnerID, KeyID: key.KeyID, SessionID: id}}
	if e.CodexSessions == nil {
		return nil, gatewaycontract.ErrConflict
	}
	route, err := e.CodexSessions.Lookup(ctx, s.id)
	if errors.Is(err, gatewaycontract.ErrNotFound) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.route = &route
	return s, nil
}

func notesRoute(c Candidate) codexsession.Route {
	return codexsession.Route{Binding: candidateBinding(c), CredentialScope: c.CredentialScope}
}

func notesMatch(c Candidate, s *notesSession) bool {
	return s == nil || s.route == nil || notesRoute(c) == *s.route
}

// This runs after permissions/capabilities/ranking and before any upstream
// request. A concurrent first request's winner must still be an eligible
// candidate here; otherwise fail without contacting a different upstream.
func (e *Engine) claimNotesSession(ctx context.Context, s *notesSession, ordered []Candidate) ([]Candidate, error) {
	if s == nil {
		return ordered, nil
	}
	if len(ordered) == 0 {
		return nil, gatewaycontract.ErrNotFound
	}
	if s.route == nil {
		route, err := e.CodexSessions.Claim(ctx, s.id, notesRoute(ordered[0]))
		if err != nil {
			return nil, err
		}
		s.route = &route
	}
	for _, c := range ordered {
		if notesMatch(c, s) {
			return []Candidate{c}, nil
		}
	}
	return nil, gatewaycontract.ErrConflict
}

var requiredNotesCapabilities = buildNotesRequirements()

func notesRequirements() []CapabilityRequirement { return requiredNotesCapabilities }

func buildNotesRequirements() []CapabilityRequirement {
	var result []CapabilityRequirement
	for _, namespace := range []struct {
		name       string
		operations []string
	}{
		{"notes", []string{"thread_hint", "write_file", "read_file", "list_files_by_prefix", "search_contents", "append_to_file"}},
		{"history", []string{"list_windows", "list_items", "read_item", "search_contents"}},
	} {
		for _, op := range namespace.operations {
			for _, req := range codexContextRequirements("codex." + namespace.name + "." + op) {
				if !slices.Contains(result, req) {
					result = append(result, req)
				}
			}
		}
	}
	return result
}

func notesSessionError(err error) (int, string, string) {
	if errors.Is(err, errNotesSessionID) {
		return 400, "notes_session_required", "使用笔记与历史工具需要一致、稳定的会话编号"
	}
	return 503, "notes_session_unavailable", "笔记会话的原渠道、凭据或状态服务不可用；请检查配置，或使用新会话"
}
