package channel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/idna"
)

var ErrUnsafeUpstream = errors.New("unsafe upstream endpoint")

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]net.IP, error)
}

type netResolver struct{ resolver *net.Resolver }

func (r netResolver) LookupNetIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return r.resolver.LookupIP(ctx, network, host)
}

type OutboundPolicy struct {
	resolver     Resolver
	dialContext  func(context.Context, string, string) (net.Conn, error)
	allowedPorts map[string]struct{}
	blockedHosts []string
	// blockedKey identifies blockedHosts; the egress pool serves a policy only
	// while it is the pool's current key (see egressPool.setBlocked).
	blockedKey string
	// egress is the DNS cache and connection pool of gateway forwarding. Every
	// copy of a policy made by WithExtraBlockedHosts shares it.
	egress *egressPool
	// testRootCAs is package-private so transport integration tests can use a
	// real TLS socket without weakening certificate verification in production.
	testRootCAs *x509.CertPool
}

func NewOutboundPolicy(allowedPorts, additionalBlockedHosts []string) (*OutboundPolicy, error) {
	return NewOutboundPolicyWithResolver(allowedPorts, additionalBlockedHosts, netResolver{resolver: net.DefaultResolver})
}

func NewOutboundPolicyWithResolver(allowedPorts, additionalBlockedHosts []string, resolver Resolver) (*OutboundPolicy, error) {
	if resolver == nil {
		return nil, errors.New("outbound resolver is required")
	}
	ports := map[string]struct{}{"443": {}}
	for _, value := range allowedPorts {
		value = strings.TrimSpace(value)
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid upstream port %q", value)
		}
		ports[strconv.Itoa(port)] = struct{}{}
	}
	blocked := []string{"api.openai.com"}
	for _, value := range additionalBlockedHosts {
		normalized, err := normalizeHostname(value)
		if err != nil {
			return nil, fmt.Errorf("invalid blocked upstream host %q: %w", value, err)
		}
		blocked = append(blocked, normalized)
	}
	slices.Sort(blocked)
	blocked = slices.Compact(blocked)
	key := blockedKey(blocked)
	return &OutboundPolicy{
		resolver:     resolver,
		dialContext:  (&net.Dialer{Timeout: dialTimeout, KeepAlive: egressKeepAlive}).DialContext,
		allowedPorts: ports,
		blockedHosts: blocked,
		blockedKey:   key,
		egress:       newEgressPool(key),
	}, nil
}

func (p *OutboundPolicy) ValidateBaseURL(ctx context.Context, raw string) (string, error) {
	normalized, err := p.NormalizeBaseURL(raw)
	if err != nil {
		return "", err
	}
	resolutionContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := p.ValidateResolution(resolutionContext, normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func (p *OutboundPolicy) NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "\\") {
		return "", ErrInvalidInput
	}
	hostname, err := normalizeHostname(parsed.Hostname())
	if err != nil || net.ParseIP(hostname) != nil || p.hostBlocked(hostname) {
		return "", ErrInvalidInput
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	if _, allowed := p.allowedPorts[port]; !allowed {
		return "", ErrInvalidInput
	}
	prefix, err := normalizeRootPath(parsed.EscapedPath())
	if err != nil {
		return "", ErrInvalidInput
	}
	parsed.Scheme = "https"
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""
	parsed.Path = prefix
	if port == "443" {
		parsed.Host = hostname
	} else {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func normalizeHostname(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || strings.ContainsAny(value, "/:@[]") {
		return "", ErrInvalidInput
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || ascii == "" || len(ascii) > 253 {
		return "", ErrInvalidInput
	}
	return strings.ToLower(strings.TrimSuffix(ascii, ".")), nil
}

func normalizeRootPath(escaped string) (string, error) {
	if escaped == "" || escaped == "/" {
		return "", nil
	}
	lowerEscaped := strings.ToLower(escaped)
	if strings.Contains(lowerEscaped, "%2f") || strings.Contains(lowerEscaped, "%5c") {
		return "", ErrInvalidInput
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil || strings.Contains(decoded, "//") || strings.Contains(decoded, "%") {
		return "", ErrInvalidInput
	}
	for _, character := range decoded {
		if character == '\\' || character < 0x20 || character == 0x7f {
			return "", ErrInvalidInput
		}
	}
	segments := strings.Split(strings.Trim(decoded, "/"), "/")
	for _, segment := range segments {
		if segment == "." || segment == ".." {
			return "", ErrInvalidInput
		}
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(decoded, "/"))
	if cleaned == "/" {
		return "", nil
	}
	// Accept the API base commonly copied from OpenAI-compatible clients.
	// Validate the original path above before removing the version suffix.
	cleaned = strings.TrimSuffix(cleaned, "/v1")
	segments = strings.Split(strings.Trim(cleaned, "/"), "/")
	for _, segment := range segments {
		lower := strings.ToLower(segment)
		if lower == "v1" || lower == "v1beta" || lower == "chat" || lower == "completions" || lower == "responses" || lower == "messages" || lower == "models" || strings.Contains(lower, "generatecontent") {
			return "", ErrInvalidInput
		}
	}
	return cleaned, nil
}

func (p *OutboundPolicy) hostBlocked(host string) bool {
	for _, blocked := range p.blockedHosts {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

func (p *OutboundPolicy) ValidateResolution(ctx context.Context, normalizedBaseURL string) ([]netip.Addr, error) {
	parsed, err := url.Parse(normalizedBaseURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, ErrUnsafeUpstream
	}
	return validatedLookup(ctx, p.resolver, parsed.Hostname())
}

// errProtectedAddress is why validateAddresses refuses an answer.
var errProtectedAddress = fmt.Errorf("%w: DNS returned a protected address", ErrUnsafeUpstream)

// validatedLookup resolves host and returns its addresses, or ErrUnsafeUpstream
// if the lookup failed or any address is protected.
func validatedLookup(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("%w: DNS resolution failed", ErrUnsafeUpstream)
	}
	return validateAddresses(addresses)
}

// validateAddresses is the one rule for what a resolution may contain: a single
// protected address rejects the whole answer, so a host that also resolves to
// an internal address is never dialed at all.
func validateAddresses(addresses []net.IP) ([]netip.Addr, error) {
	validated := make([]netip.Addr, 0, len(addresses))
	seen := map[netip.Addr]struct{}{}
	for _, address := range addresses {
		parsedAddress, ok := netip.AddrFromSlice(address)
		if !ok || unsafeAddress(parsedAddress.Unmap()) {
			return nil, errProtectedAddress
		}
		parsedAddress = parsedAddress.Unmap()
		if _, exists := seen[parsedAddress]; exists {
			continue
		}
		seen[parsedAddress] = struct{}{}
		validated = append(validated, parsedAddress)
	}
	return validated, nil
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

// IANA IPv6 Global Unicast Address Space registry allocations, last reviewed
// 2026-09-02 against the registry updated 2025-10-10. Unlisted 2000::/3 space
// is explicitly reserved for future allocation and therefore fails closed.
var allocatedGlobalUnicastIPv6 = []netip.Prefix{
	netip.MustParsePrefix("2001:200::/23"),
	netip.MustParsePrefix("2001:400::/23"),
	netip.MustParsePrefix("2001:600::/23"),
	netip.MustParsePrefix("2001:800::/22"),
	netip.MustParsePrefix("2001:c00::/23"),
	netip.MustParsePrefix("2001:e00::/23"),
	netip.MustParsePrefix("2001:1200::/23"),
	netip.MustParsePrefix("2001:1400::/22"),
	netip.MustParsePrefix("2001:1800::/23"),
	netip.MustParsePrefix("2001:1a00::/23"),
	netip.MustParsePrefix("2001:1c00::/22"),
	netip.MustParsePrefix("2001:2000::/19"),
	netip.MustParsePrefix("2001:4000::/23"),
	netip.MustParsePrefix("2001:4200::/23"),
	netip.MustParsePrefix("2001:4400::/23"),
	netip.MustParsePrefix("2001:4600::/23"),
	netip.MustParsePrefix("2001:4800::/23"),
	netip.MustParsePrefix("2001:4a00::/23"),
	netip.MustParsePrefix("2001:4c00::/23"),
	netip.MustParsePrefix("2001:5000::/20"),
	netip.MustParsePrefix("2001:8000::/19"),
	netip.MustParsePrefix("2001:a000::/20"),
	netip.MustParsePrefix("2001:b000::/20"),
	netip.MustParsePrefix("2003::/18"),
	netip.MustParsePrefix("2400::/12"),
	netip.MustParsePrefix("2410::/12"),
	netip.MustParsePrefix("2600::/12"),
	netip.MustParsePrefix("2610::/23"),
	netip.MustParsePrefix("2620::/23"),
	netip.MustParsePrefix("2630::/12"),
	netip.MustParsePrefix("2800::/12"),
	netip.MustParsePrefix("2a00::/12"),
	netip.MustParsePrefix("2a10::/12"),
	netip.MustParsePrefix("2c00::/12"),
}

func unsafeAddress(address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return true
	}
	if address.Is6() {
		allocated := false
		for _, prefix := range allocatedGlobalUnicastIPv6 {
			if prefix.Contains(address) {
				allocated = true
				break
			}
		}
		if !allocated {
			return true
		}
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (p *OutboundPolicy) Endpoint(baseURL string, format Format, upstreamModelID string, stream bool) (*url.URL, error) {
	return BuildEndpoint(baseURL, format, upstreamModelID, stream)
}

// BuildEndpoint is the default request URL of a format under a Base URL.
func BuildEndpoint(baseURL string, format Format, upstreamModelID string, stream bool) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, ErrInvalidInput
	}
	var suffix string
	switch format {
	case FormatOpenAIChat:
		suffix = "/v1/chat/completions"
	case FormatOpenAIResponses:
		suffix = "/v1/responses"
	case FormatAnthropic:
		suffix = "/v1/messages"
	case FormatGemini:
		operation := ":generateContent"
		if stream {
			operation = ":streamGenerateContent"
		}
		suffix = "/v1beta/models/" + upstreamModelID + operation
	default:
		return nil, ErrInvalidInput
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + suffix
	base.RawPath = ""
	return base, nil
}

func (p *OutboundPolicy) ClientFor(ctx context.Context, normalizedBaseURL string, totalTimeout time.Duration) (*http.Client, error) {
	responseHeaderTimeout := 10 * time.Second
	if totalTimeout < responseHeaderTimeout {
		responseHeaderTimeout = totalTimeout
	}
	return p.clientFor(ctx, normalizedBaseURL, totalTimeout, responseHeaderTimeout)
}

func (p *OutboundPolicy) ClientForProxy(ctx context.Context, normalizedBaseURL string, totalTimeout time.Duration) (*http.Client, error) {
	responseHeaderTimeout := 60 * time.Second
	if totalTimeout < responseHeaderTimeout {
		responseHeaderTimeout = totalTimeout
	}
	return p.clientFor(ctx, normalizedBaseURL, totalTimeout, responseHeaderTimeout)
}

// ClientForGateway returns the client of one gateway attempt. It differs from
// the clients of discovery and format tests in two ways.
//
// It has neither a response-header deadline (a non-streaming completion
// legitimately sends its first byte only when generation ends) nor
// Client.Timeout (it would also cut off a stream that is still producing
// output). Every deadline of a forwarded request comes from the request's
// context, which the gateway owns: the first-byte timeout and idle timeout of a
// stream, the total timeout of a non-streaming request, and the limit of the
// whole call (ADR-0035 rule 5). That is also what lets the client be shared.
//
// And it is shared: one pooled client per host and port, dialing only the
// addresses of a validated resolution that is cached for 60 seconds, with
// keep-alive and HTTP/2 (ADR-0036 rule 7, see egress.go). The checks that gate a
// request still run on every call: the Base URL is normalized against the
// current block list and must be the canonical form, and the resolution is
// validated before it enters the cache.
func (p *OutboundPolicy) ClientForGateway(ctx context.Context, normalizedBaseURL string) (*http.Client, error) {
	canonical, err := p.NormalizeBaseURL(normalizedBaseURL)
	if err != nil || canonical != normalizedBaseURL {
		return nil, ErrUnsafeUpstream
	}
	parsed, err := url.Parse(canonical)
	if err != nil || parsed.Hostname() == "" {
		return nil, ErrUnsafeUpstream
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	client, err := p.egress.client(ctx, p, parsed.Hostname(), port)
	if err != nil {
		return nil, err
	}
	if client == nil {
		// The pool belongs to a newer block list than this policy (the settings
		// changed while this snapshot was still in use), or the policy has no
		// pool: connect the way discovery does, resolving anew.
		return p.clientFor(ctx, normalizedBaseURL, 0, 0)
	}
	return client, nil
}

// ClientForFresh is the client of a streaming format test: no deadline of its
// own like ClientForGateway, but a one-shot connection to a freshly resolved
// address like every other call outside the gateway's forwarding.
func (p *OutboundPolicy) ClientForFresh(ctx context.Context, normalizedBaseURL string) (*http.Client, error) {
	return p.clientFor(ctx, normalizedBaseURL, 0, 0)
}

// WithExtraBlockedHosts returns a policy that additionally refuses the given
// hosts (settings.extra_blocked_hosts), leaving the receiver untouched.
func (p *OutboundPolicy) WithExtraBlockedHosts(hosts []string) (Outbound, error) {
	blocked := append([]string(nil), p.blockedHosts...)
	for _, value := range hosts {
		normalized, err := normalizeHostname(value)
		if err != nil {
			return nil, fmt.Errorf("invalid blocked upstream host %q: %w", value, err)
		}
		blocked = append(blocked, normalized)
	}
	slices.Sort(blocked)
	copied := *p
	copied.blockedHosts = slices.Compact(blocked)
	copied.blockedKey = blockedKey(copied.blockedHosts)
	// A changed block list closes every pooled connection at once; a policy
	// that is still built from the old list is no longer served from the pool.
	p.egress.setBlocked(copied.blockedKey)
	return &copied, nil
}

// blockedKey identifies a block list. A newline cannot occur in a host name.
func blockedKey(blocked []string) string { return strings.Join(blocked, "\n") }

func (p *OutboundPolicy) clientFor(ctx context.Context, normalizedBaseURL string, totalTimeout, responseHeaderTimeout time.Duration) (*http.Client, error) {
	canonical, err := p.NormalizeBaseURL(normalizedBaseURL)
	if err != nil || canonical != normalizedBaseURL {
		return nil, ErrUnsafeUpstream
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return nil, ErrUnsafeUpstream
	}
	resolutionContext, cancelResolution := context.WithTimeout(ctx, 5*time.Second)
	defer cancelResolution()
	addresses, err := p.ValidateResolution(resolutionContext, canonical)
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	host := parsed.Hostname()
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      false,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.testRootCAs},
		// Only the first address of the resolution, as ever: a one-shot client
		// has no use for a fallback.
		DialContext: p.pinnedDial(host, port, addresses[:1], nil),
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       totalTimeout,
		CheckRedirect: noRedirect,
	}, nil
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// pinnedDial returns a dial function that connects to the given validated
// addresses of host and to nothing else. The http.Transport asks it for the
// host and port of the request URL, which must be the ones the client was
// made for; the connection then goes to a validated address, never to the name,
// while TLS still verifies the certificate against the name. The addresses are
// tried in turn until one connects, starting with the one that connected last,
// so that an unreachable first address costs a new connection nothing once
// another has worked. dialed, if set, counts the connections made.
func (p *OutboundPolicy) pinnedDial(host, port string, addresses []netip.Addr, dialed *atomic.Uint64) func(context.Context, string, string) (net.Conn, error) {
	var preferred atomic.Int64
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		requestedHost, requestedPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(strings.TrimSuffix(requestedHost, "."), host) || requestedPort != port {
			return nil, ErrUnsafeUpstream
		}
		var lastErr error
		first := int(preferred.Load())
		for attempt := range addresses {
			index := (first + attempt) % len(addresses)
			conn, err := p.dialContext(ctx, network, net.JoinHostPort(addresses[index].String(), port))
			if err == nil {
				preferred.Store(int64(index))
				if dialed != nil {
					dialed.Add(1)
				}
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}
}

// ApplyAuthentication writes the default credential location of a format; the
// gateway itself writes the credential where the client put the platform key.
func ApplyAuthentication(request *http.Request, format Format, credential string) {
	switch format {
	case FormatAnthropic:
		request.Header.Set("x-api-key", credential)
		request.Header.Set("anthropic-version", "2023-06-01")
	case FormatGemini:
		request.Header.Set("x-goog-api-key", credential)
	default:
		request.Header.Set("Authorization", "Bearer "+credential)
	}
}

// Outbound is the pinned egress boundary (ADR-0036) as the channel service and
// the gateway use it. OutboundPolicy implements it; tests substitute a fake
// that reaches loopback servers.
type Outbound interface {
	NormalizeBaseURL(raw string) (string, error)
	// ValidateBaseURL normalizes and additionally checks DNS resolution.
	ValidateBaseURL(ctx context.Context, raw string) (string, error)
	// Client returns a client for short, bounded calls (model discovery).
	Client(ctx context.Context, normalizedBaseURL string, totalTimeout time.Duration) (*http.Client, error)
	// GatewayClient returns the shared, pooled client of forwarded requests. It
	// has no deadline of its own; the caller bounds the exchange through the
	// request's context.
	GatewayClient(ctx context.Context, normalizedBaseURL string) (*http.Client, error)
	// FreshClient returns a one-shot client without any deadline of its own for
	// the streaming format test: it resolves anew and keeps no connection.
	FreshClient(ctx context.Context, normalizedBaseURL string) (*http.Client, error)
	// WithExtraBlockedHosts adds the administrator-managed host block list.
	WithExtraBlockedHosts(hosts []string) (Outbound, error)
}

// Client implements Outbound.
func (p *OutboundPolicy) Client(ctx context.Context, normalizedBaseURL string, totalTimeout time.Duration) (*http.Client, error) {
	return p.ClientFor(ctx, normalizedBaseURL, totalTimeout)
}

// GatewayClient implements Outbound.
func (p *OutboundPolicy) GatewayClient(ctx context.Context, normalizedBaseURL string) (*http.Client, error) {
	return p.ClientForGateway(ctx, normalizedBaseURL)
}

// FreshClient implements Outbound.
func (p *OutboundPolicy) FreshClient(ctx context.Context, normalizedBaseURL string) (*http.Client, error) {
	return p.ClientForFresh(ctx, normalizedBaseURL)
}
