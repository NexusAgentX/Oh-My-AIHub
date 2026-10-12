package gateway

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStopMetadataDoesNotCopyContent(t *testing.T) {
	for _, tc := range []struct {
		format     channel.Format
		body, want string
	}{
		{channel.FormatOpenAIChat, `{"choices":[{"finish_reason":null,"delta":{"content":"finish_reason: SECRET"}}]}`, ""},
		{channel.FormatOpenAIChat, `{"choices":[{"finish_reason":"length"}]}`, "length"},
		{channel.FormatOpenAIChat, `{"choices":[{"finish_reason":"SECRET-provider-value"}]}`, "other"},
		{channel.FormatAnthropic, `{"delta":{"stop_reason":"tool_use"}}`, "tool_call"},
		{channel.FormatGemini, `{"candidates":[{"finishReason":"SAFETY"}]}`, "content_filter"},
		{channel.FormatOpenAIResponses, `{"object":"response","status":"completed"}`, "stop"},
		{channel.FormatOpenAIResponses, `{"type":"response.incomplete"}`, "incomplete"},
	} {
		o := NewObserver(tc.format, false, "application/json")
		o.Write([]byte(tc.body), time.Now())
		if got := o.Finish().StopReason; got != tc.want {
			t.Fatalf("%s: got %q", tc.body, got)
		}
	}
	o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
	normal := []byte(`{"choices":[{"finish_reason":null,"delta":{"content":"hello"}}]}`)
	if n := testing.AllocsPerRun(100, func() { o.absorb(normal) }); n != 0 {
		t.Fatalf("normal content frame allocated %v times", n)
	}
}

func TestFrameGapKeepsLateStallsAndParseLimit(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
	at := time.Unix(1000, 0)
	for i := 0; i < maxIntervals+2; i++ {
		o.frame(at.Add(time.Duration(i) * time.Millisecond))
	}
	o.frame(at.Add(time.Hour))
	if got := o.Finish().MaxFrameGapMS; got == nil || *got < 3000000 {
		t.Fatal("late stall lost after quantile sample cap")
	}
	o.Write([]byte("data: "+strings.Repeat("x", maxLineBuffer)+"\n\n"), at)
	if o.Finish().UsageStatus != "parse_limit" {
		t.Fatal("oversized completed SSE line not reported")
	}
}

type telemetryCapture struct {
	active      float64
	completions []Completion
}

func (m *telemetryCapture) Active(_ string, d float64) { m.active += d }
func (m *telemetryCapture) Complete(c Completion)      { m.completions = append(m.completions, c) }
func TestHTTPAuthFailureObservedWithoutCallRecord(t *testing.T) {
	m := &telemetryCapture{}
	e := NewEngine(Dependencies{Telemetry: m})
	w := httptest.NewRecorder()
	e.Serve(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), channel.FormatOpenAIChat, "", false)
	if w.Code != 401 || m.active != 0 || len(m.completions) != 1 || m.completions[0].Status != 401 {
		t.Fatalf("status=%d metric=%+v", w.Code, m)
	}
}

type failedFlush struct{ *httptest.ResponseRecorder }

func (w failedFlush) FlushError() error { return io.ErrClosedPipe }
func TestFlushFailureIsSeparateFromUsage(t *testing.T) {
	store := &speedStore{}
	e := NewEngine(Dependencies{Store: store})
	body := `{"usage":{"prompt_tokens":1,"completion_tokens":6},"choices":[{"finish_reason":"stop"}]}`
	call := &callState{started: time.Now().Add(-time.Second), format: channel.FormatOpenAIChat, attempts: []Attempt{{}}}
	w := &observedWriter{ResponseWriter: failedFlush{httptest.NewRecorder()}}
	e.streamBack(w, call, catalog.Model{}, Candidate{}, attemptResult{response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(""))}, first: []byte(body), firstErr: io.EOF, upstream: context.Background(), cancel: func(error) {}, watch: watchClient(context.Background(), func(error) {}, drainWindow), started: call.started}, settings.Settings{})
	f := store.finish
	if f.Outcome != OutcomeClientDisconnect || f.Diagnostics.Delivery != "client_disconnected" || f.Diagnostics.UsageStatus != "reported" || f.Usage.OutputTokens != 6 {
		t.Fatalf("lost independent outcomes: %+v %+v", f, f.Diagnostics)
	}
}
