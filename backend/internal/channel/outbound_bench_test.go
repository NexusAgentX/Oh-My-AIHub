package channel

import (
	"context"
	"net"
	"testing"
)

// The production cache-hit path must not gain DNS, connection, or allocation
// work when the development-only localhost option is introduced.
func BenchmarkGatewayClientCached(b *testing.B) {
	resolver := &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}}
	policy, err := NewOutboundPolicyWithResolver(nil, nil, resolver)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if _, err := policy.GatewayClient(ctx, "https://gateway.example"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := policy.GatewayClient(ctx, "https://gateway.example"); err != nil {
			b.Fatal(err)
		}
	}
}
