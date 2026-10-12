package supplier

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

type iconTransport func(*http.Request) (*http.Response, error)

func (f iconTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type iconOutbound struct {
	channel.Outbound
	transport iconTransport
}

func (o iconOutbound) FreshClient(context.Context, string) (*http.Client, error) {
	return &http.Client{Transport: o.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func iconResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestFetchBitmapIconAndFallback(t *testing.T) {
	policy, _ := channel.NewOutboundPolicy(nil, nil)
	calls := 0
	service := Service{Outbound: iconOutbound{Outbound: policy, transport: func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("sent credentials")
		}
		switch r.URL.Path {
		case "", "/":
			return iconResponse(200, `<link rel="icon" href="/unsupported.svg"><link rel="icon" href="/brand.ico">`), nil
		case "/unsupported.svg":
			return iconResponse(200, "<svg/>"), nil
		case "/brand.ico":
			return iconResponse(200, string(testICO(2, 2, testDIB(32, true)))), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}}}
	data, source, err := service.FetchIcon(context.Background(), "https://supplier.example")
	if err != nil || !strings.HasPrefix(data, "data:image/png;base64,") || source != "https://supplier.example/brand.ico" || calls != 3 {
		t.Fatalf("source=%s calls=%d err=%v", source, calls, err)
	}
}
func TestFetchIconClassifiesFailures(t *testing.T) {
	policy, _ := channel.NewOutboundPolicy(nil, nil)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
		want   error
	}{
		{name: "missing", status: 404, want: ErrIconNotFound},
		{name: "redirect", status: 302, want: ErrIconRedirect},
		{name: "format", status: 200, body: "<svg/>", want: ErrIconFormat},
		{name: "large", status: 200, body: strings.Repeat("a", maxIconBytes+1), want: ErrIconTooLarge},
		{name: "timeout", err: context.DeadlineExceeded, want: ErrIconTimeout},
		{name: "download", err: errors.New("private transport detail"), want: ErrIconDownload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := Service{Outbound: iconOutbound{Outbound: policy, transport: func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "supplier.example" {
					t.Fatal("followed redirect")
				}
				if tc.err != nil {
					return nil, tc.err
				}
				response := iconResponse(tc.status, tc.body)
				response.Header.Set("Location", "https://other.example/icon.png")
				return response, nil
			}}}
			_, _, err := s.FetchIcon(context.Background(), "https://supplier.example")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if calls > 4 {
				t.Fatalf("unbounded requests: %d", calls)
			}
			if strings.Contains(err.Error(), "private transport detail") {
				t.Fatal("transport error leaked")
			}
		})
	}
	t.Run("403 survives later missing candidates", func(t *testing.T) {
		s := Service{Outbound: iconOutbound{Outbound: policy, transport: func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "" || r.URL.Path == "/" {
				return iconResponse(403, "private error body"), nil
			}
			return iconResponse(404, ""), nil
		}}}
		_, _, err := s.FetchIcon(context.Background(), "https://supplier.example")
		var status *IconHTTPError
		if !errors.As(err, &status) || status.Status != 403 {
			t.Fatalf("lost HTTP status: %v", err)
		}
	})
}
