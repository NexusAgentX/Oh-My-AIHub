package detection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

type detectorRoundTrip func(*http.Request) (*http.Response, error)

func (f detectorRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeEntranceFailoverKeepsOneCapabilityAndReusesExactRequests(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_write", true: "already_sent"}[sent], func(t *testing.T) {
			bad, good := 0, 0
			r, s, service := nativeTestSession(t, "https://first.invalid")
			s.resolved.Addresses = append(s.resolved.Addresses, "https://second.invalid")
			s.outbound = fixtureOutbound{&http.Client{Transport: detectorRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "first.invalid" {
					bad++
					if sent {
						httptrace.ContextClientTrace(req.Context()).WroteHeaders()
					}
					return nil, errors.New("synthetic connection error")
				}
				good++
				httptrace.ContextClientTrace(req.Context()).WroteHeaders()
				body, _ := io.ReadAll(req.Body)
				o := nativeFixtureWithCalls("openai-responses", nativeMarker, nil)
				if strings.Contains(string(body), "10000") {
					o["status"] = "incomplete"
					o["incomplete_details"] = nativeObject{"reason": "max_output_tokens"}
				}
				raw, _ := json.Marshal(o)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: req}, nil
			})}}
			result := service.run(context.Background(), s, nativeItem(t, r, s, "openai-responses.json"), nil)
			if sent {
				if good != 0 || bad != 2 || result.State != "inconclusive" || s.entry != 0 {
					t.Fatalf("ambiguous inference retried: bad=%d good=%d result=%s", bad, good, result.State)
				}
				return
			}
			if result.State != "pass" || bad != 1 || good != 2 || len(result.Attempts) != 3 || s.entry != 1 {
				t.Fatalf("fallback/trace mismatch: bad=%d good=%d result=%+v", bad, good, result)
			}
			failed, successful := 0, 0
			for _, a := range result.Attempts {
				if a.EntryURL == "https://first.invalid" && !a.Sent && !a.ResponseReceived {
					failed++
				}
				if a.EntryURL == "https://second.invalid" && a.ResponseReceived {
					successful++
				}
			}
			if failed != 1 || successful != 2 {
				t.Fatal("actual entrance diagnostics lost")
			}
			n := nativeScene{s: s, protocol: "openai-responses"}
			if out := n.basic(context.Background()); out.State != "pass" || s.usage.HTTP != 3 {
				t.Fatal("address change split the semantic request cache")
			}
		})
	}
}

func TestReadFallbackSharesDeadlineAndDoesNotRetryAuthenticationOrProtection(t *testing.T) {
	for _, status := range []int{503, 401} {
		_, s, _ := nativeTestSession(t, "https://first.invalid")
		s.resolved.Addresses = append(s.resolved.Addresses, "https://second.invalid")
		s.allowed = map[string]bool{"models.openai.list": true}
		var deadlines []time.Time
		s.outbound = fixtureOutbound{&http.Client{Transport: detectorRoundTrip(func(req *http.Request) (*http.Response, error) {
			deadline, _ := req.Context().Deadline()
			deadlines = append(deadlines, deadline)
			httptrace.ContextClientTrace(req.Context()).WroteHeaders()
			code := status
			if req.URL.Host == "second.invalid" {
				code = 200
			}
			return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		})}}
		out, err := s.Do(context.Background(), Request{EndpointID: "models.openai.list", Path: "/v1/models", Category: "query"})
		if err != nil {
			t.Fatal(err)
		}
		if status == 503 && (out.Status != 200 || len(out.PriorAttempts) != 1 || len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1])) {
			t.Fatal("read fallback reset timeout or lost trace")
		}
		if status == 401 && (out.Status != 401 || len(deadlines) != 1) {
			t.Fatal("authentication rejection bypassed with another entrance")
		}
		before := len(deadlines)
		_, err = s.Do(context.Background(), Request{EndpointID: "models.openai.list", Path: "/v1/models", Category: "query", Oversized: true})
		if err != ErrBusy || len(deadlines) != before {
			t.Fatal("local protection bypassed by retry")
		}
	}
}
