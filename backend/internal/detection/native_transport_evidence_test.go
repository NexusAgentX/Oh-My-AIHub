package detection

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
)

func TestNativeRepeatedAssertionsKeepHTTPDenialsSeparateFromUnsentFailures(t *testing.T) {
	for _, requirement := range []string{"native-generate-content.fingerprint", "openai-chat-completions.cache_tier", "native-generate-content.json"} {
		t.Run(requirement, func(t *testing.T) {
			r, s, service := nativeTestSession(t, "https://synthetic.example")
			requests := 0
			s.outbound = fixtureOutbound{&http.Client{Transport: detectorRoundTrip(func(req *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return nil, errors.New("synthetic connection failed before headers")
				}
				httptrace.ContextClientTrace(req.Context()).WroteHeaders()
				return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Synthetic request denied"}}`)), Request: req}, nil
			})}}
			result := service.run(context.Background(), s, nativeItem(t, r, s, requirement), nil)
			if !terminal(result.State) || result.State == "pass" || result.State == "unsupported" {
				t.Fatal("mixed connection failures and HTTP denials aborted execution or proved support", result.State, result.Reason)
			}
			unsent := map[string]bool{}
			for _, attempt := range result.Attempts {
				unsent[attempt.ID] = !attempt.Sent
			}
			for _, assertion := range result.Assertions {
				if assertion.Reason != "key_unavailable" {
					continue
				}
				for _, id := range assertion.AttemptIDs {
					if unsent[id] {
						t.Fatal("an unsent connection failure was used as an HTTP denial witness", assertion.ID)
					}
				}
			}
		})
	}
}
