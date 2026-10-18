package gateway

import (
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// CodexHandler is shared by the public router and the isolated detector bridge.
// Endpoint values come only from the validated fixed manifest, never a request.
func (e *Engine) CodexHandler(endpoint gatewaycontract.Endpoint) http.HandlerFunc {
	if endpoint.Feature != "F8" {
		return nil
	}
	switch {
	case endpoint.ID == "identity.whoami":
		return func(w http.ResponseWriter, r *http.Request) { e.ServeCodexIdentity(w, r, false) }
	case endpoint.ID == "identity.accounts":
		return func(w http.ResponseWriter, r *http.Request) { e.ServeCodexIdentity(w, r, true) }
	case endpoint.Scope == "context_acl_only" || endpoint.ID == "codex.models":
		return func(w http.ResponseWriter, r *http.Request) { e.ServeAuxiliary(w, r, endpoint) }
	case endpoint.Replay == "inference":
		format := channel.FormatOpenAIResponses
		if strings.HasPrefix(endpoint.ID, "codex.images") {
			format = channel.FormatOpenAIImages
		}
		return func(w http.ResponseWriter, r *http.Request) { e.Serve(w, r, format, "", false) }
	}
	return nil
}
