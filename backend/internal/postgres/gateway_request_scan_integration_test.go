package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// The two members of a request that pick a price (ADR-0030) are read from the
// body of every format, the Gemini one included, and reach the price snapshot
// of the call the way a full decode of the body would have delivered them.
func TestGatewayReadsThePricingMembersOfEveryFormat(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	e.createChannel(sharer, channelSpec{name: "relay", upstream: okUpstream(t), upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)

	snapshotDetail := func(id string) ledger.UsageDetail {
		t.Helper()
		var raw []byte
		if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			Detail ledger.UsageDetail `json:"detail"`
		}
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return snapshot.Detail
	}
	anthropic := map[string]string{"anthropic-version": "2023-06-01"}
	for _, tc := range []struct {
		name, path, body string
		headers          map[string]string
		thinkingMode     string
		tier             string
	}{
		{"chat, thinking off", "/v1/chat/completions", `{"model":"gpt-test","enable_thinking":false,"service_tier":"priority"}`, nil, "", "priority"},
		{"chat, thinking on", "/v1/chat/completions", `{"model":"gpt-test","enable_thinking":true,"service_tier":"flex"}`, nil, "", "flex"},
		{"chat, neither", "/v1/chat/completions", `{"model":"gpt-test"}`, nil, "", ""},
		{"chat, later member wins, other case", "/v1/chat/completions", `{"enable_thinking":true,"model":"gpt-test","Enable_Thinking":false,"service_tier":"a","SERVICE_TIER":"scale"}`, nil, "", "scale"},
		{"chat, a service tier that is no string", "/v1/chat/completions", `{"model":"gpt-test","enable_thinking":null,"service_tier":5}`, nil, "", ""},
		{"responses", "/v1/responses", `{"model":"gpt-test","enable_thinking":false,"service_tier":"priority"}`, nil, "", "priority"},
		{"anthropic", "/v1/messages", `{"model":"gpt-test","max_tokens":10,"service_tier":"auto"}`, anthropic, "", "auto"},
		{"gemini", "/v1beta/models/gpt-test:generateContent?key=" + secret, `{"contents":[],"service_tier":"priority"}`, map[string]string{"Authorization": ""}, "", "priority"},
	} {
		recorder := e.call(secret, http.MethodPost, tc.path, []byte(tc.body), tc.headers)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.name, recorder.Code, recorder.Body.String())
		}
		detail := snapshotDetail(recorder.Header().Get("X-AIHub-Request-Id"))
		if detail.RequestedServiceTier != tc.tier || detail.ThinkingMode != tc.thinkingMode {
			t.Fatalf("%s: requested tier %q, thinking mode %q; want %q, %q", tc.name, detail.RequestedServiceTier, detail.ThinkingMode, tc.tier, tc.thinkingMode)
		}
	}
}

// Bodies of every size reach the upstream with the model replaced and not a
// byte else changed: the request is read into a buffer sized by its announced
// length, in one stage or two, and spliced from the one scan.
func TestGatewayForwardsLargeRequestBodiesUnchanged(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)

	for _, size := range []int{100, 70_000, 300_000, 2_000_000} {
		filler := strings.Repeat(`word \"model\": "x" é `, size/22+1)
		body := `{ "model" : "gpt-test", "stream":true, "messages":[{"role":"user","content":"` + filler + `"}], "service_tier":"priority" }`
		want := `{ "model" : "gpt-up", "stream":true, "messages":[{"role":"user","content":"` + filler + `"}], "service_tier":"priority" ,"stream_options":{"include_usage":true}}`
		// The splice adds the option after the last member, whitespace and all.
		want = strings.Replace(want, `"priority" ,"stream_options":{"include_usage":true}}`, `"priority","stream_options":{"include_usage":true} }`, 1)
		before := len(relay.inference())
		recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(body), nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%d bytes: %d %s", len(body), recorder.Code, recorder.Body.String())
		}
		seen := relay.inference()[before]
		if string(seen.Body) != want {
			t.Fatalf("%d bytes: the upstream got %d bytes, want %d; first difference at %d", len(body), len(seen.Body), len(want), firstDifference(string(seen.Body), want))
		}
	}
}

func firstDifference(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
