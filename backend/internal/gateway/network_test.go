package gateway

import (
	"crypto/tls"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/geolocation"
	"net"
	"net/http/httptrace"
	"testing"
	"time"
)

type endpointConn struct {
	net.Conn
	address net.Addr
}

func (c endpointConn) RemoteAddr() net.Addr { return c.address }
func TestNetworkTraceFreezesActualPeerAndOverlappingPhases(t *testing.T) {
	origin := time.Unix(0, 0)
	now := origin
	trace := &networkTrace{origin: origin, start: origin, now: func() time.Time { return now }}
	hooks := trace.trace()
	now = origin.Add(2 * time.Millisecond)
	hooks.TLSHandshakeStart()
	now = origin.Add(4 * time.Millisecond)
	hooks.TLSHandshakeDone(tls.ConnectionState{}, nil)
	now = origin.Add(5 * time.Millisecond)
	hooks.GotConn(httptrace.GotConnInfo{Conn: endpointConn{address: &net.TCPAddr{IP: net.ParseIP("2001:4860:4860::8888"), Port: 443}}, Reused: true})
	now = origin.Add(6 * time.Millisecond)
	hooks.WroteRequest(httptrace.WroteRequestInfo{})
	now = origin.Add(20 * time.Millisecond)
	hooks.GotFirstResponseByte()
	node := &NetworkAttempt{Phases: []NetworkPhase{}}
	record := Attempt{}
	trace.freeze(now, &record, node)
	if node.Node.IP != "2001:4860:4860::8888" || record.ConnectMS == nil || *record.ConnectMS != 5 || record.ReusedConnection == nil || !*record.ReusedConnection {
		t.Fatalf("peer or connection lost: %+v %+v", node, record)
	}
	if len(node.Phases) != 4 || node.Phases[1].Stage != "tls" || node.Phases[1].OffsetMS != 2 || node.Phases[1].DurationMS != 2 || node.Phases[3].DurationMS != 14 {
		t.Fatalf("phases: %+v", node.Phases)
	}
	hooks.GotConn(httptrace.GotConnInfo{})
	if trace.ip != node.Node.IP || !trace.hasConnection || !trace.reused {
		t.Fatal("late callback mutated frozen record")
	}
}
func TestNetworkFailureDoesNotInventPeerOrConnectionReuse(t *testing.T) {
	at := time.Unix(0, 0)
	trace := &networkTrace{origin: at, start: at, now: func() time.Time { return at }}
	record := Attempt{}
	node := &NetworkAttempt{Phases: []NetworkPhase{}}
	trace.freeze(at.Add(time.Second), &record, node)
	if node.Node.IP != "" || record.ReusedConnection != nil || record.ConnectMS != nil || node.Phases[0].DurationMS != 1000 {
		t.Fatal("failed connection facts fabricated")
	}
}
func TestRepeatedStageIsAggregateInsteadOfFalseContinuousSpan(t *testing.T) {
	at := time.Unix(0, 0)
	call := &callState{started: at}
	call.stage("routing", at, at.Add(time.Millisecond))
	if call.diagnostics.Stages[0].OffsetMS == nil {
		t.Fatal("missing phase offset")
	}
	call.stage("routing", at.Add(time.Second), at.Add(time.Second+time.Millisecond))
	if s := call.diagnostics.Stages[0]; s.OffsetMS != nil || s.DurationMS != 2 {
		t.Fatal("disjoint work turned into a continuous span")
	}
}

func TestGatewayNodeManualOverrideAndUnknownSnapshot(t *testing.T) {
	automatic := &geolocation.Location{Label: "Automatic", Latitude: 1, Longitude: 2}
	manual := &geolocation.Location{Label: "Configured", Latitude: 3, Longitude: 4}
	snapshot := geolocation.Entry{IP: "8.8.8.8", Location: automatic}
	if node := gatewaySnapshot(snapshot, nil); node.IP != "8.8.8.8" || node.Location != automatic {
		t.Fatal("automatic node not used")
	}
	if node := gatewaySnapshot(snapshot, manual); node.IP != "8.8.8.8" || node.Location != manual {
		t.Fatal("manual region did not override automatic region")
	}
	if node := gatewaySnapshot(geolocation.Entry{}, manual); node.IP != "" || node.Location != manual {
		t.Fatal("manual region requires discovery")
	}
	if node := (&Engine{}).gatewayNode(); node.IP != "" || node.Location != nil {
		t.Fatal("disabled discovery fabricated a node")
	}
	if n := testing.AllocsPerRun(100, func() { gatewaySnapshot(snapshot, manual) }); n != 0 {
		t.Fatalf("gateway node construction allocated %v", n)
	}
}
