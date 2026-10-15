package gateway

import (
	"context"
	"net/http"
	"time"
)

// Diagnostics contains only bounded metadata; never payloads, URLs or credentials.
// Nil on old records means unmeasured, not zero.
type Diagnostics struct {
	Network        *Network        `json:"network"`
	Stages         []StageTiming   `json:"stages"`
	Routing        []RouteDecision `json:"routing"`
	StopReason     string          `json:"stop_reason"`
	UsageStatus    string          `json:"usage_status"`
	Delivery       string          `json:"delivery"`
	Frames         int             `json:"frames"`
	MaxFrameGapMS  *int            `json:"max_frame_gap_ms"`
	UpstreamBytes  int64           `json:"upstream_bytes"`
	MaxAttempts    int             `json:"max_attempts"`
	CallTimeoutMS  int             `json:"call_timeout_ms"`
	DrainTimeoutMS int             `json:"drain_timeout_ms"`
}
type StageTiming struct {
	OffsetMS   *float64 `json:"offset_ms"`
	Stage      string   `json:"stage"`
	DurationMS float64  `json:"duration_ms"`
}
type RouteDecision struct {
	Channel AttemptChannel `json:"channel"`
	Reason  string         `json:"reason"`
	Rank    int            `json:"rank"`
}
type AttemptTimeouts struct {
	FirstByteMS int64 `json:"first_byte_ms"`
	IdleMS      int64 `json:"idle_ms"`
	TotalMS     int64 `json:"total_ms"`
}

// Telemetry is a synchronous in-memory recorder. It must never perform I/O.
// It is independent from the lossy live UI event bus and persistence success.
type Telemetry interface {
	Active(format string, delta float64)
	Complete(Completion)
}
type Completion struct {
	Format   string
	Stream   bool
	Status   int
	Duration time.Duration
	Finish   CallFinish
	Booked   bool
}

type observedWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *observedWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (c *callState) stage(name string, since time.Time, now time.Time) {
	if c.phase == name {
		c.phase = ""
	}
	for i := range c.diagnostics.Stages {
		if c.diagnostics.Stages[i].Stage == name {
			c.diagnostics.Stages[i].OffsetMS = nil // repeated disjoint work is an aggregate, not one span
			c.diagnostics.Stages[i].DurationMS += float64(now.Sub(since)) / float64(time.Millisecond)
			return
		}
	}
	offset := max(0, float64(since.Sub(c.started))/float64(time.Millisecond))
	c.diagnostics.Stages = append(c.diagnostics.Stages, StageTiming{Stage: name, OffsetMS: &offset, DurationMS: float64(now.Sub(since)) / float64(time.Millisecond)})
}
func (e *Engine) finishCall(ctx context.Context, call *callState, finish CallFinish) (FinishResult, error) {
	if call.phase != "" {
		call.stage(call.phase, call.phaseAt, e.Now())
	}
	e.resolveNetwork(call.diagnostics.Network)
	finish.Diagnostics = &call.diagnostics
	call.finish = finish
	at := e.Now()
	result, err := e.Store.FinishCall(ctx, finish)
	if err == nil && result.Finished {
		e.scheduleNetwork(finish.ID, call.diagnostics.Network)
	}
	call.stage("settlement", at, e.Now())
	call.booked = err == nil && result.TxID != ""
	return result, err
}
