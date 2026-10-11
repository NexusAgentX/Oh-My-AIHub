package gatewaybench

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
)

func TestServerServesTheTestCertificateAndCountsConnections(t *testing.T) {
	pki, err := NewPKI("up.perf.test", "other.perf.test")
	if err != nil {
		t.Fatal(err)
	}
	server, err := Serve(NewUpstream(Profile{Tokens: 2}), pki.ServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	// The host name does not resolve; the dialer goes to the server anyway.
	client := NewClient(ClientConfig{RootCAs: pki.Roots, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Address())
	}})
	request := RequestSpec{Format: "openai_chat"}.Build("https://up.perf.test:" + server.Port())
	for range 3 {
		httpRequest, _ := http.NewRequest(request.Method, request.URL, nil)
		httpRequest.Header = request.Header
		response, err := client.Do(httpRequest)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	// The client keeps its connection alive, so three requests used one.
	if server.Accepted() != 1 {
		t.Fatalf("accepted %d connections", server.Accepted())
	}

	// A client that does not trust the CA is refused.
	untrusting := NewClient(ClientConfig{Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Address())
	}})
	if _, err := untrusting.Get("https://up.perf.test:" + server.Port()); err == nil {
		t.Fatal("a client without the test CA was served")
	}
	if _, err := NewPKI(); err == nil {
		t.Fatal("a certificate without a host name was issued")
	}
}
