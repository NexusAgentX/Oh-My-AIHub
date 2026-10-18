package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type contextPatch struct {
	start, end int
	value      []byte
}

// RewriteCodexContext applies the approved identifier-only exception. This is
// not wired into Serve until F8: ordinary requests retain their current path.
// It uses the existing byte scanner, preserving every other byte including
// opaque encrypted contents, input item IDs, notes text and unknown extensions.
func RewriteCodexContext(body []byte, headers http.Header, actor gatewaycontract.Principal, namespace *gatewaycontract.ContextNamespace) ([]byte, http.Header, error) {
	if namespace == nil {
		return nil, nil, gatewaycontract.ErrConflict
	}
	if !actor.Active || actor.AccountID == "" || actor.KeyID == "" {
		return nil, nil, gatewaycontract.ErrInactive
	}
	if len(body) > gatewaycontract.MaxContractRequestBytes || !json.Valid(body) {
		return nil, nil, errMalformedJSON
	}
	identifier := func(raw []byte) ([]byte, error) {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return nil, errMalformedJSON
		}
		mapped, err := namespace.Identifier(actor, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(mapped)
	}
	identityFields := []string{"session_id", "thread_id", "parent_thread_id", "forked_from_thread_id"}
	var rewrite func([]byte, string) ([]byte, error)
	rewrite = func(input []byte, position string) ([]byte, error) {
		if position == "metadata-string" {
			if len(input) > 64<<10 {
				return nil, errMalformedJSON
			}
			var metadata string
			if json.Unmarshal(input, &metadata) != nil {
				return nil, errMalformedJSON
			}
			changed, err := rewrite([]byte(metadata), "metadata")
			if err != nil {
				return nil, err
			}
			return json.Marshal(string(changed))
		}
		if len(input) > gatewaycontract.MaxContractRequestBytes {
			return nil, errMalformedJSON
		}
		local := []contextPatch{}
		seen := map[string]bool{}
		var fieldErr error
		_, err := walkObject(input, 0, func(key []byte, start, end int) {
			if fieldErr != nil {
				return
			}
			name := string(key)
			kind := ""
			switch position {
			case "root":
				if name == "context" {
					kind = "context"
				}
				if name == "client_metadata" {
					kind = "client_metadata"
				}
			case "context":
				if name == "session_id" {
					kind = "id"
				}
			case "metadata":
				if slices.Contains(identityFields, name) {
					kind = "id"
				}
			case "client_metadata":
				if slices.Contains(identityFields, name) || name == "x-codex-parent-thread-id" {
					kind = "id"
				}
				if name == "x-codex-turn-metadata" {
					kind = "metadata-string"
				}
			}
			if kind == "" {
				return
			}
			if seen[name] {
				fieldErr = errors.New("duplicate context identifier field")
				return
			}
			seen[name] = true
			if position == "metadata" && bytes.Equal(bytes.TrimSpace(input[start:end]), []byte("null")) {
				return
			}
			var value []byte
			if kind == "id" {
				value, fieldErr = identifier(input[start:end])
			} else {
				value, fieldErr = rewrite(input[start:end], kind)
			}
			if fieldErr == nil {
				local = append(local, contextPatch{start, end, value})
			}
		})
		if err != nil {
			return nil, err
		}
		if fieldErr != nil {
			return nil, fieldErr
		}
		if len(local) == 0 {
			return input, nil
		}
		var output bytes.Buffer
		last := 0
		for _, p := range local {
			output.Write(input[last:p.start])
			output.Write(p.value)
			last = p.end
		}
		output.Write(input[last:])
		return output.Bytes(), nil
	}
	// Mapping JSON metadata and real headers uses the exact same namespace.
	output, err := rewrite(body, "root")
	if err != nil {
		return nil, nil, err
	}
	next := headers.Clone()
	if next == nil {
		next = make(http.Header)
	}
	for key := range next {
		if strings.EqualFold(key, "Content-Length") {
			delete(next, key)
		}
	}
	valuesFor := func(name string) []string {
		var values []string
		for key, v := range next {
			if strings.EqualFold(key, name) {
				values = append(values, v...)
				delete(next, key)
			}
		}
		return values
	}
	for _, name := range []string{"session-id", "thread-id", "x-client-request-id", "x-codex-parent-thread-id"} {
		if values := valuesFor(name); len(values) > 0 {
			if len(values) != 1 {
				return nil, nil, errors.New("duplicate context header")
			}
			mapped, err := namespace.Identifier(actor, values[0])
			if err != nil {
				return nil, nil, err
			}
			next.Set(name, mapped)
		}
	}
	if values := valuesFor("x-codex-turn-metadata"); len(values) > 0 {
		if len(values) != 1 || len(values[0]) > 64<<10 || !json.Valid([]byte(values[0])) {
			return nil, nil, errMalformedJSON
		}
		value, err := rewrite([]byte(values[0]), "metadata")
		if err != nil {
			return nil, nil, err
		}
		next.Set("x-codex-turn-metadata", string(value))
	}
	return output, next, nil
}
