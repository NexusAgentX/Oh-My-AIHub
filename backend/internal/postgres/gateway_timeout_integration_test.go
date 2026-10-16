package postgres_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// attemptField reads one text field of one attempt of a call.
func (e *env) attemptField(callID string, index int, field string) string {
	e.t.Helper()
	var value *string
	err := e.pool.QueryRow(context.Background(), `SELECT attempts->($2::int)->>$3 FROM calls WHERE id = $1`, callID, index, field).Scan(&value)
	if err != nil {
		e.t.Fatalf("attempt %d of %s: %v", index, callID, err)
	}
	if value == nil {
		return ""
	}
	return *value
}

func (e *env) cooling(channelID string) bool {
	return !e.runtime.CooldownUntil(channelID, time.Now()).IsZero()
}

const chatFrame = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"

// pacedChatStream sends frames chunks of a Chat stream, one every interval, and
// then the usage frame.
func pacedChatStream(frames int, interval time.Duration) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for range frames {
			_, _ = io.WriteString(w, chatFrame)
			flusher.Flush()
			select {
			case <-time.After(interval):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":"+responsesBody+"}\n\n")
	}
}

// stallAfter runs write, then holds the connection open until the gateway hangs up.
func stallAfter(t *testing.T, write func(http.ResponseWriter)) func(http.ResponseWriter, *http.Request, []byte) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	return func(w http.ResponseWriter, r *http.Request, _ []byte) {
		write(w)
		w.(http.Flusher).Flush()
		select {
		case <-stuck:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}
}

// A stream can outlive both its idle and non-streaming request timeout
// while output keeps arriving.
func TestGatewayStreamOutlivesTheRequestTimeoutWhileOutputKeepsFlowing(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	steady := newUpstream(t, pacedChatStream(8, 500*time.Millisecond))
	id := e.createChannel(sharer, channelSpec{name: "steady", upstream: steady, advanced: map[string]any{
		"ttft_timeout_ms": 1000, "request_timeout_ms": 1000, "idle_timeout_ms": 1500, "cooldown_failures": 1, "cooldown_seconds": 60,
	}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","stream":true}`), nil)
	elapsed := time.Since(started)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || elapsed < 3*time.Second || strings.Count(body, "\"delta\"") != 8 || !strings.Contains(body, "response.completed") {
		t.Fatalf("call = %d after %s: %q", recorder.Code, elapsed, body)
	}
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(callID)
	if row.Outcome != "succeeded" || row.Input != 1000 || row.Output != 500 || !row.HasLedgerTx || row.Attempts != 1 {
		t.Fatalf("call = %+v", row)
	}
	if reason := e.attemptField(callID, 0, "end_reason"); reason != "completed" || e.cooling(id) || len(steady.inference()) != 1 {
		t.Fatalf("end reason %q, cooling %v, upstream calls %d", reason, e.cooling(id), len(steady.inference()))
	}
	e.assertZeroSum()
}

func TestGatewayInterruptsAStreamThatFallsSilentAfterItsFirstByte(t *testing.T) {
	e := newEnvWith(t, slowCloseOutbound)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	silent := newUpstream(t, stallAfter(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chatFrame)
	}))
	id := e.createChannel(sharer, channelSpec{name: "silent", upstream: silent, advanced: map[string]any{
		"ttft_timeout_ms": 1000, "request_timeout_ms": 60000, "idle_timeout_ms": 1500, "cooldown_failures": 1, "cooldown_seconds": 60,
	}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","stream":true}`), nil)
	elapsed := time.Since(started)
	if recorder.Code != http.StatusOK || elapsed < 1500*time.Millisecond || elapsed > 10*time.Second || recorder.Body.String() != chatFrame {
		t.Fatalf("call = %d after %s: %q", recorder.Code, elapsed, recorder.Body.String())
	}
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(callID); row.Outcome != "interrupted" || row.HasLedgerTx || row.CostNano != 0 {
		t.Fatalf("call = %+v", row)
	}
	if reason, code := e.attemptField(callID, 0, "end_reason"), e.attemptField(callID, 0, "error_code"); reason != "interrupted" || code != "timeout_idle" {
		t.Fatalf("attempt ended %q / %q", reason, code)
	}
	if !e.cooling(id) {
		t.Fatal("the silent channel was not counted as failed")
	}
	e.assertZeroSum()
}

func TestGatewayFallsBackWhenANonStreamingRequestExceedsTheRequestTimeout(t *testing.T) {
	e := newEnvWith(t, slowCloseOutbound)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	slow := newUpstream(t, stallAfter(t, func(http.ResponseWriter) {}))
	fast := okUpstream(t)
	slowID := e.createChannel(sharer, channelSpec{name: "slow", upstream: slow, multiplier: "1", advanced: map[string]any{
		"request_timeout_ms": 1500, "idle_timeout_ms": 60000, "cooldown_failures": 1, "cooldown_seconds": 60,
	}})
	e.createChannel(sharer, channelSpec{name: "fast", upstream: fast, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != chatBody || time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("call = %d after %s: %s", recorder.Code, time.Since(started), recorder.Body.String())
	}
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(callID); row.Outcome != "succeeded" || row.Attempts != 2 || !row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if reason, code := e.attemptField(callID, 0, "end_reason"), e.attemptField(callID, 0, "error_code"); reason != "timeout_total" || code != "timeout_total" {
		t.Fatalf("first attempt ended %q / %q", reason, code)
	}
	if e.attemptField(callID, 1, "end_reason") != "completed" || !e.cooling(slowID) || len(fast.inference()) != 1 {
		t.Fatalf("second attempt %q, slow cooling %v, fast calls %d", e.attemptField(callID, 1, "end_reason"), e.cooling(slowID), len(fast.inference()))
	}
	e.assertZeroSum()
}

// The total timeout of a non-streaming request covers reading the whole body,
// too: an answer that stalls half way is an interruption, not a fallback.
func TestGatewayInterruptsANonStreamingAnswerThatStallsMidBody(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	half := newUpstream(t, stallAfter(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(chatBody)))
		_, _ = io.WriteString(w, chatBody[:20])
	}))
	id := e.createChannel(sharer, channelSpec{name: "half", upstream: half, advanced: map[string]any{
		"request_timeout_ms": 1500, "idle_timeout_ms": 60000, "cooldown_failures": 1, "cooldown_seconds": 60,
	}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(callID); row.Outcome != "interrupted" || row.Attempts != 1 || row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if reason, code := e.attemptField(callID, 0, "end_reason"), e.attemptField(callID, 0, "error_code"); reason != "interrupted" || code != "timeout_total" {
		t.Fatalf("attempt ended %q / %q", reason, code)
	}
	if !e.cooling(id) {
		t.Fatal("the stalled channel was not counted as failed")
	}
}

// The call time limit stops a stream that is still talking, blames nobody, and
// bills what was read.
func TestGatewayStopsACallAtItsTimeLimitAndBillsWhatWasRead(t *testing.T) {
	e := newEnvWith(t, withDependencies(slowCloseOutbound, func(d *gateway.Dependencies) { d.CallTimeLimit = 1500 * time.Millisecond }))
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	endless := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_timeout\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n")
		flusher.Flush()
		for {
			select {
			case <-time.After(100 * time.Millisecond):
				_, _ = io.WriteString(w, chatFrame)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	id := e.createChannel(sharer, channelSpec{name: "endless", upstream: endless, advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","max_tokens":10,"stream":true}`), map[string]string{"anthropic-version": "2023-06-01"})
	elapsed := time.Since(started)
	if recorder.Code != http.StatusOK || elapsed < 1500*time.Millisecond || elapsed > 10*time.Second {
		t.Fatalf("call = %d after %s", recorder.Code, elapsed)
	}
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(callID)
	if row.Outcome != "interrupted" || row.Input != 1000 || row.Output != 1 || row.CostNano == 0 || !row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if reason, code := e.attemptField(callID, 0, "end_reason"), e.attemptField(callID, 0, "error_code"); reason != "interrupted" || code != "call_deadline" {
		t.Fatalf("attempt ended %q / %q", reason, code)
	}
	if e.cooling(id) {
		t.Fatal("the call limit was counted against the channel")
	}
	if e.balance(consumer) == "0" {
		t.Fatal("what was read was not billed")
	}
	e.assertZeroSum()
}

// A call that runs out of time before any output answers the client with an
// error of its own and does not go on to the next candidate.
func TestGatewayAnswersWhenTheCallTimeLimitPassesBeforeAnyOutput(t *testing.T) {
	e := newEnvWith(t, withDependencies(slowCloseOutbound, func(d *gateway.Dependencies) { d.CallTimeLimit = time.Second }))
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	stuck := newUpstream(t, stallAfter(t, func(http.ResponseWriter) {}))
	spare := okUpstream(t)
	id := e.createChannel(sharer, channelSpec{name: "stuck", upstream: stuck, multiplier: "1", advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
	e.createChannel(sharer, channelSpec{name: "spare", upstream: spare, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), `"call_deadline"`) || time.Since(started) < time.Second {
		var outcome, attempts string
		_ = e.pool.QueryRow(context.Background(), `SELECT outcome, attempts::text FROM calls WHERE id = $1`, recorder.Header().Get("X-AIHub-Request-Id")).Scan(&outcome, &attempts)
		t.Fatalf("call = %d after %s: %q\noutcome %s, attempts %s", recorder.Code, time.Since(started), recorder.Body.String(), outcome, attempts)
	}
	callID := recorder.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(callID); row.Outcome != "interrupted" || row.Attempts != 1 || row.HasLedgerTx || row.CostNano != 0 {
		t.Fatalf("call = %+v", row)
	}
	if code := e.attemptField(callID, 0, "error_code"); code != "call_deadline" || e.cooling(id) || len(spare.inference()) != 0 {
		t.Fatalf("error code %q, cooling %v, spare calls %d", code, e.cooling(id), len(spare.inference()))
	}
	if e.balance(consumer) != "0" {
		t.Fatalf("balance = %s", e.balance(consumer))
	}
}
