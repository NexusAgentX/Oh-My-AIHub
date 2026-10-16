package gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// authLocation is where the client put the platform key; the upstream key is
// written back to the same place.
type authLocation int

const (
	authBearer authLocation = iota
	authXAPIKey
	authGoogleHeader
	authQuery
)

var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Proxy-Connection": true,
}

// requestHeadersNeverForwarded are dropped from the client request: framing is
// the transport's job, credentials are replaced, and forwarding headers would
// reveal the client's network address to a third party.
var requestHeadersNeverForwarded = map[string]bool{
	"Host": true, "Content-Length": true, "Cookie": true, "Accept-Encoding": true,
	"Authorization": true, "X-Api-Key": true, "X-Goog-Api-Key": true, "X-Aihub-Tag": true,
	"X-Forwarded-For": true, "X-Forwarded-Host": true, "X-Forwarded-Proto": true, "X-Forwarded-Port": true,
	"X-Real-Ip": true, "Forwarded": true, "Via": true,
}

func connectionTokens(header http.Header) map[string]bool {
	tokens := map[string]bool{}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				tokens[http.CanonicalHeaderKey(token)] = true
			}
		}
	}
	return tokens
}

// UpstreamHeaders builds the request headers of one attempt: the client's
// headers minus hop-by-hop and credential headers, then the channel's header
// rules and optional User-Agent, then the upstream key at the client's own
// credential location.
func UpstreamHeaders(client http.Header, location authLocation, upstreamKey string, advanced channel.Advanced) http.Header {
	out := http.Header{}
	extra := connectionTokens(client)
	for name, values := range client {
		canonical := http.CanonicalHeaderKey(name)
		if hopByHop[canonical] || extra[canonical] || requestHeadersNeverForwarded[canonical] {
			continue
		}
		out[canonical] = append([]string(nil), values...)
	}
	for _, name := range advanced.HeaderRules.Remove {
		out.Del(name)
	}
	for _, rule := range advanced.HeaderRules.Set {
		out.Set(rule.Name, rule.Value)
	}
	if advanced.UserAgent != nil {
		out.Set("User-Agent", *advanced.UserAgent)
	}
	if out.Get("Content-Type") == "" {
		out.Set("Content-Type", "application/json")
	}
	switch location {
	case authBearer:
		out.Set("Authorization", "Bearer "+upstreamKey)
	case authXAPIKey:
		out.Set("x-api-key", upstreamKey)
	case authGoogleHeader:
		out.Set("x-goog-api-key", upstreamKey)
	}
	return out
}

// UpstreamURL joins the channel Base URL with the client's request path. A
// Gemini path carries the model name, which becomes the upstream name. The
// platform key query parameter is removed, and replaced by the upstream key
// when the client authenticated that way.
func UpstreamURL(baseURL string, requestPath, rawQuery string, format channel.Format, upstreamModel string, location authLocation, upstreamKey string) (string, error) {
	target, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if format == channel.FormatGemini {
		if index := strings.LastIndex(requestPath, ":"); index >= 0 && strings.HasPrefix(requestPath, "/v1beta/models/") {
			requestPath = "/v1beta/models/" + upstreamModel + requestPath[index:]
		}
	}
	target.Path = strings.TrimSuffix(target.Path, "/") + requestPath
	target.RawPath = ""
	var kept []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		name, _, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(name); err == nil && decoded == "key" {
			continue
		}
		kept = append(kept, pair)
	}
	if location == authQuery {
		kept = append(kept, "key="+url.QueryEscape(upstreamKey))
	}
	target.RawQuery = strings.Join(kept, "&")
	return target.String(), nil
}

// ClientResponseHeaders copies the upstream response headers for the client.
// Content-Length and Content-Encoding are dropped when the transport already
// decompressed the body.
func ClientResponseHeaders(upstream http.Header, decompressed bool) http.Header {
	out := http.Header{}
	extra := connectionTokens(upstream)
	for name, values := range upstream {
		canonical := http.CanonicalHeaderKey(name)
		if hopByHop[canonical] || extra[canonical] || canonical == "Set-Cookie" || canonical == "X-Aihub-Request-Id" {
			continue
		}
		if decompressed && (canonical == "Content-Length" || canonical == "Content-Encoding") {
			continue
		}
		out[canonical] = append([]string(nil), values...)
	}
	return out
}
