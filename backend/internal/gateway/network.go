package gateway

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptrace"
	"net/netip"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/geolocation"
)

// Network is private call metadata, never event labels or channel-owner data.
// Stored inside the existing diagnostic JSON in the same final UPDATE.
type Network struct {
	Client         NetworkNode       `json:"client"`
	Gateway        NetworkNode       `json:"gateway"`
	Attempts       []*NetworkAttempt `json:"attempts"`
	LocationSource string            `json:"location_source"`
}
type NetworkNode struct {
	IP       string                `json:"ip"`
	Location *geolocation.Location `json:"location"`
}
type NetworkAttempt struct {
	Node   NetworkNode    `json:"node"`
	Phases []NetworkPhase `json:"phases"`
}
type NetworkPhase struct {
	Stage      string  `json:"stage"`
	OffsetMS   float64 `json:"offset_ms"`
	DurationMS float64 `json:"duration_ms"`
}

func phase(stage string, origin, start, end time.Time) NetworkPhase {
	return NetworkPhase{stage, elapsedMS(start.Sub(origin)), elapsedMS(end.Sub(start))}
}

// Microsecond precision is sufficient for a request replay. It avoids storing
// floating-point rounding tails in the JSON/WAL of every completed call.
func elapsedMS(duration time.Duration) float64 { return max(0, float64(duration.Microseconds())/1000) }

// Trace callbacks may outlive Do or run concurrently. Freeze copies only the
// bounded facts known when Do returns; late callbacks cannot mutate stored data.
// No callbacks are installed on body/frame reads.
type networkTrace struct {
	mu                                                          sync.Mutex
	frozen                                                      bool
	now                                                         func() time.Time
	origin, start, connected, tlsStart, tlsDone, wrote, headers time.Time
	ip                                                          string
	reused                                                      bool
	hasConnection                                               bool
}

func (t *networkTrace) update(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.frozen {
		f()
	}
}
func (t *networkTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.update(func() {
				t.connected = t.now()
				t.reused, t.hasConnection = info.Reused, true
				if info.Conn != nil && info.Conn.RemoteAddr() != nil {
					if address, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok {
						t.ip = address.AddrPort().Addr().Unmap().String()
						return
					}
					host, _, err := net.SplitHostPort(info.Conn.RemoteAddr().String())
					if err == nil {
						if ip, err := netip.ParseAddr(host); err == nil {
							t.ip = ip.Unmap().String()
						}
					}
				}
			})
		},
		TLSHandshakeStart: func() { t.update(func() { t.tlsStart = t.now() }) },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { t.update(func() { t.tlsDone = t.now() }) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.update(func() {
				if info.Err == nil {
					t.wrote = t.now()
				}
			})
		},
		GotFirstResponseByte: func() { t.update(func() { t.headers = t.now() }) },
	}
}
func (t *networkTrace) freeze(end time.Time, record *Attempt, node *NetworkAttempt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frozen = true
	if t.hasConnection {
		record.ReusedConnection = &t.reused
	}
	if !t.connected.IsZero() {
		record.ConnectMS = millis(t.connected.Sub(t.origin))
	}
	if node == nil {
		return
	}
	node.Node.IP = t.ip
	connEnd := t.connected
	if connEnd.IsZero() {
		connEnd = end
	}
	node.Phases = append(node.Phases, phase("connection", t.origin, t.start, connEnd))
	if !t.tlsStart.IsZero() && !t.tlsDone.IsZero() {
		node.Phases = append(node.Phases, phase("tls", t.origin, t.tlsStart, t.tlsDone))
	}
	if !t.connected.IsZero() && !t.wrote.IsZero() {
		node.Phases = append(node.Phases, phase("send", t.origin, t.connected, t.wrote))
	}
	if !t.wrote.IsZero() && !t.headers.IsZero() {
		node.Phases = append(node.Phases, phase("headers", t.origin, t.wrote, t.headers))
	}
}

func (e *Engine) resolveNetwork(n *Network) {
	if n == nil {
		return
	}
	n.Client.Location = e.Geo.Lookup(n.Client.IP)
	for _, attempt := range n.Attempts {
		attempt.Node.Location = e.Geo.Lookup(attempt.Node.IP)
	}
}

func withNetworkTrace(ctx context.Context, t *networkTrace) context.Context {
	return httptrace.WithClientTrace(ctx, t.trace())
}

func (e *Engine) scheduleNetwork(id string, n *Network) {
	if e.Geo == nil || n == nil {
		return
	}
	var addresses []string
	if n.Client.Location == nil && geolocation.PublicIP(n.Client.IP) {
		addresses = append(addresses, n.Client.IP)
	}
	for _, a := range n.Attempts {
		if a.Node.Location == nil && geolocation.PublicIP(a.Node.IP) {
			addresses = append(addresses, a.Node.IP)
		}
	}
	e.Geo.Offer(geolocation.Job{CallID: id, Addresses: addresses})
}

// gatewayNode is read once when opening the request. Later refreshes cannot
// move an already captured call to a different egress address or region.
func (e *Engine) gatewayNode() NetworkNode {
	return gatewaySnapshot(e.Geo.Gateway(), e.GatewayLocation)
}
func gatewaySnapshot(snapshot geolocation.Entry, override *geolocation.Location) NetworkNode {
	node := NetworkNode{IP: snapshot.IP, Location: snapshot.Location}
	if override != nil {
		node.Location = override
	}
	return node
}
