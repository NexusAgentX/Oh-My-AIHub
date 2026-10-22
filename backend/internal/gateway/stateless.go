package gateway

import "strings"

// The gateway answers every request from the request itself: it keeps no
// response, conversation or background state. The two entrances below are the
// JSON entrances a client can reach with a body that asks for such state, so
// they are the ones rewritten at the boundary.
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
var statefulFields = []string{"previous_response_id", "conversation"}

// statefulField names the first member of the scanned body that asks for
// server-side state, or "" when there is none. A member spelled in another case
// is found too, and an explicit null or empty value is not a request for state.
func (s objectScan) statefulField(body []byte) string {
	for _, key := range statefulFields {
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
