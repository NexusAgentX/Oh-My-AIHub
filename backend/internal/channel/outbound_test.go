package channel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeResolver struct {
	addresses []net.IP
	err       error
	calls     int
}

type blockingResolver struct{}

func (blockingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]net.IP, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type deadlineObservingResolver struct {
	remaining time.Duration
}

func (r *deadlineObservingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]net.IP, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("resolver context has no deadline")
	}
	r.remaining = time.Until(deadline)
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]net.IP, error) {
	r.calls++
	return r.addresses, r.err
}

func TestNormalizeBaseURLAndPermanentOfficialEndpointBan(t *testing.T) {
	policy, err := NewOutboundPolicy([]string{"8443"}, []string{"blocked.example"})
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]string{
		"https://gateway.example/v1":          "https://gateway.example",
		"https://gateway.example/v1/":         "https://gateway.example",
		" https://gateway.example/relay/v1/ ": "https://gateway.example/relay",
		"https://gateway.example/relayv1":     "https://gateway.example/relayv1",
		"https://Gateway.Example./relay/":     "https://gateway.example/relay",
		"https://bücher.example:443/root":     "https://xn--bcher-kva.example/root",
		"https://api.openai.com.evil":         "https://api.openai.com.evil",
		"https://gateway.example:8443/root":   "https://gateway.example:8443/root",
	}
	for input, expected := range valid {
		actual, err := policy.NormalizeBaseURL(input)
		if err != nil || actual != expected {
			t.Fatalf("NormalizeBaseURL(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	invalid := []string{
		"http://gateway.example", "https://user@gateway.example", "https://gateway.example?", "https://gateway.example?q=1",
		"https://gateway.example#fragment", "https://127.0.0.1", "https://[2001:4860:4860::8888]",
		"https://api.openai.com", "https://API.OPENAI.COM.", "https://sub.api.openai.com", "https://blocked.example",
		"https://gateway.example:444", "https://gateway.example/v1/chat/completions",
		"https://gateway.example/v1/v1", "https://gateway.example/../v1", "https://gateway.example/v1beta/models",
		"https://gateway.example/%2e%2e/root", "https://gateway.example/root%2fescape", "https://gateway.example/root\\escape",
		"https://gateway.example/%252e%252e", "https://gateway.example/root%252fadmin", "https://gateway.example/%2576%2531",
	}
	for _, input := range invalid {
		if actual, err := policy.NormalizeBaseURL(input); err == nil {
			t.Fatalf("NormalizeBaseURL(%q) unexpectedly returned %q", input, actual)
		}
	}
}

func TestResolutionRejectsAnyProtectedCandidateAndPinsPublicAddress(t *testing.T) {
	policy, err := NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}}
	policy.resolver = resolver
	if _, err := policy.ValidateResolution(context.Background(), "https://gateway.example"); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("mixed safe/private resolution error = %v", err)
	}
	resolver.addresses = []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")}
	addresses, err := policy.ValidateResolution(context.Background(), "https://gateway.example")
	if err != nil || len(addresses) != 2 || addresses[0] != netip.MustParseAddr("93.184.216.34") {
		t.Fatalf("public resolution = %#v, %v", addresses, err)
	}
	var dialed string
	policy.dialContext = func(_ context.Context, _ string, stringAddress string) (net.Conn, error) {
		dialed = stringAddress
		return nil, errors.New("test stop")
	}
	client, err := policy.ClientFor(context.Background(), "https://gateway.example", 0)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableKeepAlives {
		t.Fatal("outbound transport can use an environment proxy or stale pooled connection")
	}
	_, _ = transport.DialContext(context.Background(), "tcp", "gateway.example:443")
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dial target = %q, want pinned public address", dialed)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "other.example:443"); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("unexpected host dial error = %v", err)
	}
}

func TestSpecialPurposeAddressMatrix(t *testing.T) {
	unsafe := []string{
		"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "192.0.2.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1",
		"::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "ff00::1", "2001:db8::1",
		"192.88.99.2", "::192.0.2.1", "64:ff9b::a9fe:a9fe", "64:ff9b:1::a9fe:a9fe",
		"100:0:0:1::1", "2001::ffff:a9fe:a9fe", "2001:2::1", "2002:a9fe:a9fe::1", "3fff::1", "5f00::1",
		"200::1", "4000::1", "6000::1", "f000::1", "fe00::1", "fec0::1",
		"2d00::1", "3000::1", "3ffe::1",
	}
	for _, value := range unsafe {
		if !unsafeAddress(netip.MustParseAddr(value).Unmap()) {
			t.Fatalf("special-purpose address %s was accepted", value)
		}
	}
	for _, value := range []string{"93.184.216.34", "2606:4700:4700::1111", "2a00:1450:4001:81b::200e"} {
		if unsafeAddress(netip.MustParseAddr(value)) {
			t.Fatalf("public address %s was rejected", value)
		}
	}
}

func TestClientRechecksCanonicalURLAgainstCurrentPolicy(t *testing.T) {
	resolver := &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}}
	policy, err := NewOutboundPolicyWithResolver(nil, []string{"gateway.example"}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{
		"https://gateway.example",
		"https://other.example:8443",
		"https://Other.Example",
	} {
		if _, err := policy.ClientFor(context.Background(), stored, 0); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("ClientFor(%q) error = %v, want unsafe upstream", stored, err)
		}
	}
}

func TestOutboundHTTPSClientDoesNotRedirectOrRetryAndBoundsResponses(t *testing.T) {
	var redirectHits, followedHits, retryHits int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			redirectHits++
			response.Header().Set("Location", "/followed")
			response.WriteHeader(http.StatusFound)
		case "/followed":
			followedHits++
			response.WriteHeader(http.StatusNoContent)
		case "/retry":
			retryHits++
			response.WriteHeader(http.StatusServiceUnavailable)
		case "/large":
			_, _ = io.WriteString(response, strings.Repeat("x", 1<<20+1))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}}
	policy, err := NewOutboundPolicyWithResolver([]string{port}, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	policy.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	baseURL := "https://example.com:" + port
	client, err := policy.ClientFor(context.Background(), baseURL, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(upstream.Certificate())
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	if client.Timeout != 15*time.Second || transport.TLSHandshakeTimeout != 5*time.Second || transport.ResponseHeaderTimeout != 10*time.Second {
		t.Fatalf("timeouts = total %s, TLS %s, headers %s", client.Timeout, transport.TLSHandshakeTimeout, transport.ResponseHeaderTimeout)
	}

	redirectResponse, err := client.Post(baseURL+"/redirect", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	redirectResponse.Body.Close()
	if redirectResponse.StatusCode != http.StatusFound || redirectHits != 1 || followedHits != 0 {
		t.Fatalf("redirect handling = status %d, redirect hits %d, followed hits %d", redirectResponse.StatusCode, redirectHits, followedHits)
	}
	retryResponse, err := client.Post(baseURL+"/retry", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	retryResponse.Body.Close()
	if retryResponse.StatusCode != http.StatusServiceUnavailable || retryHits != 1 {
		t.Fatalf("retry handling = status %d, hits %d", retryResponse.StatusCode, retryHits)
	}
	largeResponse, err := client.Post(baseURL+"/large", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	largeBody, readErr := io.ReadAll(largeResponse.Body)
	largeResponse.Body.Close()
	if readErr != nil || len(largeBody) != 1<<20+1 {
		t.Fatalf("large response = %d bytes, error %v", len(largeBody), readErr)
	}

	resolver.addresses = []net.IP{net.ParseIP("1.1.1.1")}
	secondClient, err := policy.ClientFor(context.Background(), baseURL, time.Second)
	if err != nil || resolver.calls != 2 {
		t.Fatalf("second request policy = calls %d, error %v", resolver.calls, err)
	}
	secondTransport := secondClient.Transport.(*http.Transport)
	var secondDial string
	policy.dialContext = func(_ context.Context, _ string, address string) (net.Conn, error) {
		secondDial = address
		return nil, errors.New("stop")
	}
	_, _ = secondTransport.DialContext(context.Background(), "tcp", "example.com:"+port)
	if secondDial != "1.1.1.1:"+port {
		t.Fatalf("second request pinned %q", secondDial)
	}
}

func TestClientResolutionHonorsCallerDeadline(t *testing.T) {
	policy, err := NewOutboundPolicyWithResolver(nil, nil, blockingResolver{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := policy.ClientFor(ctx, "https://gateway.example", 0); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("deadline resolution error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("DNS deadline took %s", elapsed)
	}
}

func TestProxyClientAddsFiveSecondResolutionDeadline(t *testing.T) {
	resolver := &deadlineObservingResolver{}
	policy, err := NewOutboundPolicyWithResolver(nil, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := policy.ClientForProxy(ctx, "https://gateway.example", 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if resolver.remaining <= 0 || resolver.remaining > 5*time.Second || resolver.remaining < 4*time.Second {
		t.Fatalf("proxy DNS deadline = %s, want an internal five-second bound", resolver.remaining)
	}
}

func TestEndpointAndAuthenticationAreProtocolSpecific(t *testing.T) {
	policy, _ := NewOutboundPolicy(nil, nil)
	gemini, err := policy.Endpoint("https://gateway.example/prefix", FormatGemini, "gemini 2.5:flash", true)
	if err != nil || !strings.Contains(gemini.EscapedPath(), "/v1beta/models/gemini%202.5:flash:streamGenerateContent") {
		t.Fatalf("Gemini endpoint = %v, %v", gemini, err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://gateway.example", nil)
	ApplyAuthentication(request, FormatAnthropic, "secret")
	if request.Header.Get("x-api-key") != "secret" || request.Header.Get("anthropic-version") != "2023-06-01" || request.Header.Get("Authorization") != "" {
		t.Fatalf("Anthropic authentication headers = %#v", request.Header)
	}
	request, _ = http.NewRequest(http.MethodPost, "https://gateway.example", nil)
	ApplyAuthentication(request, FormatOpenAIResponses, "secret")
	if request.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("OpenAI authorization = %q", request.Header.Get("Authorization"))
	}
}
