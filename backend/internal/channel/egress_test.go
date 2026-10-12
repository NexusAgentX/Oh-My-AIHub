package channel

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	publicA  = "93.184.216.34"
	publicB  = "1.1.1.1"
	publicV6 = "2606:4700:4700::1111"
)

// scriptedResolver answers every name with the addresses it is told to, counts
// the lookups, and can hold them back until released.
type scriptedResolver struct {
	mu        sync.Mutex
	addresses []net.IP
	err       error
	calls     atomic.Int64
	gate      chan struct{} // when set, a lookup waits for it to close
}

func (r *scriptedResolver) set(addresses ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addresses, r.err = nil, nil
	for _, value := range addresses {
		r.addresses = append(r.addresses, net.ParseIP(value))
	}
}

func (r *scriptedResolver) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *scriptedResolver) LookupNetIP(ctx context.Context, _, _ string) ([]net.IP, error) {
	r.calls.Add(1)
	r.mu.Lock()
	gate, addresses, err := r.gate, r.addresses, r.err
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return addresses, err
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// egressRig is a policy whose pinned dial reaches a local TLS server, with a
// scripted resolver and a clock the test moves by hand.
type egressRig struct {
	t        *testing.T
	server   *httptest.Server
	resolver *scriptedResolver
	clock    *fakeClock
	policy   *OutboundPolicy
	baseURL  string
	host     string
	port     string

	mu         sync.Mutex
	dialed     []string // the addresses the policy was asked to connect to
	opened     atomic.Int64
	closed     atomic.Int64
	streamGate chan struct{}
}

func newEgressRig(t *testing.T, http2 bool) *egressRig {
	t.Helper()
	rig := &egressRig{t: t, resolver: &scriptedResolver{}, clock: &fakeClock{now: time.Unix(1_800_000_000, 0)}, host: "example.com", streamGate: make(chan struct{})}
	rig.resolver.set(publicA)
	rig.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/followed")
			w.WriteHeader(http.StatusFound)
		case "/stream":
			_, _ = io.WriteString(w, "first;")
			w.(http.Flusher).Flush()
			<-rig.streamGate
			_, _ = io.WriteString(w, "second")
		default:
			_, _ = io.WriteString(w, "ok:"+r.Proto)
		}
	}))
	rig.server.EnableHTTP2 = http2
	rig.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			rig.opened.Add(1)
		case http.StateClosed:
			rig.closed.Add(1)
		}
	}
	rig.server.StartTLS()
	t.Cleanup(rig.server.Close)
	t.Cleanup(func() {
		select {
		case <-rig.streamGate:
		default:
			close(rig.streamGate)
		}
	})
	_, port, err := net.SplitHostPort(rig.server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rig.port = port
	rig.baseURL = "https://" + rig.host + ":" + port
	policy, err := NewOutboundPolicyWithResolver([]string{port}, nil, rig.resolver)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(rig.server.Certificate())
	policy.testRootCAs = roots
	policy.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		rig.mu.Lock()
		rig.dialed = append(rig.dialed, address)
		rig.mu.Unlock()
		if strings.HasPrefix(address, "192.0.2.1:") {
			return nil, errors.New("unreachable")
		}
		return (&net.Dialer{}).DialContext(ctx, network, rig.server.Listener.Addr().String())
	}
	policy.egress.now = rig.clock.Now
	rig.policy = policy
	return rig
}

func (r *egressRig) dials() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

// post sends a request through the pooled client of the policy and returns the
// body.
func (r *egressRig) post(path string) (string, *http.Response, error) {
	r.t.Helper()
	client, err := r.policy.ClientForGateway(context.Background(), r.baseURL)
	if err != nil {
		return "", nil, err
	}
	return r.do(client, path)
}

func (r *egressRig) do(client *http.Client, path string) (string, *http.Response, error) {
	r.t.Helper()
	request, err := http.NewRequest(http.MethodPost, r.baseURL+path, strings.NewReader(`{}`))
	if err != nil {
		r.t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return string(body), response, err
}

func (r *egressRig) mustPost(path string) (string, *http.Response) {
	r.t.Helper()
	body, response, err := r.post(path)
	if err != nil {
		r.t.Fatalf("POST %s: %v", path, err)
	}
	return body, response
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestGatewayClientReusesOneConnectionAndOneResolution(t *testing.T) {
	rig := newEgressRig(t, false)
	first, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		body, response := rig.mustPost("/v1/chat/completions")
		if body != "ok:HTTP/1.1" || response.ProtoMajor != 1 {
			t.Fatalf("request %d: %q over HTTP/%d", i, body, response.ProtoMajor)
		}
	}
	again, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil || again != first {
		t.Fatalf("the pooled client is not shared: %v", err)
	}
	if dials := rig.dials(); len(dials) != 1 || dials[0] != publicA+":"+rig.port {
		t.Fatalf("dials = %v, want one connection to the pinned address", dials)
	}
	if rig.opened.Load() != 1 {
		t.Fatalf("server accepted %d connections, want 1", rig.opened.Load())
	}
	if calls := rig.resolver.calls.Load(); calls != 1 {
		t.Fatalf("DNS lookups = %d, want 1", calls)
	}
	// 7 client lookups: the first resolved, the other 6 were answered from the cache.
	stats := rig.policy.EgressStats()
	if stats.NewConnections != 1 || stats.ReusedConnections != 4 || stats.DNSMisses != 1 || stats.DNSHits != 6 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestGatewayClientNegotiatesHTTP2(t *testing.T) {
	rig := newEgressRig(t, true)
	for i := 0; i < 3; i++ {
		body, response := rig.mustPost("/v1/messages")
		if response.ProtoMajor != 2 || body != "ok:HTTP/2.0" {
			t.Fatalf("request %d: %q over HTTP/%d", i, body, response.ProtoMajor)
		}
	}
	if dials := rig.dials(); len(dials) != 1 {
		t.Fatalf("dials = %v, want one HTTP/2 connection for all requests", dials)
	}
	if stats := rig.policy.EgressStats(); stats.NewConnections != 1 || stats.ReusedConnections != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestGatewayTransportSettings(t *testing.T) {
	rig := newEgressRig(t, false)
	client, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*countedTransport).Transport
	switch {
	case client.Timeout != 0 || transport.ResponseHeaderTimeout != 0:
		t.Fatal("the pooled client has a deadline of its own")
	case transport.Proxy != nil:
		t.Fatal("the pooled client can use an environment proxy")
	case transport.DisableKeepAlives || !transport.ForceAttemptHTTP2:
		t.Fatal("keep-alive or HTTP/2 is off")
	case transport.MaxIdleConnsPerHost != 64 || transport.IdleConnTimeout != 90*time.Second:
		t.Fatalf("pool limits = %d idle per host, %s idle timeout", transport.MaxIdleConnsPerHost, transport.IdleConnTimeout)
	case transport.MaxResponseHeaderBytes != 1<<20 || transport.TLSHandshakeTimeout != 5*time.Second:
		t.Fatal("response header or handshake limits changed")
	case transport.TLSClientConfig.MinVersion != 0x0303:
		t.Fatalf("minimum TLS version = %#x", transport.TLSClientConfig.MinVersion)
	}
	// Redirects are handed to the caller, never followed.
	_, response, err := rig.post("/redirect")
	if err != nil || response.StatusCode != http.StatusFound {
		t.Fatalf("redirect = %v, %v", response, err)
	}
}

func TestPooledDialConnectsOnlyToTheValidatedAddresses(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.resolver.set("192.0.2.1", publicA) // 192.0.2.1 is documentation space: refused
	if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("documentation address accepted: %v", err)
	}
	rig.resolver.set(publicA, publicB)
	rig.clock.advance(6 * time.Second) // the refusal is only remembered briefly
	client, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*countedTransport).Transport
	if _, err := transport.DialContext(context.Background(), "tcp", "other.example:"+rig.port); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("dial to another host: %v", err)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", rig.host+":444"); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("dial to another port: %v", err)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:"+rig.port); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("dial by IP literal: %v", err)
	}
	if dials := rig.dials(); len(dials) != 0 {
		t.Fatalf("refused dials reached the dialer: %v", dials)
	}
}

func TestPooledDialFallsBackToTheNextValidatedAddress(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.policy.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		rig.mu.Lock()
		rig.dialed = append(rig.dialed, address)
		rig.mu.Unlock()
		if strings.HasPrefix(address, publicA+":") {
			return nil, errors.New("connection refused")
		}
		return (&net.Dialer{}).DialContext(ctx, network, rig.server.Listener.Addr().String())
	}
	rig.resolver.set(publicA, publicB)
	if body, _ := rig.mustPost("/v1/responses"); body != "ok:HTTP/1.1" {
		t.Fatalf("body = %q", body)
	}
	if dials := rig.dials(); len(dials) != 2 || dials[0] != publicA+":"+rig.port || dials[1] != publicB+":"+rig.port {
		t.Fatalf("dials = %v, want the first address then the second", dials)
	}
	// A new connection starts with the address that worked, not the one that failed.
	client, _ := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	client.CloseIdleConnections()
	if body, _ := rig.mustPost("/v1/responses"); body != "ok:HTTP/1.1" {
		t.Fatalf("body = %q", body)
	}
	if dials := rig.dials(); len(dials) != 3 || dials[2] != publicB+":"+rig.port {
		t.Fatalf("dials = %v, want the second connection to go straight to the working address", dials)
	}
	if stats := rig.policy.EgressStats(); stats.NewConnections != 2 {
		t.Fatalf("a failed dial counted as a connection: %+v", stats)
	}
}

func TestDNSCacheRefreshesEverySixtySeconds(t *testing.T) {
	rig := newEgressRig(t, false)
	first, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	rig.mustPost("/a")
	rig.clock.advance(59 * time.Second)
	rig.mustPost("/a")
	if calls := rig.resolver.calls.Load(); calls != 1 {
		t.Fatalf("lookups after 59s = %d, want the cached answer", calls)
	}
	rig.clock.advance(2 * time.Second)
	rig.mustPost("/a")
	if calls := rig.resolver.calls.Load(); calls != 2 {
		t.Fatalf("lookups after 61s = %d, want a refresh", calls)
	}
	// The refresh found the same addresses: same client, same connection.
	again, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil || again != first {
		t.Fatalf("an unchanged resolution replaced the client: %v", err)
	}
	if rig.opened.Load() != 1 {
		t.Fatalf("connections = %d, want the one connection kept", rig.opened.Load())
	}
	// The same set in another order is still the same set.
	rig.resolver.set(publicA, publicB)
	rig.clock.advance(61 * time.Second)
	second, _ := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	rig.resolver.set(publicB, publicA)
	rig.clock.advance(61 * time.Second)
	third, _ := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if second == first || third != second {
		t.Fatal("a reordered answer was taken for a changed one, or a changed one for the same")
	}
}

func TestConcurrentRequestsShareOneResolution(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.resolver.gate = make(chan struct{})
	const callers = 20
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			_, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
			results <- err
		}()
	}
	waitFor(t, "every caller to wait for the lookup", func() bool { return rig.policy.EgressStats().DNSMisses == callers })
	if calls := rig.resolver.calls.Load(); calls != 1 {
		t.Fatalf("lookups = %d while %d callers wait, want 1", calls, callers)
	}
	close(rig.resolver.gate)
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls := rig.resolver.calls.Load(); calls != 1 {
		t.Fatalf("lookups = %d, want 1", calls)
	}
}

func TestAWaitingCallerLeavingDoesNotFailTheOthers(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.resolver.gate = make(chan struct{})
	leaver, leave := context.WithCancel(context.Background())
	left := make(chan error, 1)
	go func() {
		_, err := rig.policy.ClientForGateway(leaver, rig.baseURL)
		left <- err
	}()
	waitFor(t, "the first caller to start the lookup", func() bool { return rig.resolver.calls.Load() == 1 })
	stayed := make(chan error, 1)
	go func() {
		_, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
		stayed <- err
	}()
	waitFor(t, "the second caller to wait", func() bool { return rig.policy.EgressStats().DNSMisses == 2 })
	leave()
	if err := <-left; !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("leaving caller: %v", err)
	}
	close(rig.resolver.gate)
	if err := <-stayed; err != nil {
		t.Fatalf("the caller that stayed failed with the one that left: %v", err)
	}
}

func TestResolutionFailureIsNotCached(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.resolver.fail(errors.New("SERVFAIL"))
	for i := 0; i < 2; i++ {
		if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("failed lookup %d: %v", i, err)
		}
	}
	if calls := rig.resolver.calls.Load(); calls != 2 {
		t.Fatalf("lookups = %d, a failure must not be remembered", calls)
	}
	rig.resolver.set(publicA)
	if body, _ := rig.mustPost("/v1/chat/completions"); body == "" {
		t.Fatal("no answer once DNS recovered")
	}
}

func TestMixedPublicAndPrivateAnswerIsRefusedAndTheRefusalIsRememberedBriefly(t *testing.T) {
	rig := newEgressRig(t, false)
	for _, private := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "::1", "fd00::1"} {
		rig.resolver.set(publicA, private)
		rig.clock.advance(time.Minute)
		if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("answer with %s accepted: %v", private, err)
		}
	}
	if dials := rig.dials(); len(dials) != 0 {
		t.Fatalf("a refused answer was dialed: %v", dials)
	}

	rig.resolver.set(publicA, "10.0.0.5")
	rig.clock.advance(time.Minute)
	before := rig.resolver.calls.Load()
	for i := 0; i < 3; i++ {
		if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatal("mixed answer accepted")
		}
	}
	if calls := rig.resolver.calls.Load() - before; calls != 1 {
		t.Fatalf("refusal looked up %d times, want it remembered", calls)
	}
	// After five seconds the name is looked at again, and the fixed answer is used.
	rig.resolver.set(publicA)
	rig.clock.advance(5*time.Second + time.Millisecond)
	if body, _ := rig.mustPost("/v1/chat/completions"); body == "" {
		t.Fatal("no answer after the refusal expired")
	}
}

func TestDNSRebindingCannotRedirectConnectionsToAProtectedAddress(t *testing.T) {
	rig := newEgressRig(t, false)
	port := rig.port

	// A stream is established to the address the name had when it was checked.
	oldClient, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, rig.baseURL+"/stream", strings.NewReader(`{}`))
	response, err := oldClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	chunk := make([]byte, len("first;"))
	if _, err := io.ReadFull(response.Body, chunk); err != nil || string(chunk) != "first;" {
		t.Fatalf("first chunk = %q, %v", chunk, err)
	}

	// The name moves to another public address: connections made from now on go
	// there, and the stream in progress is not disturbed.
	rig.resolver.set(publicB)
	rig.clock.advance(61 * time.Second)
	newClient, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil || newClient == oldClient {
		t.Fatalf("client after the address changed: same=%v, %v", newClient == oldClient, err)
	}
	if body, _, err := rig.do(newClient, "/v1/chat/completions"); err != nil || body != "ok:HTTP/1.1" {
		t.Fatalf("request on the new address: %q, %v", body, err)
	}
	if dials := rig.dials(); len(dials) != 2 || dials[0] != publicA+":"+port || dials[1] != publicB+":"+port {
		t.Fatalf("dials = %v, want the old address and then the new one", dials)
	}

	// Then it flips to a private address: refused, and nothing is dialed.
	rig.resolver.set("10.0.0.5")
	rig.clock.advance(61 * time.Second)
	if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("rebinding to a private address: %v", err)
	}
	rig.resolver.set("127.0.0.1")
	rig.clock.advance(61 * time.Second)
	if _, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("rebinding to loopback: %v", err)
	}
	for _, dialed := range rig.dials() {
		if !strings.HasPrefix(dialed, publicA+":") && !strings.HasPrefix(dialed, publicB+":") {
			t.Fatalf("dialed %s", dialed)
		}
	}
	// The connection that was established before still carries its stream to the end.
	close(rig.streamGate)
	rest, err := io.ReadAll(response.Body)
	if err != nil || string(rest) != "second" {
		t.Fatalf("rest of the established stream = %q, %v", rest, err)
	}
	// Idle connections of a host that no longer resolves safely are closed.
	waitFor(t, "the idle connection to be closed", func() bool { return rig.closed.Load() >= 1 })
}

func TestChangedBlockListClosesThePoolAndRefusesTheHost(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.mustPost("/v1/chat/completions")
	if rig.opened.Load() != 1 || rig.closed.Load() != 0 {
		t.Fatalf("connections opened %d closed %d", rig.opened.Load(), rig.closed.Load())
	}
	before, err := rig.policy.WithExtraBlockedHosts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rig.closed.Load() != 0 {
		t.Fatal("an unchanged block list closed the pool")
	}
	blocked, err := rig.policy.WithExtraBlockedHosts([]string{rig.host})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.(*OutboundPolicy).ClientForGateway(context.Background(), rig.baseURL); !errors.Is(err, ErrUnsafeUpstream) {
		t.Fatalf("blocked host: %v", err)
	}
	waitFor(t, "the pooled connection to be closed", func() bool { return rig.closed.Load() == 1 })

	// A snapshot built from the old list still names the host as allowed. It
	// must not repopulate the pool: it connects the way discovery does.
	stale := before.(*OutboundPolicy)
	for i := 0; i < 3; i++ {
		client, err := stale.ClientForGateway(context.Background(), rig.baseURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := client.Transport.(*http.Transport); !ok {
			t.Fatal("a policy of an outdated block list got a pooled client")
		}
		if body, _, err := rig.do(client, "/v1/chat/completions"); err != nil || body == "" {
			t.Fatalf("one-shot request: %q, %v", body, err)
		}
	}
	if rig.opened.Load() != 4 {
		t.Fatalf("connections = %d, want one per one-shot request", rig.opened.Load())
	}
	if len(rig.policy.egress.hosts) != 0 {
		t.Fatal("the pool holds a client of an outdated block list")
	}

	// Lifting the block brings the pool back.
	lifted, _ := rig.policy.WithExtraBlockedHosts(nil)
	client, err := lifted.(*OutboundPolicy).ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Transport.(*countedTransport); !ok {
		t.Fatal("pool did not come back after the block was lifted")
	}
}

func TestFormatTestClientResolvesAnewAndKeepsNoConnection(t *testing.T) {
	rig := newEgressRig(t, false)
	for i := 0; i < 3; i++ {
		client, err := rig.policy.FreshClient(context.Background(), rig.baseURL)
		if err != nil {
			t.Fatal(err)
		}
		transport := client.Transport.(*http.Transport)
		if !transport.DisableKeepAlives || client.Timeout != 0 {
			t.Fatal("the format test client is pooled or has a timeout")
		}
		if body, _, err := rig.do(client, "/v1/chat/completions"); err != nil || body == "" {
			t.Fatalf("request: %q, %v", body, err)
		}
	}
	if calls := rig.resolver.calls.Load(); calls != 3 {
		t.Fatalf("lookups = %d, want one per client", calls)
	}
	if rig.opened.Load() != 3 {
		t.Fatalf("connections = %d, want one per request", rig.opened.Load())
	}
	if stats := rig.policy.EgressStats(); stats != (EgressStats{}) {
		t.Fatalf("the one-shot client touched the pool counters: %+v", stats)
	}
	if len(rig.policy.egress.hosts) != 0 || len(rig.policy.egress.dns) != 0 {
		t.Fatal("the one-shot client filled the pool or the DNS cache")
	}
}

func TestNonCanonicalOrBlockedBaseURLNeverReachesThePool(t *testing.T) {
	rig := newEgressRig(t, false)
	policy, err := rig.policy.WithExtraBlockedHosts([]string{"blocked.example"})
	if err != nil {
		t.Fatal(err)
	}
	rig.policy = policy.(*OutboundPolicy)
	for _, raw := range []string{
		rig.baseURL + "/",
		strings.ToUpper(rig.baseURL),
		"http://example.com",
		"https://127.0.0.1",
		"https://blocked.example",
		"https://example.com:444",
	} {
		if _, err := rig.policy.ClientForGateway(context.Background(), raw); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("ClientForGateway(%q) = %v", raw, err)
		}
	}
	if calls := rig.resolver.calls.Load(); calls != 0 {
		t.Fatalf("a refused URL cost %d lookups", calls)
	}
}

func TestIdleHostsAndStaleResolutionsAreSwept(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.mustPost("/v1/chat/completions")
	if len(rig.policy.egress.hosts) != 1 || len(rig.policy.egress.dns) != 1 {
		t.Fatalf("pool = %d hosts, %d resolutions", len(rig.policy.egress.hosts), len(rig.policy.egress.dns))
	}
	rig.clock.advance(12 * time.Minute)
	// Any request for another host triggers the sweep.
	rig.resolver.set(publicB)
	other, err := rig.policy.ClientForGateway(context.Background(), "https://other.example:"+rig.port)
	if err != nil || other == nil {
		t.Fatal(err)
	}
	rig.policy.egress.mu.Lock()
	defer rig.policy.egress.mu.Unlock()
	if _, kept := rig.policy.egress.hosts[hostKey{rig.host, rig.port}]; kept {
		t.Fatal("a host unused for ten minutes was kept")
	}
	if _, kept := rig.policy.egress.dns[rig.host]; kept {
		t.Fatal("a resolution expired for ten minutes was kept")
	}
	if len(rig.policy.egress.hosts) != 1 {
		t.Fatalf("hosts after the sweep = %d", len(rig.policy.egress.hosts))
	}
}

func TestPoolSizeIsBoundedByDroppingTheLeastRecentlyUsedHost(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.policy.egress.maxHosts = 2
	port := rig.port
	for _, host := range []string{"a.example", "b.example", "c.example"} {
		if _, err := rig.policy.ClientForGateway(context.Background(), "https://"+host+":"+port); err != nil {
			t.Fatal(err)
		}
		rig.clock.advance(time.Second)
	}
	rig.policy.egress.mu.Lock()
	defer rig.policy.egress.mu.Unlock()
	if len(rig.policy.egress.hosts) != 2 {
		t.Fatalf("hosts = %d, want the cap of 2", len(rig.policy.egress.hosts))
	}
	if _, kept := rig.policy.egress.hosts[hostKey{"a.example", port}]; kept {
		t.Fatal("the least recently used host was kept")
	}
}

func TestSameAddresses(t *testing.T) {
	a, b, c := netip.MustParseAddr(publicA), netip.MustParseAddr(publicB), netip.MustParseAddr(publicV6)
	for _, tc := range []struct {
		x, y []netip.Addr
		same bool
	}{
		{[]netip.Addr{a, b}, []netip.Addr{b, a}, true},
		{[]netip.Addr{a}, []netip.Addr{a}, true},
		{[]netip.Addr{a, b}, []netip.Addr{a}, false},
		{[]netip.Addr{a, b}, []netip.Addr{a, c}, false},
		{nil, nil, true},
	} {
		if sameAddresses(tc.x, tc.y) != tc.same {
			t.Errorf("sameAddresses(%v, %v) != %v", tc.x, tc.y, tc.same)
		}
	}
}

func TestPolicyWithoutAPoolFallsBackToOneShotClients(t *testing.T) {
	rig := newEgressRig(t, false)
	rig.policy.egress = nil
	client, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatal("a policy without a pool returned a pooled client")
	}
	if stats := rig.policy.EgressStats(); stats != (EgressStats{}) {
		t.Fatalf("stats = %+v", stats)
	}
}

// Streams are multiplexed on one HTTP/2 connection; cancelling one of them (a
// call that hit its limit, or a client that left) resets that stream only.
func TestCancellingOneHTTP2StreamLeavesTheOthersRunning(t *testing.T) {
	rig := newEgressRig(t, true)
	client, err := rig.policy.ClientForGateway(context.Background(), rig.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	type stream struct {
		cancel   context.CancelFunc
		response *http.Response
	}
	var streams []stream
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, rig.baseURL+"/stream", strings.NewReader(`{}`))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.ProtoMajor != 2 {
			t.Fatalf("stream %d runs over HTTP/%d", i, response.ProtoMajor)
		}
		chunk := make([]byte, len("first;"))
		if _, err := io.ReadFull(response.Body, chunk); err != nil {
			t.Fatal(err)
		}
		streams = append(streams, stream{cancel, response})
		t.Cleanup(func() { cancel(); response.Body.Close() })
	}
	streams[0].cancel()
	if _, err := io.ReadAll(streams[0].response.Body); err == nil {
		t.Fatal("a cancelled stream ended without an error")
	}
	close(rig.streamGate)
	for i := 1; i < 3; i++ {
		rest, err := io.ReadAll(streams[i].response.Body)
		if err != nil || string(rest) != "second" {
			t.Fatalf("stream %d after another was cancelled: %q, %v", i, rest, err)
		}
	}
	if dials := rig.dials(); len(dials) != 1 {
		t.Fatalf("dials = %v, want one connection for all streams", dials)
	}
}
