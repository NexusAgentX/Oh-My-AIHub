package gateway

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

func extractPlatformCredential(r *http.Request, protocol channel.Protocol) (string, error) {
	headerNames := []string{"Authorization", "x-api-key", "x-goog-api-key"}
	present := make([]string, 0, 3)
	for _, name := range headerNames {
		if len(r.Header.Values(name)) > 0 {
			present = append(present, name)
		}
	}
	if len(present) != 1 {
		return "", ErrInvalidAPIKey
	}
	expected := adapterFor(protocol).platformCredentialHeader()
	if !strings.EqualFold(present[0], expected) || len(r.Header.Values(expected)) != 1 {
		return "", ErrInvalidAPIKey
	}
	value := r.Header.Get(expected)
	if expected == "Authorization" {
		if !strings.HasPrefix(value, "Bearer ") || strings.Contains(value[len("Bearer "):], " ") {
			return "", ErrInvalidAPIKey
		}
		value = strings.TrimPrefix(value, "Bearer ")
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", ErrInvalidAPIKey
	}
	return value, nil
}

const defaultAnthropicVersion = "2023-06-01"

func safeHeaderValue(value string, maxLength int) bool {
	if strings.TrimSpace(value) == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return false
		}
	}
	return true
}

// outboundHeaderDenylist lists headers the gateway never forwards.
//
// Request framing and hop-by-hop headers belong to Go's transport; credential
// and account-scoping headers belong to the platform. Accept-Encoding is
// stripped so Go negotiates and transparently decodes gzip itself.
//
// ponytail: static denylist. When a channel genuinely needs an
// account-scoping header such as OpenAI-Organization, add a per-channel
// allowlist rather than removing the entry.
var outboundHeaderDenylist = map[string]struct{}{
	"Host":                {},
	"Content-Length":      {},
	"Connection":          {},
	"Keep-Alive":          {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Authorization":       {},
	"X-Api-Key":           {},
	"X-Goog-Api-Key":      {},
	"Cookie":              {},
	"Accept-Encoding":     {},
	"Openai-Organization": {},
	"Openai-Project":      {},
	// Client identity, tracing and retry-key headers stay platform-owned: they
	// change what the upstream attributes to this call and are not vendor
	// protocol features.
	"Forwarded":         {},
	"X-Forwarded-For":   {},
	"X-Forwarded-Host":  {},
	"X-Forwarded-Proto": {},
	"X-Real-Ip":         {},
	"Idempotency-Key":   {},
	"Traceparent":       {},
	"Baggage":           {},
}

// copyOutboundHeaders forwards every client header the platform does not own, so
// beta features and vendor headers reach the upstream unchanged.
func copyOutboundHeaders(target, source http.Header, protocol channel.Protocol, stream bool) {
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if _, blocked := outboundHeaderDenylist[canonical]; blocked {
			continue
		}
		for _, value := range values {
			target.Add(canonical, value)
		}
	}
	// Connection may name additional hop-by-hop headers, in any of its values.
	for _, connection := range source.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				target.Del(trimmed)
			}
		}
	}
	target.Set("Content-Type", "application/json")
	if stream {
		target.Set("Accept", "text/event-stream")
	} else {
		target.Set("Accept", "application/json")
	}
	adapterFor(protocol).defaultRequestHeaders(target)
}

func injectUpstreamAuthentication(header http.Header, protocol channel.Protocol, credential string) {
	header.Del("Authorization")
	header.Del("x-api-key")
	header.Del("x-goog-api-key")
	adapterFor(protocol).setUpstreamAuthentication(header, credential)
}

func sanitizedResponseHeaders(source http.Header) http.Header {
	result := make(http.Header)
	for _, name := range []string{
		"Request-Id", "X-Request-Id",
		"OpenAI-Request-Id", "Anthropic-Request-Id",
	} {
		values := source.Values(name)
		if len(values) != 1 || !safeHeaderValue(values[0], 256) {
			continue
		}
		result.Set(name, values[0])
	}
	return result
}

// outboundRequestCarriesForeignCredential reports whether the request the gateway
// is about to send would leak a platform or sibling-candidate credential through
// a forwarded header or query parameter. The caller runs it before the platform
// injects the candidate's own upstream authentication, so only forwarded client
// data is scanned. foreignCredentials holds the platform secret and the other
// candidates' credentials, never the candidate's own, since candidates may
// legitimately share one upstream key.
func outboundRequestCarriesForeignCredential(request *http.Request, foreignCredentials ...string) bool {
	for name, values := range request.Header {
		if headerNameCarriesCredential(name, foreignCredentials) {
			return true
		}
		for _, value := range values {
			if secretguard.ContainsExactOrJSONEscaped(value, foreignCredentials...) {
				return true
			}
		}
	}
	// Query values are decoded first so a percent-encoded credential is caught too.
	for key, values := range request.URL.Query() {
		if secretguard.ContainsExactOrJSONEscaped(key, foreignCredentials...) {
			return true
		}
		for _, value := range values {
			if secretguard.ContainsExactOrJSONEscaped(value, foreignCredentials...) {
				return true
			}
		}
	}
	return false
}

// headerNameCarriesCredential reports a credential smuggled in a header name.
// Names are canonicalised and may be percent-encoded, so the lowered original and
// the lowered PathUnescape form are both checked (PathUnescape keeps a literal
// '+', which QueryUnescape would turn into a space).
func headerNameCarriesCredential(name string, credentials []string) bool {
	candidates := []string{strings.ToLower(name)}
	if decoded, err := url.PathUnescape(name); err == nil && decoded != name {
		candidates = append(candidates, strings.ToLower(decoded))
	}
	for _, candidate := range candidates {
		for _, credential := range credentials {
			if credential != "" && strings.Contains(candidate, strings.ToLower(credential)) {
				return true
			}
		}
	}
	return false
}

func responseHeaderContainsCredential(source http.Header, credentials ...string) bool {
	for _, values := range sanitizedResponseHeaders(source) {
		for _, value := range values {
			if secretguard.ContainsExactOrJSONEscaped(value, credentials...) {
				return true
			}
		}
	}
	return false
}

func supportedContentEncoding(header http.Header) bool {
	values := header.Values("Content-Encoding")
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		for _, encoding := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(encoding); trimmed != "" && !strings.EqualFold(trimmed, "identity") {
				return false
			}
		}
	}
	return true
}

func validateProtocolQuery(protocol channel.Protocol, stream bool, rawQuery string) error {
	// Only two things are rejected: a malformed query string, and the API-key
	// query parameter, which would leak the platform credential upstream. Every
	// other parameter is forwarded verbatim; the upstream decides what it takes.
	query, err := url.ParseQuery(rawQuery)
	if err != nil || query.Has("key") {
		return ErrInvalidInput
	}
	return nil
}

// mergeUpstreamQuery carries the client query string to the upstream endpoint.
// The provider endpoint keeps its own parameters; the client's are appended.
// Gemini streaming keeps its mandated alt=sse.
func mergeUpstreamQuery(endpoint, rawQuery string, protocol channel.Protocol, stream bool) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", ErrInvalidInput
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", ErrInvalidInput
	}
	if rawQuery != "" {
		clientQuery, err := url.ParseQuery(rawQuery)
		if err != nil {
			return "", ErrInvalidInput
		}
		for key, values := range clientQuery {
			for _, value := range values {
				query.Add(key, value)
			}
		}
	}
	adapterFor(protocol).adjustUpstreamQuery(query, stream)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func copyHeader(target, source http.Header) {
	for name, values := range source {
		if len(values) > 0 {
			target.Set(name, values[0])
		}
	}
}
