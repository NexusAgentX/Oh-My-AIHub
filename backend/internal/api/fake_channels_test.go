package api

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
)

// Retained channel identities used only by historical observation fixtures.
type fakeChannelStore struct {
	mu       sync.Mutex
	channels []*fakeStoredChannel
}
type fakeStoredChannel struct{ groupchannel.Record }

func newFakeChannelStore(_ *fakeStore) *fakeChannelStore { return &fakeChannelStore{} }
func (s *fakeChannelStore) find(id string) (*fakeStoredChannel, error) {
	for _, item := range s.channels {
		if item.ID == id {
			return item, nil
		}
	}
	return nil, channel.ErrNotFound
}

// ---- the listed-channel read side: model pages and the home page ----

type fakeBrowse struct {
	channels *fakeChannelStore
	home     gateway.HomeStats
	recent   []gateway.CallSummary
}

func (b *fakeBrowse) Home(context.Context, string, time.Time) (gateway.HomeStats, error) {
	return b.home, nil
}

func (b *fakeBrowse) RecentCalls(_ context.Context, _ string, limit int) ([]gateway.CallSummary, error) {
	if len(b.recent) > limit {
		return b.recent[:limit], nil
	}
	return b.recent, nil
}

// seedChannelHealth gives a channel the statistics the model pages report:
// ten attempts of which nine completed, all of streaming calls with a first
// token after 420 ms. The median is read off a histogram, so the pages show the
// middle of the 395–430 ms bin it falls into, 412.5 ms.
func seedChannelHealth(book *gateway.StatsBook, channelID, modelID string) {
	at := time.Now()
	first := 420
	for range 9 {
		book.Record(channelID, modelID, true, gateway.Attempt{EndReason: gateway.EndCompleted, TTFTMS: &first}, at)
	}
	book.Record(channelID, modelID, true, gateway.Attempt{EndReason: gateway.EndUpstreamError}, at)
}

// ---- a loopback stand-in for the pinned egress policy ----

type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

// testOutbound keeps the real URL normalization and validation but sends
// every request to a loopback test server, which the real policy refuses.
type testOutbound struct {
	channel.Outbound
	target *url.URL
}

func newTestOutbound(t *testing.T, upstream *httptest.Server) testOutbound {
	t.Helper()
	policy, err := channel.NewOutboundPolicyWithResolver(nil, nil, publicResolver{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return testOutbound{Outbound: policy, target: target}
}

type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme, rewritten.URL.Host, rewritten.Host = t.target.Scheme, t.target.Host, t.target.Host
	return http.DefaultTransport.RoundTrip(rewritten)
}

func (o testOutbound) client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout, Transport: rewriteTransport{o.target},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (o testOutbound) Client(_ context.Context, _ string, timeout time.Duration) (*http.Client, error) {
	return o.client(timeout), nil
}

func (o testOutbound) GatewayClient(_ context.Context, _ string) (*http.Client, error) {
	return o.client(0), nil
}

func (o testOutbound) FreshClient(_ context.Context, _ string) (*http.Client, error) {
	return o.client(0), nil
}

func (o testOutbound) WithExtraBlockedHosts(hosts []string) (channel.Outbound, error) {
	inner, err := o.Outbound.WithExtraBlockedHosts(hosts)
	if err != nil {
		return nil, err
	}
	return testOutbound{Outbound: inner, target: o.target}, nil
}

// newFakeUpstream is a relay that lists two OpenAI models, pages two
// Anthropic and two Gemini models, and streams a completed OpenAI chat answer,
// which is all discovery and the format test ask for.
func newFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case r.URL.Path == "/apple-touch-icon.png":
			w.Header().Set("Content-Type", "image/png")
			_ = png.Encode(w, image.NewNRGBA(image.Rect(0, 0, 16, 16)))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"deepseek-chat"},{"id":"some-unlisted-model"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") != "" && r.URL.Query().Get("limit") == "1000":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("after_id") == "relay-anthropic-1" {
				_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"relay-anthropic-2"}],"has_more":false,"first_id":"relay-anthropic-2","last_id":"relay-anthropic-2"}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"relay-anthropic-1"}],"has_more":true,"first_id":"relay-anthropic-1","last_id":"relay-anthropic-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/models" && r.URL.Query().Get("key") != "" && r.URL.Query().Get("pageSize") == "1000":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("pageToken") == "page-2" {
				_, _ = io.WriteString(w, `{"models":[{"name":"models/relay-gemini-2"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"models":[{"name":"models/relay-gemini-1"}],"nextPageToken":"page-2"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
