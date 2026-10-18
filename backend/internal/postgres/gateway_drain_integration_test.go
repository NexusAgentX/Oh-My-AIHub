package postgres_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// These tests put the gateway on a real HTTP server so that a client can
// really hang up: the server notices the closed connection and cancels the
// request context, which is what the drain after a disconnect reacts to.

const drainTestWindow = 1500 * time.Millisecond

func newDrainEnv(t *testing.T) *env {
	return newGroupEnvWith(t, withDependencies(slowCloseOutbound, func(d *gateway.Dependencies) { d.DrainWindow = drainTestWindow }))
}

// liveGateway serves the environment's handler on a real server.
func (e *env) liveGateway() *httptest.Server {
	server := httptest.NewServer(e.handler)
	e.t.Cleanup(server.Close)
	return server
}

// liveStream is the client side of a streaming call.
type liveStream struct {
	t        *testing.T
	response *http.Response
	reader   *bufio.Reader
	cancel   context.CancelFunc
}

func (e *env) openStream(server *httptest.Server, secret, path, body string, headers map[string]string) *liveStream {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	stream := &liveStream{t: e.t, response: response, reader: bufio.NewReader(response.Body), cancel: cancel}
	e.t.Cleanup(stream.hangUp)
	return stream
}

// event reads one SSE event, up to and including its blank line.
func (s *liveStream) event() string {
	s.t.Helper()
	var event strings.Builder
	for {
		line, err := s.reader.ReadString('\n')
		event.WriteString(line)
		if err != nil {
			s.t.Fatalf("reading an event: %v (so far %q)", err, event.String())
		}
		if line == "\n" {
			return event.String()
		}
	}
}

// hangUp drops the connection without reading the rest, like a client that
// was closed or lost its network.
func (s *liveStream) hangUp() {
	s.cancel()
	_ = s.response.Body.Close()
}

func (s *liveStream) requestID() string { return s.response.Header.Get("X-AIHub-Request-Id") }

// waitFinished waits until the call has left in_progress and returns how long
// after the given moment that happened.
func (e *env) waitFinished(callID string, since time.Time) time.Duration {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var outcome string
		if err := e.pool.QueryRow(context.Background(), `SELECT outcome FROM calls WHERE id = $1`, callID).Scan(&outcome); err != nil {
			e.t.Fatal(err)
		}
		if outcome != "in_progress" {
			return time.Since(since)
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("call %s is still in progress", callID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// slowTail is an upstream that sends frames of a Chat stream and, after the
// first, waits pause before the rest, noting whether the gateway hung up on it.
type slowTail struct {
	cancelled atomic.Bool
	finished  atomic.Bool
}

func (s *slowTail) handler(pause time.Duration, rest string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, chatFrame)
		flusher.Flush()
		select {
		case <-time.After(pause):
		case <-r.Context().Done():
			s.cancelled.Store(true)
			return
		}
		_, _ = io.WriteString(w, rest)
		flusher.Flush()
		s.finished.Store(true)
	}
}

const chatUsageTail = chatFrame + "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\ndata: [DONE]\n\n"

func TestGatewayKeepsReadingAfterTheClientHangsUpAndBillsTheUsage(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	tail := &slowTail{}
	upstream := newUpstream(t, tail.handler(500*time.Millisecond, chatUsageTail))
	e.createChannel(sharer, channelSpec{name: "stream", upstream: upstream})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()

	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	if first := stream.event(); first != chatFrame {
		t.Fatalf("first event = %q", first)
	}
	left := time.Now()
	stream.hangUp()

	callID := stream.requestID()
	elapsed := e.waitFinished(callID, left)
	row := e.callRow(callID)
	if row.Outcome != "client_disconnected" || row.Input != 1000 || row.Output != 500 || row.CostNano != 40_000_000 || !row.HasLedgerTx || row.ResponseBytes != int64(len(chatFrame)) {
		t.Fatalf("call = %+v", row)
	}
	if elapsed > drainTestWindow+time.Second {
		t.Fatalf("settled %s after the client left although the answer ended in 500ms", elapsed)
	}
	if tail.cancelled.Load() || !tail.finished.Load() {
		t.Fatalf("upstream cancelled=%v finished=%v: the gateway hung up on it", tail.cancelled.Load(), tail.finished.Load())
	}
	if reason := e.attemptField(callID, 0, "end_reason"); reason != "client_disconnected" {
		t.Fatalf("attempt end reason = %q", reason)
	}
	if e.balance(consumer) != "-"+expectedCharge || e.balance(sharer) != expectedCost {
		t.Fatalf("balances %s / %s", e.balance(consumer), e.balance(sharer))
	}
	e.assertZeroSum()
}

func TestGatewayStopsReadingWhenTheDrainWindowIsOver(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	// The rest of the answer only comes long after the window.
	tail := &slowTail{}
	chat := newUpstream(t, tail.handler(20*time.Second, chatUsageTail))
	chatChannel := e.createChannel(sharer, channelSpec{name: "chat", upstream: chat, advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()

	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	stream.event()
	left := time.Now()
	stream.hangUp()
	callID := stream.requestID()
	elapsed := e.waitFinished(callID, left)
	row := e.callRow(callID)
	if row.Outcome != "client_disconnected" || row.HasLedgerTx || row.Input != 0 || row.Output != 0 {
		t.Fatalf("call = %+v", row)
	}
	if elapsed < drainTestWindow-200*time.Millisecond || elapsed > drainTestWindow+3*time.Second {
		t.Fatalf("settled %s after the client left, want about %s", elapsed, drainTestWindow)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !tail.cancelled.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the gateway never hung up on the upstream")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e.cooling(chatChannel) {
		t.Fatal("the drain window was counted against the channel")
	}
	if e.balance(consumer) != "0" {
		t.Fatalf("balance = %s", e.balance(consumer))
	}

	// Anthropic reports the input at the start and the output at the end: when
	// the end does not come in time, only the input is billed.
	messages := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(20 * time.Second):
			_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":500}}\n\n")
		case <-r.Context().Done():
		}
	})
	sharer2 := e.member("sharer2", "0")
	e.createChannel(sharer2, channelSpec{name: "messages", upstream: messages, model: "claude-test"})
	anthropic := e.openStream(server, secret, "/v1/messages", `{"model":"claude-test","max_tokens":10,"stream":true}`, map[string]string{"anthropic-version": "2023-06-01"})
	anthropic.event()
	left = time.Now()
	anthropic.hangUp()
	anthropicID := anthropic.requestID()
	e.waitFinished(anthropicID, left)
	if row := e.callRow(anthropicID); row.Outcome != "client_disconnected" || row.Input != 1000 || row.Output != 1 || !row.HasLedgerTx {
		t.Fatalf("anthropic call = %+v", row)
	}
	e.assertZeroSum()
}

func TestGatewayBillsTheAnthropicOutputWhenItsEndArrivesInsideTheWindow(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	messages := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":500}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	e.createChannel(sharer, channelSpec{name: "messages", upstream: messages})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()
	stream := e.openStream(server, secret, "/v1/messages", `{"model":"gpt-test","max_tokens":10,"stream":true}`, map[string]string{"anthropic-version": "2023-06-01"})
	stream.event()
	left := time.Now()
	stream.hangUp()
	e.waitFinished(stream.requestID(), left)
	row := e.callRow(stream.requestID())
	if row.Outcome != "client_disconnected" || row.Input != 1000 || row.Output != 500 || row.CostNano != 40_000_000 || !row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	e.assertZeroSum()
}

// Before the gateway has written anything there is nothing to settle: the
// upstream is cancelled at once, even though a drain window is configured.
func TestGatewayCancelsTheUpstreamAtOnceWhenTheClientLeavesBeforeAnyOutput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"non-streaming", `{"model":"gpt-test"}`},
		{"streaming before the first byte", `{"model":"gpt-test","stream":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGroupEnvWith(t, func(d *gateway.Dependencies) { d.DrainWindow = 30 * time.Second })
			consumer := e.member("consumer", "100")
			sharer := e.member("sharer", "0")
			var cancelled atomic.Bool
			reached := make(chan struct{}, 1)
			waiting := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				reached <- struct{}{}
				select {
				case <-r.Context().Done():
					cancelled.Store(true)
				case <-time.After(30 * time.Second):
				}
			})
			e.createChannel(sharer, channelSpec{name: "waiting", upstream: waiting})
			_, secret := e.defaultKey(consumer)
			server := e.liveGateway()

			ctx, cancel := context.WithCancel(context.Background())
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+secret)
			done := make(chan struct{})
			go func() {
				defer close(done)
				client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
				if response, err := client.Do(request); err == nil {
					_ = response.Body.Close()
				}
			}()
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("the request never reached the upstream")
			}
			left := time.Now()
			cancel()
			<-done
			deadline := time.Now().Add(3 * time.Second)
			for !cancelled.Load() {
				if time.Now().After(deadline) {
					t.Fatal("the upstream was not cancelled when the client left")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if elapsed := time.Since(left); elapsed > 3*time.Second {
				t.Fatalf("cancelling took %s", elapsed)
			}
			var callID string
			if err := e.pool.QueryRow(context.Background(), `SELECT id FROM calls ORDER BY created_at DESC LIMIT 1`).Scan(&callID); err != nil {
				t.Fatal(err)
			}
			e.waitFinished(callID, left)
			if row := e.callRow(callID); row.Outcome != "client_disconnected" || row.HasLedgerTx || row.CostNano != 0 {
				t.Fatalf("call = %+v", row)
			}
			if e.balance(consumer) != "0" {
				t.Fatalf("balance = %s", e.balance(consumer))
			}
		})
	}
}

// The channel's concurrency slot stays taken while the gateway reads on, and
// is given back when the call has been settled.
func TestGatewayHoldsTheChannelSlotWhileItReadsOnAfterTheClientLeft(t *testing.T) {
	e := newGroupEnvWith(t, func(d *gateway.Dependencies) { d.DrainWindow = 2 * time.Second })
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if !strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, chatBody)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chatFrame)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never ends by itself: only the drain window stops it
	})
	e.createChannel(sharer, channelSpec{name: "single", upstream: upstream, advanced: map[string]any{"concurrency_limit": 1}})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()

	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	stream.event()
	left := time.Now()
	stream.hangUp()
	time.Sleep(300 * time.Millisecond)
	busy := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if busy.Code != http.StatusServiceUnavailable || !strings.Contains(busy.Body.String(), `"no_channel_available"`) {
		t.Fatalf("call while the slot is held = %d %s", busy.Code, busy.Body.String())
	}
	e.waitFinished(stream.requestID(), left)
	free := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if free.Code != http.StatusOK {
		t.Fatalf("call after the drain = %d %s", free.Code, free.Body.String())
	}
}

func TestGatewayDoesNotBlameTheChannelForAnUpstreamBreakWhileItReadsOn(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	breaking := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "100000")
		_, _ = io.WriteString(w, chatFrame)
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
		panic(http.ErrAbortHandler)
	})
	id := e.createChannel(sharer, channelSpec{name: "breaking", upstream: breaking, advanced: map[string]any{"cooldown_failures": 1, "cooldown_seconds": 60}})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()
	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	stream.event()
	left := time.Now()
	stream.hangUp()
	e.waitFinished(stream.requestID(), left)
	if row := e.callRow(stream.requestID()); row.Outcome != "client_disconnected" || row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if e.cooling(id) {
		t.Fatal("an upstream break after the client left was counted against the channel")
	}
}

// Clients usually hang up as soon as they have the last frame, before the
// upstream has closed its own side. That call is complete.
func TestGatewayCountsACallAsCompleteWhenTheClientHangsUpOnTheLastFrame(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	lingering := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chatUsageTail)
		w.(http.Flusher).Flush()
		time.Sleep(600 * time.Millisecond) // the connection stays open after [DONE]
	})
	e.createChannel(sharer, channelSpec{name: "lingering", upstream: lingering})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()
	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	var last string
	for last != "data: [DONE]\n\n" {
		last = stream.event()
	}
	left := time.Now()
	stream.hangUp()
	e.waitFinished(stream.requestID(), left)
	row := e.callRow(stream.requestID())
	if row.Outcome != "succeeded" || row.Input != 1000 || row.Output != 500 || !row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if reason := e.attemptField(stream.requestID(), 0, "end_reason"); reason != "completed" {
		t.Fatalf("attempt end reason = %q", reason)
	}
}
