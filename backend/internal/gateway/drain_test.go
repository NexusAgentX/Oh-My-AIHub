package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestDrainWindowIsTenSeconds(t *testing.T) {
	if drainWindow != 10*time.Second {
		t.Fatalf("drain window = %s, ADR-0035 says 10 seconds", drainWindow)
	}
	if got := NewEngine(Dependencies{}).DrainWindow; got != drainWindow {
		t.Fatalf("default drain window = %s", got)
	}
}

// ---- the client watch ----

type cancelRecorder struct {
	cause chan error
}

func newCancelRecorder() *cancelRecorder { return &cancelRecorder{cause: make(chan error, 4)} }

func (c *cancelRecorder) cancel(cause error) { c.cause <- cause }

func (c *cancelRecorder) wait(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case cause := <-c.cause:
		return cause
	case <-time.After(within):
		t.Fatal("the attempt was not cancelled")
		return nil
	}
}

func (c *cancelRecorder) expectNone(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case cause := <-c.cause:
		t.Fatalf("the attempt was cancelled: %v", cause)
	case <-time.After(within):
	}
}

func TestClientLeavingBeforeTheAnswerBeginsCancelsTheAttemptAtOnce(t *testing.T) {
	client, leave := context.WithCancel(context.Background())
	recorder := newCancelRecorder()
	watch := watchClient(client, recorder.cancel, time.Hour)
	defer watch.stop()
	leave()
	if cause := recorder.wait(t, time.Second); !errors.Is(cause, errClientGone) {
		t.Fatalf("cause = %v", cause)
	}
	if watch.commit() {
		t.Fatal("committed to a client that has left")
	}
	if !watch.isGone() {
		t.Fatal("the client is not recorded as gone")
	}
}

func TestClientLeavingAfterTheAnswerBeganLetsTheAttemptRunForTheWindow(t *testing.T) {
	client, leave := context.WithCancel(context.Background())
	recorder := newCancelRecorder()
	watch := watchClient(client, recorder.cancel, 150*time.Millisecond)
	defer watch.stop()
	if !watch.commit() {
		t.Fatal("client reported gone before it left")
	}
	left := time.Now()
	leave()
	recorder.expectNone(t, 50*time.Millisecond)
	if cause := recorder.wait(t, time.Second); !errors.Is(cause, errDrainDeadline) {
		t.Fatalf("cause = %v", cause)
	}
	if elapsed := time.Since(left); elapsed < 140*time.Millisecond {
		t.Fatalf("the attempt was cut off after %s", elapsed)
	}
}

func TestAFailedWriteStartsTheDrainOnce(t *testing.T) {
	recorder := newCancelRecorder()
	watch := watchClient(context.Background(), recorder.cancel, 100*time.Millisecond)
	defer watch.stop()
	watch.commit()
	watch.left()
	watch.left() // a second report must not restart or double the window
	if cause := recorder.wait(t, time.Second); !errors.Is(cause, errDrainDeadline) {
		t.Fatalf("cause = %v", cause)
	}
	recorder.expectNone(t, 200*time.Millisecond)
}

func TestStoppingTheWatchEndsItsClocks(t *testing.T) {
	client, leave := context.WithCancel(context.Background())
	recorder := newCancelRecorder()
	watch := watchClient(client, recorder.cancel, 50*time.Millisecond)
	watch.commit()
	watch.left()
	watch.stop()
	leave()
	recorder.expectNone(t, 200*time.Millisecond)

	again := newCancelRecorder()
	// A watcher on an already cancelled context may fire before stop is called.
	// Use a live context to test that stop prevents future callbacks.
	other, cancelOther := context.WithCancel(context.Background())
	defer cancelOther()
	stopped := watchClient(other, again.cancel, time.Millisecond)
	stopped.stop()
	stopped.left()
	again.expectNone(t, 50*time.Millisecond)
}

// ---- answering and settling a call whose client leaves ----

// scriptedBody is an upstream response body fed by the test; it fails like a
// cancelled HTTP body once the attempt's context ends.
type scriptedBody struct {
	ctx    context.Context
	chunks chan []byte
	fail   chan error
	// endsCleanlyOnCancel makes a cancelled attempt end with io.EOF instead of
	// an error: a transport being torn down can hand its reader the last bytes
	// of a peer that has seen it hang up, and then a plain end of body.
	endsCleanlyOnCancel bool
}

func (b *scriptedBody) Read(p []byte) (int, error) {
	select {
	case chunk, ok := <-b.chunks:
		if !ok {
			return 0, io.EOF
		}
		return copy(p, chunk), nil
	case err := <-b.fail:
		return 0, err
	case <-b.ctx.Done():
		if b.endsCleanlyOnCancel {
			return 0, io.EOF
		}
		return 0, b.ctx.Err()
	}
}

func (b *scriptedBody) Close() error { return nil }

// scriptedClient is the client side: it records what was written and can stop
// accepting writes.
type scriptedClient struct {
	mutex   sync.Mutex
	header  http.Header
	body    strings.Builder
	broken  bool
	writes  int
	failAt  int // 1-based number of the first failing write; 0 never
	written chan struct{}
}

func newScriptedClient() *scriptedClient {
	return &scriptedClient{header: http.Header{}, written: make(chan struct{}, 100)}
}

func (c *scriptedClient) Header() http.Header { return c.header }
func (c *scriptedClient) WriteHeader(int)     {}
func (c *scriptedClient) Flush()              {}

func (c *scriptedClient) Write(p []byte) (int, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.writes++
	if c.broken || (c.failAt > 0 && c.writes >= c.failAt) {
		return 0, io.ErrClosedPipe
	}
	c.body.Write(p)
	c.written <- struct{}{}
	return len(p), nil
}

func (c *scriptedClient) received() string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.body.String()
}

type drainRun struct {
	t        *testing.T
	engine   *Engine
	store    *speedStore
	client   *scriptedClient
	leave    context.CancelFunc
	body     *scriptedBody
	cancel   context.CancelCauseFunc
	result   attemptResult
	call     *callState
	candid   Candidate
	done     chan struct{}
	finished time.Time
	started  time.Time
}

// startDrainRun starts streamBack on a scripted upstream answer of the given
// format whose first chunk has been read already.
func startDrainRun(t *testing.T, format channel.Format, window time.Duration, first string, mutate func(*drainRun)) *drainRun {
	t.Helper()
	run := &drainRun{t: t, store: &speedStore{}, client: newScriptedClient(), done: make(chan struct{})}
	run.engine = NewEngine(Dependencies{Store: run.store, DrainWindow: window})
	clientCtx, leave := context.WithCancel(context.Background())
	run.leave = leave
	upstream, cancel := context.WithCancelCause(context.Background())
	run.cancel = cancel
	run.body = &scriptedBody{ctx: upstream, chunks: make(chan []byte, 16), fail: make(chan error, 1)}
	contentType := "text/event-stream"
	run.call = &callState{id: "call", started: time.Now(), stream: true, format: format, key: KeyAuth{OwnerID: "consumer"}, attempts: []Attempt{{}}}
	run.candid = Candidate{ChannelID: "channel", OwnerID: "sharer", MultiplierNano: ledger.UnitRate, RateNano: ledger.UnitRate}
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: run.body}
	run.result = attemptResult{
		response: response, first: []byte(first), upstream: upstream, started: time.Now(), cancel: cancel,
		watch: watchClient(clientCtx, cancel, window),
	}
	if mutate != nil {
		mutate(run)
	}
	model := catalog.Model{ID: "main", InputPrice: money.FromNano(10_000_000_000), OutputPrice: money.FromNano(20_000_000_000)}
	run.candid = quotedCandidate(t, run.candid, model)
	// A cooldown threshold of one makes any failure counted against the channel visible.
	defaults := settings.Settings{DefaultCooldownFailures: 1, DefaultCooldownSeconds: 60}
	run.started = time.Now()
	go func() {
		run.engine.streamBack(run.client, run.call, model, run.candid, run.result, defaults)
		run.finished = time.Now()
		close(run.done)
	}()
	return run
}

func (r *drainRun) send(chunk string) { r.body.chunks <- []byte(chunk) }
func (r *drainRun) end()              { close(r.body.chunks) }

func (r *drainRun) wait(within time.Duration) {
	r.t.Helper()
	select {
	case <-r.done:
	case <-time.After(within):
		r.t.Fatal("streamBack did not return")
	}
}

func (r *drainRun) waitWritten(n int) {
	r.t.Helper()
	for range n {
		select {
		case <-r.client.written:
		case <-time.After(time.Second):
			r.t.Fatal("chunk was not written to the client")
		}
	}
}

func (r *drainRun) channelFailed() bool {
	return !r.engine.Runtime.CooldownUntil(r.candid.ChannelID, time.Now()).IsZero()
}

const (
	chatContent = "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"
	chatRest    = "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\ndata: [DONE]\n\n"
	anthropicStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n"
	anthropicDelta = "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":500}}\n\n"
)

func TestChatUsageThatArrivesAfterTheClientLeftIsBilled(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, nil)
	run.waitWritten(1)
	run.leave()
	time.Sleep(50 * time.Millisecond) // the upstream is still talking
	run.send(chatRest)
	run.end()
	run.wait(2 * time.Second)

	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Usage.InputTokens != 1000 || finish.Usage.OutputTokens != 500 || finish.Bill == nil {
		t.Fatalf("finish = %+v", finish)
	}
	// The client got what was written before it left, and response_bytes
	// counts only that.
	if run.client.received() != chatContent || finish.ResponseBytes != int64(len(chatContent)) {
		t.Fatalf("client received %q, response bytes %d", run.client.received(), finish.ResponseBytes)
	}
	if reason := run.call.attempts[0].EndReason; reason != EndClientDisconnected {
		t.Fatalf("attempt end reason = %q", reason)
	}
	if run.channelFailed() {
		t.Fatal("the client's leaving was counted against the channel")
	}
	if run.finished.Sub(run.started) > 2*time.Second {
		t.Fatalf("the drain waited for the window although the answer had ended: %s", run.finished.Sub(run.started))
	}
}

func TestChatUsageThatNeverArrivesIsNotBilled(t *testing.T) {
	window := 200 * time.Millisecond
	run := startDrainRun(t, channel.FormatOpenAIChat, window, chatContent, nil)
	run.waitWritten(1)
	run.leave()
	run.wait(3 * time.Second)
	if elapsed := run.finished.Sub(run.started); elapsed < window-20*time.Millisecond {
		t.Fatalf("the drain ended after %s, before its window", elapsed)
	}
	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Bill != nil || finish.Usage.InputTokens != 0 || finish.Usage.OutputTokens != 0 {
		t.Fatalf("finish = %+v", finish)
	}
	if run.channelFailed() {
		t.Fatal("the drain window was counted against the channel")
	}
}

func TestAnthropicBillsOutputFromTheMessageDeltaOnlyIfItArrivesInTime(t *testing.T) {
	for _, tc := range []struct {
		name         string
		deltaInTime  bool
		wantOutputAt int64
	}{
		{"delta within the window", true, 500},
		{"delta never comes", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := startDrainRun(t, channel.FormatAnthropic, 200*time.Millisecond, anthropicStart, nil)
			run.waitWritten(1)
			run.leave()
			if tc.deltaInTime {
				time.Sleep(30 * time.Millisecond)
				run.send(anthropicDelta)
				run.end()
			}
			run.wait(3 * time.Second)
			finish := run.store.finish
			if finish.Outcome != OutcomeClientDisconnect || finish.Usage.InputTokens != 1000 || finish.Usage.OutputTokens != tc.wantOutputAt || finish.Bill == nil {
				t.Fatalf("finish = %+v", finish)
			}
		})
	}
}

func TestAnUpstreamErrorWhileDrainingIsNotTheChannelsFault(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, nil)
	run.waitWritten(1)
	run.leave()
	time.Sleep(30 * time.Millisecond)
	run.body.fail <- errors.New("connection reset by peer")
	run.wait(2 * time.Second)
	if run.store.finish.Outcome != OutcomeClientDisconnect || run.channelFailed() {
		t.Fatalf("finish = %+v, channel failed %v", run.store.finish, run.channelFailed())
	}
}

func TestAClientThatCannotBeWrittenToStartsTheDrain(t *testing.T) {
	// The second write fails, as on a connection the peer has reset, although
	// the request context has not been cancelled yet.
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, func(r *drainRun) { r.client.failAt = 2 })
	run.waitWritten(1)
	run.send(chatRest)
	run.end()
	run.wait(2 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Usage.OutputTokens != 500 || finish.Bill == nil {
		t.Fatalf("finish = %+v", finish)
	}
	if run.client.received() != chatContent {
		t.Fatalf("client received %q", run.client.received())
	}
}

// Clients commonly hang up as soon as they have the final frame, before the
// upstream has closed its side; that call is complete, not disconnected.
func TestAClientThatLeavesAfterReceivingEverythingIsNotADisconnect(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, nil)
	run.waitWritten(1)
	run.send(chatRest)
	run.waitWritten(1)
	run.leave()
	time.Sleep(50 * time.Millisecond)
	run.end()
	run.wait(2 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeSucceeded || finish.Usage.OutputTokens != 500 || finish.Bill == nil {
		t.Fatalf("finish = %+v", finish)
	}
	if run.call.attempts[0].EndReason != EndCompleted {
		t.Fatalf("attempt end reason = %q", run.call.attempts[0].EndReason)
	}
}

func TestTheIdleTimeoutAlsoEndsTheDrain(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, func(r *drainRun) {
		r.result.idleAfter = 80 * time.Millisecond
		r.result.idle = time.AfterFunc(r.result.idleAfter, func() { r.cancel(errIdle) })
	})
	run.waitWritten(1)
	run.leave()
	run.wait(2 * time.Second)
	if elapsed := run.finished.Sub(run.started); elapsed > time.Second {
		t.Fatalf("the drain outlived the idle timeout: %s", elapsed)
	}
	if run.store.finish.Outcome != OutcomeClientDisconnect || run.channelFailed() {
		t.Fatalf("finish = %+v, channel failed %v", run.store.finish, run.channelFailed())
	}
}

func TestACallTimeLimitDuringTheDrainEndsItWithoutBlamingTheChannel(t *testing.T) {
	run := startDrainRun(t, channel.FormatAnthropic, 5*time.Second, anthropicStart, nil)
	run.waitWritten(1)
	run.leave()
	time.Sleep(30 * time.Millisecond)
	run.cancel(errCallDeadline)
	run.wait(2 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Usage.InputTokens != 1000 || run.channelFailed() {
		t.Fatalf("finish = %+v, channel failed %v", finish, run.channelFailed())
	}
}

func TestAClientThatLeftBeforeTheFirstWriteIsNeverBilled(t *testing.T) {
	run := startDrainRun(t, channel.FormatAnthropic, 5*time.Second, anthropicStart, func(r *drainRun) {
		// The client leaves between the first byte and the first write: the
		// watch cancels the attempt and streamBack must not write anything.
		r.leave()
		deadline := time.After(time.Second)
		for !r.result.watch.isGone() {
			select {
			case <-deadline:
				t.Fatal("the watch did not notice the client")
			case <-time.After(time.Millisecond):
			}
		}
	})
	run.wait(2 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Bill != nil || finish.Usage.InputTokens != 0 || run.client.received() != "" {
		t.Fatalf("finish = %+v, client received %q", finish, run.client.received())
	}
	if run.call.attempts[0].EndReason != EndClientDisconnected {
		t.Fatalf("attempt end reason = %q", run.call.attempts[0].EndReason)
	}
}
