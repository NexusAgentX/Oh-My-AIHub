package gateway

import (
	"bytes"
	"encoding/json"
	"slices"
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
	StopReason    string
	UsageStatus   string
	MaxFrameGapMS *int
	Usage         ledger.Usage
	Found         bool
	ResponseID    string
	Frames        int
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

	line         []byte
	skipLine     bool
	buffer       []byte
	overflow     bool
	parseLimited bool
	last         time.Time
	intervals    []time.Duration
	frames       int

	detail         ledger.UsageDetail
	qwenObserved   bool
	anthropicSpeed string
	anthropicTier  string
	usage          ledger.Usage
	found          bool
	responseID     string
	stopReason     string
	maxGap         time.Duration
	// anthropic reports its fields in two events; track which were seen.
	anthropic       anthropicUsage
	openai          openAIUsageState
	iterationsSeen  bool
	iterationOffset anthropicUsage
	iterationMask   uint8
	// members is the scratch space the members of an event's objects are
	// listed in (see objectOf).
	members []jsonMember
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
	if !o.last.IsZero() {
		gap := at.Sub(o.last)
		if len(o.intervals) < maxIntervals {
			o.intervals = append(o.intervals, gap)
		}
		if gap > o.maxGap {
			o.maxGap = gap
		}
	}
	o.last = at
}

func (o *Observer) writeSSE(chunk []byte, at time.Time) {
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			if !o.skipLine {
				if len(o.line)+len(chunk) > maxLineBuffer {
					o.skipLine, o.line, o.parseLimited = true, nil, true
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
			} else {
				o.parseLimited = true
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
	status := "missing"
	if o.found {
		status = "reported"
	} else if o.overflow || o.parseLimited {
		status = "parse_limit"
	}
	observation := Observation{StopReason: o.stopReason, UsageStatus: status, Usage: o.usage, Found: o.found, ResponseID: o.responseID, Frames: o.frames}
	if len(o.intervals) > 0 {
		observation.MaxFrameGapMS = millis(o.maxGap)
		sorted := slices.Clone(o.intervals)
		slices.Sort(sorted)
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
	// One look at the member names decides what is worth decoding at all.
	keys := scanFrameKeys(raw)
	if keys&keyStop != 0 || o.format == channel.FormatOpenAIResponses {
		o.observeStop(raw)
	}
	switch o.format {
	case channel.FormatOpenAIChat:
		if keys&keyUsage != 0 {
			o.absorbChat(raw)
		}
	case channel.FormatOpenAIResponses:
		if keys&(keyUsage|keyID) != 0 {
			o.absorbResponses(raw)
		}
	case channel.FormatAnthropic:
		if keys&keyUsage != 0 {
			o.absorbAnthropic(raw)
		}
	case channel.FormatGemini:
		if keys&keyUsageMetadata != 0 {
			o.absorbGemini(raw)
		}
	}
	o.absorbDetails(raw, keys)
}

// OpenAI-compatible usage is cumulative, but each field can arrive separately.
// Pointers distinguish omitted/null from explicit zero; aliases never add up.
type tokenDetails struct {
	CachedTokens             *int64          `json:"cached_tokens"`
	CacheWriteTokens         *int64          `json:"cache_write_tokens"`
	CacheCreationTokens      json.RawMessage `json:"cache_creation_tokens"`
	CacheCreationInputTokens json.RawMessage `json:"cache_creation_input_tokens"`
}
type responsesUsage struct {
	InputTokens        *int64          `json:"input_tokens"`
	OutputTokens       *int64          `json:"output_tokens"`
	InputTokensDetails *tokenDetails   `json:"input_tokens_details"`
	CacheRead          json.RawMessage `json:"cache_read_input_tokens"`
	CacheWrite         json.RawMessage `json:"cache_creation_input_tokens"`
	CacheHit           json.RawMessage `json:"prompt_cache_hit_tokens"`
}
type openAIUsageState struct{ input, output, read, write int64 }

func (o *Observer) aliasRaw(raw []byte) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	n, ok := count(raw)
	if !ok {
		o.note("兼容用量字段无效，使用可靠字段")
		return nil
	}
	return &n
}

func (o *Observer) alias(values ...*int64) *int64 {
	var selected *int64
	for _, v := range values {
		if v == nil {
			continue
		}
		if *v < 0 {
			o.note("负用量无效，保留此前可靠字段")
			continue
		}
		if selected == nil {
			selected = v
		} else if *selected != *v {
			o.note("同义用量冲突，使用优先字段")
		}
	}
	return selected
}
func (o *Observer) setResponsesUsage(u *responsesUsage) {
	if u == nil {
		return
	}
	read, write := o.aliasRaw(u.CacheRead), o.aliasRaw(u.CacheWrite)
	if d := u.InputTokensDetails; d != nil {
		read = o.alias(d.CachedTokens, read)
		write = o.alias(d.CacheWriteTokens, o.aliasRaw(d.CacheCreationTokens), o.aliasRaw(d.CacheCreationInputTokens), write)
	}
	// DeepSeek is only a fallback when standard details are absent.
	if u.InputTokensDetails == nil {
		read = o.alias(read, o.aliasRaw(u.CacheHit))
	}
	for _, item := range []struct {
		value *int64
		dest  *int64
	}{
		{u.InputTokens, &o.openai.input}, {u.OutputTokens, &o.openai.output}, {read, &o.openai.read}, {write, &o.openai.write},
	} {
		if v := o.alias(item.value); v != nil {
			*item.dest = *v
		}
	}
	readCount, writeCount := o.openai.read, o.openai.write
	if readCount > o.openai.input || writeCount > o.openai.input-readCount {
		// Inconsistent cache facts cannot define mutually exclusive buckets.
		o.note("缓存用量超过输入总量，使用通用输入价")
		readCount, writeCount = 0, 0
	}
	o.usage = ledger.Usage{InputTokens: o.openai.input - readCount - writeCount, OutputTokens: o.openai.output, CacheReadTokens: readCount, CacheWriteTokens: writeCount}
	o.found = true
}
func (o *Observer) absorbChat(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	var value struct {
		Usage *struct {
			PromptTokens        *int64          `json:"prompt_tokens"`
			CompletionTokens    *int64          `json:"completion_tokens"`
			PromptTokensDetails *tokenDetails   `json:"prompt_tokens_details"`
			CacheRead           json.RawMessage `json:"cache_read_input_tokens"`
			CacheWrite          json.RawMessage `json:"cache_creation_input_tokens"`
			CacheHit            json.RawMessage `json:"prompt_cache_hit_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Usage == nil {
		return
	}
	u := value.Usage
	o.setResponsesUsage(&responsesUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, InputTokensDetails: u.PromptTokensDetails, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, CacheHit: u.CacheHit})
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
	InputTokens          *int64            `json:"input_tokens"`
	OutputTokens         *int64            `json:"output_tokens"`
	CacheCreation        *int64            `json:"cache_creation_input_tokens"`
	CacheReadInputs      *int64            `json:"cache_read_input_tokens"`
	Iterations           []anthropicFields `json:"iterations"`
	CacheCreationDetails *struct {
		Five *int64 `json:"ephemeral_5m_input_tokens"`
		Hour *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (o *Observer) applyAnthropic(fields *anthropicFields) {
	if fields == nil {
		return
	}
	// Anthropic compaction usage replaces (rather than adds to) top-level usage:
	// https://platform.claude.com/docs/en/build-with-claude/compaction-threshold#understanding-usage
	if len(fields.Iterations) > 0 {
		var totals anthropicUsage
		five, hour := int64(0), int64(0)
		ttlValid := true
		valid := true
		for _, iteration := range fields.Iterations {
			for _, pair := range []struct {
				v    *int64
				dest *int64
			}{{iteration.InputTokens, &totals.input}, {iteration.OutputTokens, &totals.output}, {iteration.CacheCreation, &totals.cacheWrite}, {iteration.CacheReadInputs, &totals.cacheRead}} {
				if pair.v == nil {
					continue
				}
				if *pair.v < 0 || *pair.v > int64(^uint64(0)>>1)-*pair.dest {
					valid = false
					break
				}
				*pair.dest += *pair.v
			}
			if iteration.CacheCreationDetails == nil && iteration.CacheCreation != nil && *iteration.CacheCreation > 0 {
				o.note("迭代缓存TTL未细分，余量使用通用缓存写价")
			}
			if iteration.CacheCreationDetails != nil {
				d := iteration.CacheCreationDetails
				if d.Five == nil || d.Hour == nil || *d.Five < 0 || *d.Hour < 0 || iteration.CacheCreation == nil || *d.Five > *iteration.CacheCreation || *d.Hour > *iteration.CacheCreation-*d.Five {
					ttlValid = false
				} else {
					five += *d.Five
					hour += *d.Hour
				}
			}
		}
		if valid {
			o.anthropic = totals
			o.iterationsSeen = true
			o.iterationMask = 0
			for index, pair := range []struct {
				top    *int64
				total  int64
				offset *int64
			}{
				{fields.InputTokens, totals.input, &o.iterationOffset.input}, {fields.OutputTokens, totals.output, &o.iterationOffset.output}, {fields.CacheCreation, totals.cacheWrite, &o.iterationOffset.cacheWrite}, {fields.CacheReadInputs, totals.cacheRead, &o.iterationOffset.cacheRead},
			} {
				if pair.top != nil && *pair.top >= 0 && *pair.top <= pair.total {
					*pair.offset = pair.total - *pair.top
					o.iterationMask |= 1 << index
				}
			}

			o.usage = ledger.Usage{InputTokens: totals.input, OutputTokens: totals.output, CacheWriteTokens: totals.cacheWrite, CacheReadTokens: totals.cacheRead}
			o.found = true
			o.setBucket("cache_write_", nil, totals.cacheWrite)
			if ttlValid && (five > 0 || hour > 0) {
				o.setBucket("cache_write_", map[string]int64{"cache_write_5m": five, "cache_write_1h": hour}, totals.cacheWrite)
			} else if !ttlValid {
				o.note("迭代缓存TTL无效，使用通用缓存写价")
			}
			return
		}
		o.note("迭代用量无效，使用顶层可靠用量")
	}
	if o.iterationsSeen {
		// An earlier cumulative iterations list can precede later top-level deltas.
		// Keep its extra iterations, replacing only the known top-level component.
		for index, pair := range []struct {
			top    *int64
			offset int64
			total  *int64
		}{
			{fields.InputTokens, o.iterationOffset.input, &o.anthropic.input}, {fields.OutputTokens, o.iterationOffset.output, &o.anthropic.output}, {fields.CacheCreation, o.iterationOffset.cacheWrite, &o.anthropic.cacheWrite}, {fields.CacheReadInputs, o.iterationOffset.cacheRead, &o.anthropic.cacheRead},
		} {
			if pair.top == nil {
				continue
			}
			if o.iterationMask&(1<<index) == 0 {
				o.note("迭代后部分用量缺少顶层基准，保留迭代事实")
				continue
			}
			if total, ok := sumTokens(pair.offset, *pair.top); ok {
				*pair.total = total
			} else {
				o.note("迭代后用量无效，保留迭代事实")
			}
		}
		o.usage = ledger.Usage{InputTokens: o.anthropic.input, OutputTokens: o.anthropic.output, CacheWriteTokens: o.anthropic.cacheWrite, CacheReadTokens: o.anthropic.cacheRead}
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
	// Read numeric facts in place: Gemini can repeat usage on every token.
	// Keeping this in Observer scratch avoids a larger decoded struct per frame.
	if !json.Valid(raw) {
		return
	}
	o.members = o.members[:0]
	root := o.objectOf(raw)
	fields := o.objectOf(root.get("usageMetadata"))
	if fields == nil {
		return
	}
	var usage struct{ PromptTokenCount, CandidatesTokenCount, ThoughtsTokenCount, TotalTokenCount, ToolUsePromptTokenCount, CachedContentTokenCount int64 }
	for _, field := range []struct {
		name string
		dest *int64
	}{
		{"promptTokenCount", &usage.PromptTokenCount}, {"candidatesTokenCount", &usage.CandidatesTokenCount}, {"thoughtsTokenCount", &usage.ThoughtsTokenCount}, {"totalTokenCount", &usage.TotalTokenCount}, {"toolUsePromptTokenCount", &usage.ToolUsePromptTokenCount}, {"cachedContentTokenCount", &usage.CachedContentTokenCount},
	} {
		v := fields.get(field.name)
		if len(v) == 0 || string(v) == "null" {
			continue
		}
		n, ok := count(v)
		if !ok {
			return
		}
		*field.dest = n
	}
	output, ok := sumTokens(usage.CandidatesTokenCount, usage.ThoughtsTokenCount)
	if !ok {
		output = clamp(usage.CandidatesTokenCount)
		o.note("思考或输出用量无效，使用可靠候选输出")
	}
	// Native total = prompt + tool prompt + candidates + thoughts:
	// https://ai.google.dev/api/generate-content#UsageMetadata
	// Only a complete, overflow-safe total equation proves inclusive thoughts.
	if usage.ThoughtsTokenCount > 0 && usage.TotalTokenCount > 0 {
		base, valid := sumTokens(usage.PromptTokenCount, usage.CandidatesTokenCount, usage.ToolUsePromptTokenCount)
		exclusive, complete := sumTokens(base, usage.ThoughtsTokenCount)
		if valid && usage.TotalTokenCount == base {
			output = clamp(usage.CandidatesTokenCount)
		} else if !valid || !complete || usage.TotalTokenCount != exclusive {
			o.note("思考总量关系不明确，沿用独立思考字段")
		}
	}
	prompt, cached := clamp(usage.PromptTokenCount), usage.CachedContentTokenCount
	if cached < 0 || cached > prompt {
		cached = 0
		o.note("缓存用量超过输入总量或为负，使用通用输入价")
	}
	o.usage = ledger.Usage{InputTokens: prompt - cached, OutputTokens: output, CacheReadTokens: cached}
	o.found = true
}

// sumTokens rejects malformed counts and overflow before arithmetic.
func sumTokens(values ...int64) (int64, bool) {
	var total int64
	for _, n := range values {
		if n < 0 || n > int64(^uint64(0)>>1)-total {
			return 0, false
		}
		total += n
	}
	return total, true
}
