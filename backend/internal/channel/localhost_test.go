package channel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalhostDevelopmentPolicyIsOptInAndNarrow(t *testing.T) {
	resolver := &fakeResolver{addresses: []net.IP{net.ParseIP("127.0.0.1")}}
	original, err := NewOutboundPolicyWithResolver([]string{"80", "18081"}, []string{"blocked.example"}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	policy := original.WithLocalhostForDevelopment()
	ctx := context.Background()
	for _, raw := range []string{"http://localhost:18081", "https://localhost:18081"} {
		if _, err := original.ValidateBaseURL(ctx, raw); err == nil {
			t.Fatalf("default accepted %s", raw)
		}
		if _, err := policy.ValidateBaseURL(ctx, raw); err != nil {
			t.Fatalf("development rejected %s: %v", raw, err)
		}
	}
	if got, err := policy.NormalizeBaseURL("http://LOCALHOST.:80/v1"); err != nil || got != "http://localhost" {
		t.Fatalf("normalized = %q, %v", got, err)
	}
	if resolver.calls != 1 {
		t.Fatalf("localhost exception should not use DNS; calls = %d", resolver.calls)
	}
	for _, raw := range []string{"http://gateway.example:18081", "http://127.0.0.1:18081", "https://127.0.0.1:18081", "http://[::1]:18081", "http://sub.localhost:18081", "http://localhost:18082", "https://blocked.example"} {
		if _, err := policy.ValidateBaseURL(ctx, raw); err == nil {
			t.Fatalf("development accepted %s", raw)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "198.18.0.1", "::1"} {
		resolver.addresses = []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP(address)}
		if _, err := policy.ValidateBaseURL(ctx, "https://gateway.example"); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("protected answer %s: %v", address, err)
		}
	}
	blocked, err := policy.WithExtraBlockedHosts([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.ValidateBaseURL(ctx, "http://localhost:18081"); err == nil {
		t.Fatal("localhost host ban ignored")
	}
	if _, err := original.ValidateBaseURL(ctx, "http://localhost:18081"); err == nil {
		t.Fatal("original policy changed")
	}
}

func TestLocalhostDevelopmentClientsRemainPinnedAndReuseGatewayConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/followed")
			w.WriteHeader(http.StatusFound)
			return
		}
		if r.URL.Path == "/followed" {
			t.Error("redirect was followed")
		}
		_, _ = io.WriteString(w, "local response")
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	policy, err := NewOutboundPolicy([]string{port}, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy = policy.WithLocalhostForDevelopment()
	base := "http://localhost:" + port
	ctx := context.Background()
	for _, clientFor := range []func() (*http.Client, error){
		func() (*http.Client, error) { return policy.Client(ctx, base, time.Second) },
		func() (*http.Client, error) { return policy.FreshClient(ctx, base) },
		func() (*http.Client, error) { return policy.GatewayClient(ctx, base) },
	} {
		client, err := clientFor()
		if err != nil {
			t.Fatal(err)
		}
		var transport *http.Transport
		switch value := client.Transport.(type) {
		case *http.Transport:
			transport = value
		case *countedTransport:
			transport = value.Transport
		default:
			t.Fatalf("unexpected transport %T", value)
		}
		if transport.Proxy != nil {
			t.Fatal("environment proxy enabled")
		}
		for i := 0; i < 2; i++ {
			resp, err := client.Get(base + "/redirect")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("redirect status: %d", resp.StatusCode)
			}
		}
		if _, err := transport.DialContext(ctx, "tcp", "other.example:"+port); !errors.Is(err, ErrUnsafeUpstream) {
			t.Fatalf("unpinned destination: %v", err)
		}
		transport.CloseIdleConnections()
	}
	stats := policy.EgressStats()
	if stats.NewConnections != 1 || stats.ReusedConnections != 1 {
		t.Fatalf("gateway did not reuse connection: %+v", stats)
	}
}

func TestLocalhostDevelopmentDoesNotSkipTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS reached handler") }))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	policy, err := NewOutboundPolicy([]string{port}, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy = policy.WithLocalhostForDevelopment()
	base := "https://localhost:" + port
	client, err := policy.Client(context.Background(), base, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Get(base); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted TLS result: %v", err)
	}
}
