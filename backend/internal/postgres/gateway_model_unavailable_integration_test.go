package postgres_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// attemptRow reads one entry of a call's attempt timeline.
func (e *env) attemptRow(callID string, index int) (endReason, errorCode string, status int) {
	e.t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT attempts->$2::int->>'end_reason', coalesce(attempts->$2::int->>'error_code', ''), coalesce((attempts->$2::int->>'status_code')::int, 0)
		FROM calls WHERE id = $1`, callID, index).Scan(&endReason, &errorCode, &status)
	if err != nil {
		e.t.Fatalf("attempt %d of %s: %v", index, callID, err)
	}
	return endReason, errorCode, status
}

// inferenceRequest is one client request per format; the gateway swaps the
// model for the channel's own name before it reaches the upstream.
type inferenceRequest struct {
	name, path, body string
	headers          map[string]string
}

var allFormats = []inferenceRequest{
	{name: "openai chat", path: "/v1/chat/completions", body: `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`},
	{name: "openai responses", path: "/v1/responses", body: `{"model":"gpt-test","input":"hi"}`},
	{name: "anthropic", path: "/v1/messages", body: `{"model":"gpt-test","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`,
		headers: map[string]string{"anthropic-version": "2023-06-01"}},
	{name: "gemini", path: "/v1beta/models/gpt-test:generateContent", body: `{"contents":[{"parts":[{"text":"hi"}]}]}`},
}

// replyWith answers every request with a fixed status and body.
func replyWith(t *testing.T, status int, body string) *upstream {
	return newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// A channel whose upstream model name is wrong must not keep winning the
// model: the 4xx that says so is the channel's failure, whatever the format.
func TestGatewayFallsBackWhenTheUpstreamSaysTheModelDoesNotExist(t *testing.T) {
	cases := []struct {
		name    string
		request inferenceRequest
		status  int
		body    string
	}{
		{"openai chat error code", allFormats[0], http.StatusBadRequest,
			`{"error":{"message":"The model ` + "`gpt-up`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","param":null,"code":"model_not_found"}}`},
		{"openai chat chinese message", allFormats[0], http.StatusUnprocessableEntity,
			`{"error":{"message":"当前分组 default 下对于模型 gpt-up 无可用渠道","type":"new_api_error"}}`},
		{"openai responses error code", allFormats[1], http.StatusBadRequest,
			`{"error":{"message":"The requested model 'gpt-up' does not exist.","type":"invalid_request_error","param":"model","code":"model_not_found"}}`},
		// Anthropic itself answers 404 not_found_error, which falls back by status;
		// the 4xx outside that list comes from Anthropic-compatible relays.
		{"anthropic relay message", allFormats[2], http.StatusBadRequest,
			`{"type":"error","error":{"type":"invalid_request_error","message":"The model ` + "`gpt-up`" + ` does not exist or you do not have access to it."}}`},
		{"gemini not found status", allFormats[3], http.StatusBadRequest,
			`{"error":{"code":400,"message":"models/gpt-up is not found for API version v1beta, or is not supported for generateContent.","status":"NOT_FOUND"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			consumer := e.member("consumer", "100")
			sharer := e.member("sharer", "0")
			wrong := replyWith(t, tc.status, tc.body)
			good := okUpstream(t)
			// One failure is enough to cool the channel, which shows it was counted.
			wrongID := e.createChannel(sharer, channelSpec{name: "wrong-name", upstream: wrong, upstreamAs: "gpt-up", multiplier: "1",
				advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
			goodID := e.createChannel(sharer, channelSpec{name: "good", upstream: good, multiplier: "2"})
			_, secret := e.defaultKey(consumer)
			send := func() *httptest.ResponseRecorder {
				return e.call(secret, http.MethodPost, tc.request.path, []byte(tc.request.body), tc.request.headers)
			}

			first := send()
			if first.Code != http.StatusOK {
				t.Fatalf("response = %d %s", first.Code, first.Body.String())
			}
			row := e.callRow(first.Header().Get("X-AIHub-Request-Id"))
			if row.Outcome != "succeeded" || row.Attempts != 2 || row.FinalChannel != goodID || !row.HasLedgerTx {
				t.Fatalf("call = %+v", row)
			}
			endReason, errorCode, status := e.attemptRow(first.Header().Get("X-AIHub-Request-Id"), 0)
			if endReason != "upstream_error" || errorCode != "upstream_model_unavailable" || status != tc.status {
				t.Fatalf("first attempt = %s %s %d", endReason, errorCode, status)
			}
			if len(wrong.inference()) != 1 || len(good.inference()) != 1 {
				t.Fatalf("attempts: wrong %d good %d", len(wrong.inference()), len(good.inference()))
			}
			if e.runtime.CooldownUntil(wrongID, time.Now()).IsZero() {
				t.Fatal("the failure was not counted against the channel")
			}

			// The channel is out of rotation now: the next call does not pay for the failure again.
			if second := send(); second.Code != http.StatusOK || len(wrong.inference()) != 1 {
				t.Fatalf("second call = %d, wrong channel hit %d times", second.Code, len(wrong.inference()))
			}
		})
	}
}

// When every channel lacks the model the client still gets an upstream answer:
// the last one, as with any other failure on every channel.
func TestGatewayAnswersWithTheLastUpstreamErrorWhenEveryChannelLacksTheModel(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	const lastBody = `{"error":{"message":"The model ` + "`gpt-b`" + ` does not exist","type":"invalid_request_error","code":"model_not_found"}}`
	e.createChannel(sharer, channelSpec{name: "a", upstream: replyWith(t, http.StatusBadRequest, `{"error":{"code":"invalid_model","message":"nope"}}`), multiplier: "1"})
	e.createChannel(sharer, channelSpec{name: "b", upstream: replyWith(t, http.StatusUnprocessableEntity, lastBody), multiplier: "2"})
	_, secret := e.defaultKey(consumer)

	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusUnprocessableEntity || recorder.Body.String() != lastBody {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	id := recorder.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(id)
	if row.Outcome != "upstream_failed" || row.Attempts != 2 || row.HasLedgerTx || row.CostNano != 0 {
		t.Fatalf("call = %+v", row)
	}
	for index := range 2 {
		if _, code, _ := e.attemptRow(id, index); code != "upstream_model_unavailable" {
			t.Fatalf("attempt %d error code = %q", index, code)
		}
	}
	if e.balance(consumer) != "0" {
		t.Fatalf("consumer balance = %s", e.balance(consumer))
	}
	e.assertZeroSum()
}

// requestError is a 4xx an upstream gives for a faulty request.
type requestError struct {
	name    string
	request inferenceRequest
	status  int
	body    string
}

// Errors about the request itself reach the client untouched and never count
// against the channel, however alike they read to a missing model.
func TestGatewayHandsBackRequestErrorsByteForByteAndDoesNotCountThem(t *testing.T) {
	cases := []requestError{
		{"openai chat max_tokens too large", allFormats[0], http.StatusBadRequest,
			"{\n  \"error\": {\n    \"message\": \"max_tokens is too large: 1000000. This model supports at most 4096 completion tokens, whereas you provided 1000000.\",\n" +
				"    \"type\": \"invalid_request_error\",\n    \"param\": \"max_tokens\",\n    \"code\": null\n  }\n}\n"},
		{"openai chat unsupported parameter", allFormats[0], http.StatusBadRequest,
			`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`},
		{"openai chat bad message", allFormats[0], http.StatusUnprocessableEntity,
			`{"error":{"message":"Invalid value: 'bot'. Supported values are: 'system', 'assistant', 'user', 'function', and 'tool'.","type":"invalid_request_error","param":"messages[0].role","code":"invalid_value"}}`},
		{"openai responses missing input", allFormats[1], http.StatusBadRequest,
			`{"error":{"message":"Missing required parameter: 'input'.","type":"invalid_request_error","param":"input","code":"missing_required_parameter"}}`},
		{"anthropic max_tokens", allFormats[2], http.StatusBadRequest,
			`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 100000 > 8192, which is the maximum allowed number of output tokens for claude-3-5-sonnet-20241022"},"request_id":"req_011"}`},
		{"anthropic bad messages", allFormats[2], http.StatusBadRequest,
			`{"type":"error","error":{"type":"invalid_request_error","message":"messages: text content blocks must be non-empty"}}`},
		{"gemini bad payload", allFormats[3], http.StatusBadRequest,
			"{\n  \"error\": {\n    \"code\": 400,\n    \"message\": \"Invalid JSON payload received. Unknown name \\\"foo\\\" at 'generation_config': Cannot find field.\",\n" +
				"    \"status\": \"INVALID_ARGUMENT\"\n  }\n}\n"},
	}
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	var current atomic.Pointer[requestError]
	strict := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		answer := current.Load()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Upstream", "kept")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	})
	other := okUpstream(t)
	// One failure would cool the channel: none must be counted.
	strictID := e.createChannel(sharer, channelSpec{name: "strict", upstream: strict, multiplier: "1",
		advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
	e.createChannel(sharer, channelSpec{name: "other", upstream: other, multiplier: "2"})
	_, secret := e.defaultKey(consumer)

	for index, tc := range cases {
		current.Store(&tc)
		recorder := e.call(secret, http.MethodPost, tc.request.path, []byte(tc.request.body), tc.request.headers)
		if recorder.Code != tc.status || recorder.Body.String() != tc.body {
			t.Fatalf("%s: response = %d %q", tc.name, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("%s: content type %q", tc.name, got)
		}
		if recorder.Header().Get("X-Upstream") != "kept" || recorder.Header().Get("Retry-After") != "7" {
			t.Fatalf("%s: headers = %v", tc.name, recorder.Header())
		}
		id := recorder.Header().Get("X-AIHub-Request-Id")
		row := e.callRow(id)
		if row.Outcome != "upstream_failed" || row.Attempts != 1 || row.HasLedgerTx || row.CostNano != 0 || row.ResponseBytes != int64(len(tc.body)) {
			t.Fatalf("%s: call = %+v", tc.name, row)
		}
		if endReason, code, _ := e.attemptRow(id, 0); endReason != "client_error" || code != "" {
			t.Fatalf("%s: attempt = %s %q", tc.name, endReason, code)
		}
		if len(strict.inference()) != index+1 {
			t.Fatalf("%s: the strict channel was skipped after %d calls", tc.name, index)
		}
		if !e.runtime.CooldownUntil(strictID, time.Now()).IsZero() {
			t.Fatalf("%s: the request's own error was counted against the channel", tc.name)
		}
	}
	if len(other.inference()) != 0 {
		t.Fatal("a request-level error must not try another channel")
	}
	e.assertZeroSum()
}
