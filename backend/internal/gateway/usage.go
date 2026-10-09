package gateway

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

const (
	// maxJSONBuffer bounds how much of a non-event-stream response is kept to
	// read its usage; a larger body simply yields no usage (and no charge).
	maxJSONBuffer = 16 << 20
	// maxLineBuffer bounds one SSE line.
	maxLineBuffer = 4 << 20
	maxIntervals  = 200_000
)

// Observation is what the side-channel reader learned about a response.
type Observation struct {
	Usage      ledger.Usage
	Found      bool
	ResponseID string
	Frames     int
	// IntervalP50MS and IntervalP95MS describe the gaps between data frames.
	IntervalP50MS *int
	IntervalP95MS *int
}

// Observer scans a response as it streams past, without ever altering it and
// without validating its shape: anything it cannot parse is ignored.
type Observer struct {
	format channel.Format
	stream bool
	sse    bool

	line      []byte
	skipLine  bool
	buffer    []byte
	overflow  bool
	last      time.Time
	intervals []time.Duration
	frames    int

	detail         ledger.UsageDetail
	qwenObserved   bool
	anthropicSpeed string
	anthropicTier  string
	usage          ledger.Usage
	found          bool
	responseID     string
	// anthropic reports its fields in two events; track which were seen.
	anthropic anthropicUsage
}

type anthropicUsage struct{ input, output, cacheWrite, cacheRead int64 }

// NewObserver starts a reader for one response of the given format.
// stream reports whether the client asked for a streamed response.
func NewObserver(format channel.Format, stream bool, contentType string) *Observer {
	return &Observer{format: format, stream: stream, sse: strings.Contains(strings.ToLower(contentType), "text/event-stream")}
}

// Write feeds one chunk that arrived at the given time.
func (o *Observer) Write(chunk []byte, at time.Time) {
	if o.sse {
		o.writeSSE(chunk, at)
		return
	}
	if o.stream {
		o.frame(at)
	}
	if o.overflow {
		return
	}
	if len(o.buffer)+len(chunk) > maxJSONBuffer {
		o.overflow, o.buffer = true, nil
		return
	}
	o.buffer = append(o.buffer, chunk...)
}

func (o *Observer) frame(at time.Time) {
	o.frames++
	if !o.last.IsZero() && len(o.intervals) < maxIntervals {
		o.intervals = append(o.intervals, at.Sub(o.last))
	}
	o.last = at
}

func (o *Observer) writeSSE(chunk []byte, at time.Time) {
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			if !o.skipLine {
				if len(o.line)+len(chunk) > maxLineBuffer {
					o.skipLine, o.line = true, nil
				} else {
					o.line = append(o.line, chunk...)
				}
			}
			return
		}
		if !o.skipLine {
			if len(o.line)+newline <= maxLineBuffer {
				o.line = append(o.line, chunk[:newline]...)
				o.handleLine(o.line, at)
			}
		}
		o.line, o.skipLine = o.line[:0], false
		chunk = chunk[newline+1:]
	}
}

func (o *Observer) handleLine(line []byte, at time.Time) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || payload[0] != '{' {
		return
	}
	o.frame(at)
	o.absorb(payload)
}

// Finish parses whatever was buffered (non-event-stream bodies) and returns
// the observation.
func (o *Observer) Finish() Observation {
	if o.sse && len(o.line) > 0 && !o.skipLine {
		o.handleLine(o.line, o.last)
		o.line = nil
	}
	if !o.sse && !o.overflow && len(o.buffer) > 0 {
		body := bytes.TrimSpace(o.buffer)
		switch {
		case len(body) > 0 && body[0] == '{':
			o.absorb(body)
		case len(body) > 0 && body[0] == '[':
			var elements []json.RawMessage
			if json.Unmarshal(body, &elements) == nil {
				for _, element := range elements {
					o.absorb(element)
				}
			}
		}
	}
	if o.qwenObserved && o.detail.ThinkingMode == "" {
		o.detail.ThinkingMode = "qwen_non_thinking"
	}
	if len(o.detail.Tokens) > 0 || o.detail.RequestedServiceTier != "" || o.detail.ServiceTier != "" || o.detail.ThinkingMode != "" || len(o.detail.Notes) > 0 {
		o.usage.Detail = &o.detail
	}
	observation := Observation{Usage: o.usage, Found: o.found, ResponseID: o.responseID, Frames: o.frames}
	if len(o.intervals) > 0 {
		sorted := append([]time.Duration(nil), o.intervals...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		p50 := int(percentile(sorted, 0.50).Milliseconds())
		p95 := int(percentile(sorted, 0.95).Milliseconds())
		observation.IntervalP50MS, observation.IntervalP95MS = &p50, &p95
	}
	return observation
}

func percentile(sorted []time.Duration, fraction float64) time.Duration {
	index := int(float64(len(sorted)-1)*fraction + 0.5)
	return sorted[index]
}

func clamp(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

// absorb reads usage (and the response ID) out of one JSON object.
func (o *Observer) absorb(raw []byte) {
	defer o.absorbDetails(raw)
	switch o.format {
	case channel.FormatOpenAIChat:
		o.absorbChat(raw)
	case channel.FormatOpenAIResponses:
		o.absorbResponses(raw)
	case channel.FormatAnthropic:
		o.absorbAnthropic(raw)
	case channel.FormatGemini:
		o.absorbGemini(raw)
	}
}

type tokenDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func (o *Observer) absorbChat(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	var value struct {
		Usage *struct {
			PromptTokens        int64         `json:"prompt_tokens"`
			CompletionTokens    int64         `json:"completion_tokens"`
			PromptTokensDetails *tokenDetails `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Usage == nil {
		return
	}
	cached, written := int64(0), int64(0)
	if value.Usage.PromptTokensDetails != nil {
		cached = value.Usage.PromptTokensDetails.CachedTokens
		written = value.Usage.PromptTokensDetails.CacheWriteTokens
	}
	o.usage = ledger.Usage{
		InputTokens: clamp(value.Usage.PromptTokens - cached - written), OutputTokens: clamp(value.Usage.CompletionTokens), CacheReadTokens: clamp(cached), CacheWriteTokens: clamp(written),
	}
	o.found = true
}

type responsesUsage struct {
	InputTokens        int64         `json:"input_tokens"`
	OutputTokens       int64         `json:"output_tokens"`
	InputTokensDetails *tokenDetails `json:"input_tokens_details"`
}

func (o *Observer) setResponsesUsage(usage *responsesUsage) {
	if usage == nil {
		return
	}
	cached, written := int64(0), int64(0)
	if usage.InputTokensDetails != nil {
		cached = usage.InputTokensDetails.CachedTokens
		written = usage.InputTokensDetails.CacheWriteTokens
	}
	o.usage = ledger.Usage{InputTokens: clamp(usage.InputTokens - cached - written), OutputTokens: clamp(usage.OutputTokens), CacheReadTokens: clamp(cached), CacheWriteTokens: clamp(written)}
	o.found = true
}

func (o *Observer) absorbResponses(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) && !bytes.Contains(raw, []byte(`"id"`)) {
		return
	}
	var value struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Usage    *responsesUsage `json:"usage"`
		Response *struct {
			ID    string          `json:"id"`
			Usage *responsesUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	if value.Response != nil {
		if value.Response.ID != "" {
			o.responseID = value.Response.ID
		}
		o.setResponsesUsage(value.Response.Usage)
	}
	if value.Type == "" {
		if value.ID != "" {
			o.responseID = value.ID
		}
		o.setResponsesUsage(value.Usage)
	}
}

type anthropicFields struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheCreation   *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputs *int64 `json:"cache_read_input_tokens"`
}

func (o *Observer) applyAnthropic(fields *anthropicFields) {
	if fields == nil {
		return
	}
	if fields.InputTokens != nil {
		o.anthropic.input = clamp(*fields.InputTokens)
	}
	if fields.OutputTokens != nil {
		o.anthropic.output = clamp(*fields.OutputTokens)
	}
	if fields.CacheCreation != nil {
		o.anthropic.cacheWrite = clamp(*fields.CacheCreation)
	}
	if fields.CacheReadInputs != nil {
		o.anthropic.cacheRead = clamp(*fields.CacheReadInputs)
	}
	o.usage = ledger.Usage{
		InputTokens: o.anthropic.input, OutputTokens: o.anthropic.output,
		CacheWriteTokens: o.anthropic.cacheWrite, CacheReadTokens: o.anthropic.cacheRead,
	}
	o.found = true
}

func (o *Observer) absorbAnthropic(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	var value struct {
		Usage   *anthropicFields `json:"usage"`
		Message *struct {
			Usage *anthropicFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	if value.Message != nil {
		o.applyAnthropic(value.Message.Usage)
	}
	o.applyAnthropic(value.Usage)
}

func (o *Observer) absorbGemini(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usageMetadata"`)) {
		return
	}
	var value struct {
		UsageMetadata *struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(raw, &value) != nil || value.UsageMetadata == nil {
		return
	}
	usage := value.UsageMetadata
	o.usage = ledger.Usage{
		InputTokens:     clamp(usage.PromptTokenCount - usage.CachedContentTokenCount),
		OutputTokens:    clamp(usage.CandidatesTokenCount + usage.ThoughtsTokenCount),
		CacheReadTokens: clamp(usage.CachedContentTokenCount),
	}
	o.found = true
}
