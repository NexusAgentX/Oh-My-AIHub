package gateway

import (
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// The gateway answers every request from the request itself: it keeps no
// response, conversation, cache or background state. The entrances below are
// the ones a client can reach with a body that asks for such state, so they are
// the ones checked at the boundary.
func statelessPath(path string) bool {
	switch path {
	case "/v1/responses", "/v1/chat/completions":
		return true
	}
	return false
}

// statefulFields are the top-level members that continue an upstream-side
// conversation. They cannot be forwarded: the gateway would have to store the
// earlier turn for the request to mean anything.
var continuationFields = []string{"previous_response_id", "conversation"}

// cachedContextFields are the Gemini members that name an upstream-side cache.
// The cache management endpoints are retired, so a reference to an existing
// cache cannot be honoured either.
var cachedContextFields = []string{"cachedContent"}

// statefulFields names the members that ask for server-side state on this
// entrance, or nil when the entrance is not checked. A Gemini generation body
// names its cached context; the two OpenAI JSON entrances continue a
// conversation.
func statefulFields(format channel.Format, path string) []string {
	if format == channel.FormatGemini {
		return cachedContextFields
	}
	if statelessPath(path) {
		return continuationFields
	}
	return nil
}

// statefulField names the first member of the scanned body that asks for
// server-side state, or "" when there is none. A member spelled in another case
// is found too, and an explicit null or empty value is not a request for state.
func (s objectScan) statefulField(body []byte, fields []string) string {
	for _, key := range fields {
		for _, member := range s.entries {
			if !strings.EqualFold(member.key, key) {
				continue
			}
			switch value := string(body[member.start:member.end]); value {
			case "null", `""`:
				continue
			}
			return key
		}
	}
	return ""
}
