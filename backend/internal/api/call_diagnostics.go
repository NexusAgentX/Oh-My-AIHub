package api

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"

type attemptTimeoutsJSON struct {
	FirstByteMS int64 `json:"first_byte_ms"`
	IdleMS      int64 `json:"idle_ms"`
	TotalMS     int64 `json:"total_ms"`
}

func newAttemptTimeoutsJSON(v *gateway.AttemptTimeouts) *attemptTimeoutsJSON {
	if v == nil {
		return nil
	}
	return &attemptTimeoutsJSON{v.FirstByteMS, v.IdleMS, v.TotalMS}
}

type stageTimingJSON struct {
	OffsetMS   *float64 `json:"offset_ms"`
	Stage      string   `json:"stage"`
	DurationMS float64  `json:"duration_ms"`
}
type routeDecisionJSON struct {
	Channel channelRefJSON `json:"channel"`
	Reason  string         `json:"reason"`
	Rank    int            `json:"rank"`
}
type callDiagnosticsJSON struct {
	Operation            string              `json:"operation,omitempty"`
	RequiredCapabilities []string            `json:"required_capabilities,omitempty"`
	BoundResource        bool                `json:"bound_resource,omitempty"`
	Network              *gateway.Network    `json:"network"`
	Stages               []stageTimingJSON   `json:"stages"`
	Routing              []routeDecisionJSON `json:"routing"`
	StopReason           string              `json:"stop_reason"`
	UsageStatus          string              `json:"usage_status"`
	Delivery             string              `json:"delivery"`
	Frames               int                 `json:"frames"`
	MaxFrameGapMS        *int                `json:"max_frame_gap_ms"`
	UpstreamBytes        int64               `json:"upstream_bytes"`
	MaxAttempts          int                 `json:"max_attempts"`
	CallTimeoutMS        int                 `json:"call_timeout_ms"`
	DrainTimeoutMS       int                 `json:"drain_timeout_ms"`
}

func newCallDiagnosticsJSON(v *gateway.Diagnostics) *callDiagnosticsJSON {
	if v == nil {
		return nil
	}
	d := &callDiagnosticsJSON{Operation: v.Operation, RequiredCapabilities: v.RequiredCapabilities, BoundResource: v.BoundResource, Network: v.Network, Stages: []stageTimingJSON{}, Routing: []routeDecisionJSON{}, StopReason: v.StopReason, UsageStatus: v.UsageStatus, Delivery: v.Delivery, Frames: v.Frames, MaxFrameGapMS: v.MaxFrameGapMS, UpstreamBytes: v.UpstreamBytes, MaxAttempts: v.MaxAttempts, CallTimeoutMS: v.CallTimeoutMS, DrainTimeoutMS: v.DrainTimeoutMS}
	for _, s := range v.Stages {
		d.Stages = append(d.Stages, stageTimingJSON{OffsetMS: s.OffsetMS, Stage: s.Stage, DurationMS: s.DurationMS})
	}
	for _, s := range v.Routing {
		d.Routing = append(d.Routing, routeDecisionJSON{channelRefJSON{s.Channel.ID, s.Channel.Name}, s.Reason, s.Rank})
	}
	return d
}
