package channel

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGatewayClientsHaveNoDeadlineOfTheirOwn(t *testing.T) {
	const probeTimeout = 10 * time.Second
	policy, err := NewOutboundPolicyWithResolver(nil, nil, &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}})
	if err != nil {
		t.Fatal(err)
	}
	// Client.Timeout would cut off a stream that is still producing output; the
	// caller bounds the exchange through the request context.
	pooled, err := policy.GatewayClient(context.Background(), "https://relay.example")
	if err != nil {
		t.Fatal(err)
	}
	if pooled.Timeout != 0 || pooled.Transport.(*countedTransport).Transport.ResponseHeaderTimeout != 0 {
		t.Fatal("unexpected pooled client deadlines")
	}
	fresh, err := policy.FreshClient(context.Background(), "https://relay.example")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Timeout != 0 || fresh.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
		t.Fatal("unexpected format test client deadlines")
	}
	discovery, err := policy.Client(context.Background(), "https://relay.example", probeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Timeout != probeTimeout || discovery.Transport.(*http.Transport).ResponseHeaderTimeout != 10*time.Second {
		t.Fatal("discovery deadlines changed")
	}
}
