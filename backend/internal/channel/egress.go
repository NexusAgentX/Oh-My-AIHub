package channel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// The gateway's forwarding path does not make a client per request: it shares
// one pooled client per host and port, and takes the addresses it dials from a
// small DNS cache (ADR-0036 rule 7). Both are built so that sharing and caching
// weaken none of the outbound checks:
//
//   - The cache holds only addresses that passed the protected-address check.
//     An answer that contains a protected address is not cached as an answer at
//     all; the refusal is, for a few seconds.
//   - A connection is dialed only to an address of the cache entry its client
//     was built from, by IP, with TLS verifying the certificate against the name
//     of the URL. A name that later resolves somewhere else cannot redirect an
//     established connection, and a pooled client whose host no longer resolves
//     to the same set of addresses is replaced rather than reused.
//   - Every request still normalizes its Base URL against the current block
//     list before it gets here, and a changed block list closes every pooled
//     connection.
//
// Discovery and format tests keep making a client per call (clientFor).

const (
	// dnsCacheTTL is how long a validated resolution is used before the name is
	// resolved again. The standard resolver does not report TTLs, so it is fixed.
	dnsCacheTTL = 60 * time.Second
	// dnsRejectionTTL is how long the refusal of an answer with a protected
	// address is remembered, so that a name pointing at an internal address does
	// not send every request to the resolver. A failed lookup is not cached.
	dnsRejectionTTL = 5 * time.Second
	// dnsLookupTimeout bounds one resolution, which is shared by every caller
	// waiting for the same name and so does not depend on any one of them.
	dnsLookupTimeout = 5 * time.Second

	dialTimeout            = 5 * time.Second
	tlsHandshakeTimeout    = 5 * time.Second
	maxResponseHeaderBytes = 1 << 20

	egressKeepAlive      = 30 * time.Second
	egressIdleTimeout    = 90 * time.Second
	egressMaxIdlePerHost = 64
	// An HTTP/2 connection that has received nothing for this long is pinged and
	// closed if the ping stays unanswered, so that a connection a middlebox has
	// silently dropped does not hold requests until their own timeouts.
	egressPingAfter   = 30 * time.Second
	egressPingTimeout = 15 * time.Second

	// A host that has not been used for this long loses its pooled client and
	// its cache entry; sweeping happens at most once per egressSweepEvery.
	egressHostIdle   = 10 * time.Minute
	egressSweepEvery = time.Minute
	// egressMaxHosts bounds the pooled hosts; the least recently used goes first.
	egressMaxHosts = 1024
)

// EgressStats counts what the gateway's pooled outbound connections and DNS
// cache have done since the process started.
type EgressStats struct {
	NewConnections    uint64 // connections dialed
	ReusedConnections uint64 // requests that went out on an already open connection
	DNSHits           uint64 // lookups answered from the cache
	DNSMisses         uint64 // lookups that needed (or joined) a resolution
}

// EgressStats reports the counters of the gateway's connection pool.
func (p *OutboundPolicy) EgressStats() EgressStats {
	if p.egress == nil {
		return EgressStats{}
	}
	return EgressStats{
		NewConnections:    p.egress.newConns.Load(),
		ReusedConnections: p.egress.reusedConns.Load(),
		DNSHits:           p.egress.dnsHits.Load(),
		DNSMisses:         p.egress.dnsMisses.Load(),
	}
}

type hostKey struct{ host, port string }

// egressPool is the DNS cache and the pooled clients of one OutboundPolicy and
// all copies made from it.
type egressPool struct {
	now func() time.Time

	newConns, reusedConns, dnsHits, dnsMisses atomic.Uint64

	mu         sync.Mutex
	blockedKey string
	maxHosts   int
	swept      time.Time
	dns        map[string]*dnsEntry
	flights    map[string]*dnsFlight
	hosts      map[hostKey]*pooledHost
}

func newEgressPool(blockedKey string) *egressPool {
	return &egressPool{
		now: time.Now, blockedKey: blockedKey, maxHosts: egressMaxHosts,
		dns: map[string]*dnsEntry{}, flights: map[string]*dnsFlight{}, hosts: map[hostKey]*pooledHost{},
	}
}

// dnsEntry is one resolution of a name: validated addresses, or the refusal of
// the answer. It is replaced, never changed.
type dnsEntry struct {
	addrs   []netip.Addr
	err     error
	expires time.Time
}

// dnsFlight is a resolution in progress that every caller of the same name waits for.
type dnsFlight struct {
	done  chan struct{}
	entry *dnsEntry
	err   error
}

// pooledHost is the client of one host and port, built for one resolution.
type pooledHost struct {
	client    *http.Client
	transport *http.Transport
	entry     *dnsEntry
	addrs     []netip.Addr
	lastUsed  time.Time
}

// setBlocked moves the pool to a block list. When it differs from the one the
// pool has been serving, every pooled connection is closed and every pooled
// client dropped: connections that were allowed under the old list may point at
// a host the new list forbids.
func (e *egressPool) setBlocked(key string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.blockedKey == key {
		e.mu.Unlock()
		return
	}
	e.blockedKey = key
	closing := e.takeAllHostsLocked()
	e.mu.Unlock()
	closeIdle(closing)
}

func (e *egressPool) takeAllHostsLocked() []*http.Transport {
	closing := make([]*http.Transport, 0, len(e.hosts))
	for key, host := range e.hosts {
		closing = append(closing, host.transport)
		delete(e.hosts, key)
	}
	return closing
}

func closeIdle(transports []*http.Transport) {
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

// client returns the pooled client of host and port for the policy p. It
// returns a nil client without an error when p is not served from the pool: it
// has none, or it was built from a block list the pool has moved past.
func (e *egressPool) client(ctx context.Context, p *OutboundPolicy, host, port string) (*http.Client, error) {
	if e == nil {
		return nil, nil
	}
	key := hostKey{host, port}
	now := e.now()
	e.mu.Lock()
	if e.blockedKey != p.blockedKey {
		e.mu.Unlock()
		return nil, nil
	}
	closing := e.sweepLocked(now)
	if entry := e.dns[host]; entry != nil && now.Before(entry.expires) {
		var client *http.Client
		if entry.err == nil {
			var replaced []*http.Transport
			client, replaced = e.hostLocked(p, key, entry, now)
			closing = append(closing, replaced...)
		}
		e.mu.Unlock()
		closeIdle(closing)
		e.dnsHits.Add(1)
		if entry.err != nil {
			return nil, entry.err
		}
		return client, nil
	}
	flight := e.flights[host]
	leader := flight == nil
	if leader {
		flight = &dnsFlight{done: make(chan struct{})}
		e.flights[host] = flight
	}
	e.mu.Unlock()
	closeIdle(closing)
	e.dnsMisses.Add(1)
	if leader {
		// The resolution outlives the caller that started it: the others
		// waiting for the same name must not fail because that one left.
		go e.resolve(p.resolver, host, flight, context.WithoutCancel(ctx))
	}
	select {
	case <-flight.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: DNS resolution failed", ErrUnsafeUpstream)
	}
	if flight.err != nil {
		return nil, flight.err
	}
	now = e.now()
	e.mu.Lock()
	if e.blockedKey != p.blockedKey { // the block list changed while waiting
		e.mu.Unlock()
		return nil, nil
	}
	client, replaced := e.hostLocked(p, key, flight.entry, now)
	e.mu.Unlock()
	closeIdle(replaced)
	return client, nil
}

// resolve resolves host, validates the answer and publishes the outcome to the
// cache and to everyone waiting on the flight.
func (e *egressPool) resolve(resolver Resolver, host string, flight *dnsFlight, ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addresses, err := validatedLookup(ctx, resolver, host)
	now := e.now()
	var closing []*http.Transport
	e.mu.Lock()
	delete(e.flights, host)
	switch {
	case err == nil:
		flight.entry = &dnsEntry{addrs: addresses, expires: now.Add(dnsCacheTTL)}
		e.dns[host] = flight.entry
	case errors.Is(err, errProtectedAddress):
		flight.err = err
		e.dns[host] = &dnsEntry{err: err, expires: now.Add(dnsRejectionTTL)}
		closing = e.takeHostLocked(host)
	default:
		flight.err = err
		delete(e.dns, host)
		closing = e.takeHostLocked(host)
	}
	e.mu.Unlock()
	closeIdle(closing)
	close(flight.done)
}

// takeHostLocked drops every pooled client of host: a name that cannot be
// resolved safely is not served from connections made before.
func (e *egressPool) takeHostLocked(host string) []*http.Transport {
	var closing []*http.Transport
	for key, pooled := range e.hosts {
		if key.host == host {
			closing = append(closing, pooled.transport)
			delete(e.hosts, key)
		}
	}
	return closing
}

// hostLocked returns the pooled client of key for the resolution entry. A client
// built for a different set of addresses is replaced; the transports to close
// the idle connections of are returned. Connections of a replaced client that
// are in use finish on it, but nothing new is sent through it.
func (e *egressPool) hostLocked(p *OutboundPolicy, key hostKey, entry *dnsEntry, now time.Time) (*http.Client, []*http.Transport) {
	var closing []*http.Transport
	pooled := e.hosts[key]
	if pooled != nil && pooled.entry != entry {
		if sameAddresses(pooled.addrs, entry.addrs) {
			pooled.entry = entry
		} else {
			closing = append(closing, pooled.transport)
			delete(e.hosts, key)
			pooled = nil
		}
	}
	if pooled == nil {
		pooled = e.newHost(p, key, entry)
		e.hosts[key] = pooled
		for len(e.hosts) > e.maxHosts {
			if evicted := e.evictOldestLocked(key); evicted != nil {
				closing = append(closing, evicted)
			}
		}
	}
	pooled.lastUsed = now
	return pooled.client, closing
}

// evictOldestLocked removes the least recently used host other than keep.
func (e *egressPool) evictOldestLocked(keep hostKey) *http.Transport {
	var oldest hostKey
	var found bool
	for key, pooled := range e.hosts {
		if key == keep {
			continue
		}
		if !found || pooled.lastUsed.Before(e.hosts[oldest].lastUsed) {
			oldest, found = key, true
		}
	}
	if !found {
		return nil
	}
	transport := e.hosts[oldest].transport
	delete(e.hosts, oldest)
	return transport
}

// sweepLocked drops hosts and resolutions that have not been used for a long
// time, at most once per egressSweepEvery.
func (e *egressPool) sweepLocked(now time.Time) []*http.Transport {
	if now.Sub(e.swept) < egressSweepEvery {
		return nil
	}
	e.swept = now
	var closing []*http.Transport
	for key, pooled := range e.hosts {
		if now.Sub(pooled.lastUsed) > egressHostIdle {
			closing = append(closing, pooled.transport)
			delete(e.hosts, key)
		}
	}
	for host, entry := range e.dns {
		if now.Sub(entry.expires) > egressHostIdle {
			delete(e.dns, host)
		}
	}
	return closing
}

func sameAddresses(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, netip.Addr.Compare)
	slices.SortFunc(b, netip.Addr.Compare)
	return slices.Equal(a, b)
}

// newHost builds the client of one host and port for one resolution.
func (e *egressPool) newHost(p *OutboundPolicy, key hostKey, entry *dnsEntry) *pooledHost {
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      false,
		ForceAttemptHTTP2:      true, // a custom DialContext switches HTTP/2 off unless forced
		MaxIdleConnsPerHost:    egressMaxIdlePerHost,
		IdleConnTimeout:        egressIdleTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		// No ResponseHeaderTimeout and no client timeout: the gateway bounds each
		// exchange through the request context (ADR-0035 rule 5).
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.testRootCAs},
		DialContext:     p.pinnedDial(key.host, key.port, entry.addrs, &e.newConns),
		HTTP2:           &http.HTTP2Config{SendPingTimeout: egressPingAfter, PingTimeout: egressPingTimeout},
	}
	return &pooledHost{
		client: &http.Client{
			Transport:     &countedTransport{Transport: transport, reused: &e.reusedConns},
			CheckRedirect: noRedirect,
		},
		transport: transport, entry: entry, addrs: entry.addrs,
	}
}

// countedTransport counts the requests that went out on an open connection.
type countedTransport struct {
	*http.Transport
	reused *atomic.Uint64
}

func (t *countedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if info.Reused {
			t.reused.Add(1)
		}
	}}
	return t.Transport.RoundTrip(request.WithContext(httptrace.WithClientTrace(request.Context(), trace)))
}
