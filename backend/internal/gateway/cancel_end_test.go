package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// When an attempt is cancelled the HTTP transport closes its connection, and
// closing a TLS connection first tells the peer (close_notify). A peer that is
// waiting for exactly that finishes its chunked body, and the reader that is
// still blocked on the connection receives those last bytes and a plain
// io.EOF instead of an error. These tests give the gateway that outcome on
// purpose: an end of body that comes after a cancellation must be judged by
// the cancellation, never taken as a complete answer.

func TestAnEndOfBodyAfterTheDrainWindowIsStillADisconnect(t *testing.T) {
	run := startDrainRun(t, channel.FormatAnthropic, 100*time.Millisecond, anthropicStart, func(r *drainRun) { r.body.endsCleanlyOnCancel = true })
	run.waitWritten(1)
	run.leave()
	run.wait(3 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeClientDisconnect || finish.Usage.InputTokens != 1000 || finish.Usage.OutputTokens != 1 || run.channelFailed() {
		t.Fatalf("finish = %+v, channel failed %v", finish, run.channelFailed())
	}
	if reason := run.call.attempts[0].EndReason; reason != EndClientDisconnected {
		t.Fatalf("attempt end reason = %q", reason)
	}
}

func TestAnEndOfBodyAfterTheCallLimitIsStillTheCallLimit(t *testing.T) {
	run := startDrainRun(t, channel.FormatAnthropic, 5*time.Second, anthropicStart, func(r *drainRun) { r.body.endsCleanlyOnCancel = true })
	run.waitWritten(1)
	run.cancel(errCallDeadline)
	run.wait(2 * time.Second)
	finish := run.store.finish
	if finish.Outcome != OutcomeInterrupted || finish.Usage.InputTokens != 1000 || finish.Bill == nil || run.channelFailed() {
		t.Fatalf("finish = %+v, channel failed %v", finish, run.channelFailed())
	}
	attempt := run.call.attempts[0]
	if attempt.EndReason != EndInterrupted || attempt.ErrorCode == nil || *attempt.ErrorCode != codeCallDeadline {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestAnEndOfBodyAfterTheIdleTimeoutIsStillAnIdleStream(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, func(r *drainRun) {
		r.body.endsCleanlyOnCancel = true
		r.result.idleAfter = 50 * time.Millisecond
		r.result.idle = time.AfterFunc(r.result.idleAfter, func() { r.cancel(errIdle) })
	})
	run.wait(3 * time.Second)
	attempt := run.call.attempts[0]
	if run.store.finish.Outcome != OutcomeInterrupted || attempt.ErrorCode == nil || *attempt.ErrorCode != "timeout_idle" || !run.channelFailed() {
		t.Fatalf("finish = %+v, attempt = %+v, channel failed %v", run.store.finish, attempt, run.channelFailed())
	}
}

func TestAnEndOfBodyWithNoCancellationIsStillACompleteAnswer(t *testing.T) {
	run := startDrainRun(t, channel.FormatOpenAIChat, 5*time.Second, chatContent, func(r *drainRun) { r.body.endsCleanlyOnCancel = true })
	run.waitWritten(1)
	run.send(chatRest)
	run.end()
	run.wait(2 * time.Second)
	if run.store.finish.Outcome != OutcomeSucceeded || run.store.finish.Usage.OutputTokens != 500 {
		t.Fatalf("finish = %+v", run.store.finish)
	}
}

// ---- the first read of an attempt ----

// cleanEndBody blocks like a response whose upstream has stopped talking and
// ends with io.EOF the moment the request is cancelled.
type cleanEndBody struct{ ctx context.Context }

func (b cleanEndBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, io.EOF
}

func (cleanEndBody) Close() error { return nil }

type cleanEndTransport struct{}

func (cleanEndTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: cleanEndBody{request.Context()}, Request: request,
	}, nil
}

// emptyTransport answers 200 with a body that is already finished.
type emptyTransport struct{}

func (emptyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

type fakeOutbound struct {
	channel.Outbound
	transport http.RoundTripper
}

func (o fakeOutbound) GatewayClient(context.Context, string) (*http.Client, error) {
	return &http.Client{Transport: o.transport}, nil
}

func TestTheFirstReadOfAnAttemptJudgesAnEndOfBodyByTheCancellation(t *testing.T) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	keyring, err := channel.ParseKeyring("test="+base64.StdEncoding.EncodeToString(secret), "test")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := keyring.Encrypt("c", 1, "upstream-key")
	if err != nil {
		t.Fatal(err)
	}
	millis := func(v int32) *int32 { return &v }
	for _, tc := range []struct {
		name      string
		transport http.RoundTripper
		stream    bool
		advanced  channel.Advanced
		limit     time.Duration // the call time limit; zero for none
		wantEnd   string
		wantCode  string
		expired   bool
		answered  bool
	}{
		{"call limit", cleanEndTransport{}, false, channel.Advanced{}, 50 * time.Millisecond, EndInterrupted, codeCallDeadline, true, false},
		{"call limit on a stream", cleanEndTransport{}, true, channel.Advanced{}, 50 * time.Millisecond, EndInterrupted, codeCallDeadline, true, false},
		{"first-byte timeout", cleanEndTransport{}, true, channel.Advanced{TTFTTimeoutMS: millis(50)}, 0, EndTimeoutTTFT, "timeout_ttft", false, false},
		{"total timeout", cleanEndTransport{}, false, channel.Advanced{TotalTimeoutMS: millis(50)}, 0, EndTimeoutTotal, "timeout_total", false, false},
		{"an empty answer nobody cancelled", emptyTransport{}, false, channel.Advanced{}, 0, "", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewEngine(Dependencies{Keyring: keyring, Outbound: fakeOutbound{transport: tc.transport}})
			body := []byte(`{"model":"gpt-test"}`)
			_, scan, _ := ScanRequest(body)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			call := &callState{format: channel.FormatOpenAIChat, stream: tc.stream}
			candidate := Candidate{ChannelID: "c", BaseURL: "https://relay.example", UpstreamModel: "gpt-up", Credential: credential, Advanced: tc.advanced}
			ctx := context.Background()
			if tc.limit > 0 {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeoutCause(ctx, tc.limit, errCallDeadline)
				defer stop()
			}
			result := engine.attempt(ctx, request, call, candidate, Limits{}, settings.Settings{DefaultTotalTimeoutMS: 600000, DefaultTTFTTimeoutMS: 600000},
				engine.Outbound, body, scan, authBearer, 600*time.Second)
			if result.cancel != nil {
				defer result.cancel(nil)
			}
			if (result.response != nil) != tc.answered || result.expired != tc.expired {
				t.Fatalf("answered %v expired %v, record %+v", result.response != nil, result.expired, result.record)
			}
			if result.record.EndReason != tc.wantEnd || (tc.wantCode != "" && (result.record.ErrorCode == nil || *result.record.ErrorCode != tc.wantCode)) {
				t.Fatalf("record = %+v", result.record)
			}
		})
	}
}
