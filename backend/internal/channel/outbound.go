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
	// testRootCAs is package-private so transport integration tests can use a
	// real TLS socket without weakening certificate verification in production.
	testRootCAs *x509.CertPool
}

func NewOutboundPolicy(allowedPorts, additionalBlockedHosts []string) (*OutboundPolicy, error) {
	return NewOutboundPolicyWithResolver(allowedPorts, additionalBlockedHosts, netResolver{resolver: net.DefaultResolver})
}

func NewOutboundPolicyWithResolver(allowedPorts, additionalBlockedHosts []string, resolver Resolver) (*OutboundPolicy, error) {
	if resolver == nil {
		return nil, errorsNewConfiguration()
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
	return &OutboundPolicy{
		resolver:     resolver,
		dialContext:  (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}).DialContext,
		allowedPorts: ports,
		blockedHosts: blocked,
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
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("%w: DNS resolution failed", ErrUnsafeUpstream)
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := map[netip.Addr]struct{}{}
	for _, address := range addresses {
		parsedAddress, ok := netip.AddrFromSlice(address)
		if !ok || unsafeAddress(parsedAddress.Unmap()) {
			return nil, fmt.Errorf("%w: DNS returned a protected address", ErrUnsafeUpstream)
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

func (p *OutboundPolicy) Endpoint(baseURL string, protocol Protocol, upstreamModelID string, stream bool) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, ErrInvalidInput
	}
	var suffix string
	switch protocol {
	case ProtocolOpenAIChat:
		suffix = "/v1/chat/completions"
	case ProtocolOpenAIResponse:
		suffix = "/v1/responses"
	case ProtocolAnthropic:
		suffix = "/v1/messages"
	case ProtocolGemini:
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
	canonical, err := p.NormalizeBaseURL(normalizedBaseURL)
	if err != nil || canonical != normalizedBaseURL {
		return nil, ErrUnsafeUpstream
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return nil, ErrUnsafeUpstream
	}
	addresses, err := p.ValidateResolution(ctx, canonical)
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	host := parsed.Hostname()
	pinned := addresses[0]
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      false,
		MaxResponseHeaderBytes: 1 << 20,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.testRootCAs},
		DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
			requestedHost, requestedPort, splitErr := net.SplitHostPort(address)
			if splitErr != nil || !strings.EqualFold(strings.TrimSuffix(requestedHost, "."), host) || requestedPort != port {
				return nil, ErrUnsafeUpstream
			}
			return p.dialContext(dialContext, network, net.JoinHostPort(pinned.String(), port))
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   totalTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func ApplyAuthentication(request *http.Request, protocol Protocol, credential string) {
	switch protocol {
	case ProtocolAnthropic:
		request.Header.Set("x-api-key", credential)
		request.Header.Set("anthropic-version", "2023-06-01")
	case ProtocolGemini:
		request.Header.Set("x-goog-api-key", credential)
	default:
		request.Header.Set("Authorization", "Bearer "+credential)
	}
}
