package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

// A settings refresh that changes extra_blocked_hosts must reach the pooled
// outbound connections (ADR-0036 rule 7): the host is refused from the next
// request on, and a snapshot of the old list is not served from the pool.
func TestRefreshedBlockListIsAppliedToTheEgressPool(t *testing.T) {
	policy, err := channel.NewOutboundPolicyWithResolver(nil, nil, publicResolver{})
	if err != nil {
		t.Fatal(err)
	}
	source := newGatedSource(settings.Settings{DefaultMaxAttempts: 1})
	engine := NewEngine(Dependencies{
		Settings: settings.NewService(settingsOnlyStore{source: source}),
		Outbound: policy,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx := context.Background()
	_, before, err := engine.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pooled, err := before.GatewayClient(ctx, "https://relay.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, oneShot := pooled.Transport.(*http.Transport); oneShot {
		t.Fatal("the gateway client is not the shared pooled client")
	}

	source.set(settings.Settings{DefaultMaxAttempts: 1, ExtraBlockedHosts: []string{"relay.example"}}, nil)
	if _, err := engine.loadSettings(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, after, err := engine.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := after.GatewayClient(ctx, "https://relay.example"); !errors.Is(err, channel.ErrUnsafeUpstream) {
		t.Fatalf("a newly blocked host: %v", err)
	}
	stale, err := before.GatewayClient(ctx, "https://relay.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, oneShot := stale.Transport.(*http.Transport); !oneShot {
		t.Fatal("a snapshot of the old block list was served from the pool")
	}
}
