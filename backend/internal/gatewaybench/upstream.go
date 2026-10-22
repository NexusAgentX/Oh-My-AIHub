package gatewaybench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// UsageMode says whether and how a fake upstream reports token usage.
type UsageMode int

const (
	// UsageProtocol reports usage the way the real API of the format does:
	// always in a non-streamed body; in a stream, OpenAI Chat only to a client
	// that asked for it with stream_options.include_usage (the gateway asks on
	// every client's behalf), the other formats always.
	UsageProtocol UsageMode = iota
	// UsageOff reports no usage anywhere: the gateway cannot bill the call.
	UsageOff
	// UsageAlways also sends the OpenAI Chat stream's usage-only frame to a
	// client that did not ask for it.
	UsageAlways
)

// Profile is how a fake upstream behaves. It can be swapped while the upstream
// runs (Upstream.SetProfile); requests already under way keep the profile they
// started with.
type Profile struct {
	// TTFT is the time from the request to the first byte of the answer. For a
	// non-streamed answer that is the time to the whole body.
	TTFT time.Duration
	// Interval is the time between two token frames of a stream; zero sends them
	// back to back.
	Interval time.Duration
	// Tokens is the number of output tokens, one frame each in a stream.
	Tokens int
	// InputTokens is the prompt size reported in the usage.
	InputTokens int
	Usage       UsageMode
}

// DefaultProfile is a small, quick answer.
func DefaultProfile() Profile {
	return Profile{TTFT: 20 * time.Millisecond, Interval: 0, Tokens: 64, InputTokens: 50}
}

func (p Profile) normalized() Profile {
	if p.Tokens < 1 {
		p.Tokens = 1
	}
	if p.InputTokens < 0 {
		p.InputTokens = 0
	}
	return p
}

// wordToken is the text of one output token.
const wordToken = "word "

// idPlaceholder stands for the response ID of an OpenAI Responses answer in a
// rendered frame; ServeHTTP swaps in a fresh ID per request.
const idPlaceholder = "@ID@"

// answer is one rendered reply: the frames of a stream, or the single body of
// a non-streamed reply. Frames in [tokenFrom, tokenTo) are the paced token
// frames; the ones before them leave with the first token, the ones after it
// right behind the last.
type answer struct {
	contentType        string
	frames             [][]byte
	withID             []int
	tokenFrom, tokenTo int
}

// variant is everything that decides which bytes an upstream answers with.
type variant struct {
	format  channel.Format
	stream  bool
	include bool // the client asked for usage in its stream (OpenAI Chat)
}

type plan struct {
	profile  Profile
	variants sync.Map // variant -> *answer
}

func (p *plan) answer(v variant) *answer {
	if cached, ok := p.variants.Load(v); ok {
		return cached.(*answer)
	}
	rendered := render(p.profile, v)
	actual, _ := p.variants.LoadOrStore(v, rendered)
	return actual.(*answer)
}

// UpstreamStats are what an Upstream has seen.
type UpstreamStats struct {
	Requests int64
	Streams  int64
}

// Upstream is a fake model API for all four formats. It answers by path:
// /v1/chat/completions, /v1/responses, /v1/messages and the two Gemini
// generateContent methods. The answer's shape and timing come from its Profile.
type Upstream struct {
	plan     atomic.Pointer[plan]
	sequence atomic.Int64
	requests atomic.Int64
	streams  atomic.Int64
}

// NewUpstream returns an upstream that behaves as profile says.
func NewUpstream(profile Profile) *Upstream {
	u := &Upstream{}
	u.SetProfile(profile)
	return u
}

// SetProfile changes how the upstream answers from the next request on.
func (u *Upstream) SetProfile(profile Profile) {
	u.plan.Store(&plan{profile: profile.normalized()})
}

// Stats returns the counters of the upstream.
func (u *Upstream) Stats() UpstreamStats {
	return UpstreamStats{Requests: u.requests.Load(), Streams: u.streams.Load()}
}

// route tells the format of a path; geminiStream is true for the streaming
// Gemini method.
func route(path string) (format channel.Format, geminiStream, ok bool) {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return channel.FormatOpenAIChat, false, true
	case strings.HasSuffix(path, "/responses"):
		return channel.FormatOpenAIResponses, false, true
	case strings.HasSuffix(path, "/messages"):
		return channel.FormatAnthropic, false, true
	case strings.HasSuffix(path, ":streamGenerateContent"):
		return channel.FormatGemini, true, true
	case strings.HasSuffix(path, ":generateContent"):
		return channel.FormatGemini, false, true
	}
	return "", false, false
}

// requestFacts is what a fake upstream reads of a request body.
type requestFacts struct {
	Stream        bool `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func (u *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	format, geminiStream, ok := route(r.URL.Path)
	if !ok || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var facts requestFacts
	_ = json.Unmarshal(body, &facts)
	u.requests.Add(1)

	stream := facts.Stream || geminiStream
	include := facts.StreamOptions != nil && facts.StreamOptions.IncludeUsage
	current := u.plan.Load()
	a := current.answer(variant{format: format, stream: stream, include: include && format == channel.FormatOpenAIChat})

	id := ""
	if format == channel.FormatOpenAIResponses {
		id = "resp_" + strconv.FormatInt(u.sequence.Add(1), 10)
	}
	if stream {
		u.streams.Add(1)
		serveStream(w, r, current.profile, a, id)
		return
	}
	serveBody(w, r, current.profile, a, id)
}

func withID(frame []byte, id string) []byte {
	if id == "" {
		return frame
	}
	return bytes.ReplaceAll(frame, []byte(idPlaceholder), []byte(id))
}

func serveBody(w http.ResponseWriter, r *http.Request, p Profile, a *answer, id string) {
	timer := time.NewTimer(p.TTFT)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	body := a.frames[0]
	if len(a.withID) > 0 {
		body = withID(body, id)
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func serveStream(w http.ResponseWriter, r *http.Request, p Profile, a *answer, id string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "no-cache")
	start := time.Now()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	// wait returns false when the client has gone before the time came.
	wait := func(until time.Time) bool {
		d := time.Until(until)
		if d <= 0 {
			return r.Context().Err() == nil
		}
		timer.Reset(d)
		select {
		case <-timer.C:
			return true
		case <-r.Context().Done():
			return false
		}
	}
	idFrames := map[int]struct{}{}
	for _, index := range a.withID {
		idFrames[index] = struct{}{}
	}
	for index, frame := range a.frames {
		switch {
		case index == 0:
			if !wait(start.Add(p.TTFT)) {
				return
			}
		case index >= a.tokenFrom && index < a.tokenTo:
			if !wait(start.Add(p.TTFT + time.Duration(index-a.tokenFrom)*p.Interval)) {
				return
			}
		}
		if _, hasID := idFrames[index]; hasID {
			frame = withID(frame, id)
		}
		if _, err := w.Write(frame); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// ---- rendering ----

func render(p Profile, v variant) *answer {
	switch v.format {
	case channel.FormatOpenAIChat:
		return renderChat(p, v.stream, v.include)
	case channel.FormatOpenAIResponses:
		return renderResponses(p, v.stream)
	case channel.FormatAnthropic:
		return renderAnthropic(p, v.stream)
	default:
		return renderGemini(p, v.stream)
	}
}

func words(n int) string { return strings.Repeat(wordToken, n) }

func (p Profile) total() int { return p.InputTokens + p.Tokens }

// sseBuilder collects the frames of a stream.
type sseBuilder struct{ a *answer }

func newSSE() *sseBuilder { return &sseBuilder{a: &answer{contentType: "text/event-stream"}} }

func (b *sseBuilder) raw(frame []byte) {
	if bytes.Contains(frame, []byte(idPlaceholder)) {
		b.a.withID = append(b.a.withID, len(b.a.frames))
	}
	b.a.frames = append(b.a.frames, frame)
}

func (b *sseBuilder) data(payload string) { b.raw([]byte("data: " + payload + "\n\n")) }

func (b *sseBuilder) event(name, payload string) {
	b.raw([]byte("event: " + name + "\ndata: " + payload + "\n\n"))
}

// tokens appends n identical token frames: the paced part of the stream.
func (b *sseBuilder) tokens(n int, frame []byte) {
	b.a.tokenFrom = len(b.a.frames)
	for range n {
		b.a.frames = append(b.a.frames, frame)
	}
	b.a.tokenTo = len(b.a.frames)
}

func body(contentType, payload string) *answer {
	rendered := &answer{contentType: contentType, frames: [][]byte{[]byte(payload)}}
	if strings.Contains(payload, idPlaceholder) {
		rendered.withID = []int{0}
	}
	return rendered
}

func renderChat(p Profile, stream, include bool) *answer {
	usage := fmt.Sprintf(`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`,
		p.InputTokens, p.Tokens, p.total())
	if !stream {
		payload := `{"id":"chatcmpl-perf","object":"chat.completion","created":1700000000,"model":"perf","choices":[{"index":0,"message":{"role":"assistant","content":"` +
			words(p.Tokens) + `"},"finish_reason":"stop"}]`
		if p.Usage != UsageOff {
			payload += `,"usage":` + usage
		}
		return body("application/json", payload+"}")
	}
	const head = `{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"perf","choices":`
	nullUsage := ""
	if include && p.Usage != UsageOff {
		// A client that asked for usage gets "usage":null on every chunk before it.
		nullUsage = `,"usage":null`
	}
	b := newSSE()
	b.data(head + `[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]` + nullUsage + `}`)
	b.tokens(p.Tokens, []byte("data: "+head+`[{"index":0,"delta":{"content":"`+wordToken+`"},"finish_reason":null}]`+nullUsage+"}\n\n"))
	b.data(head + `[{"index":0,"delta":{},"finish_reason":"stop"}]` + nullUsage + `}`)
	if p.Usage == UsageAlways || (p.Usage == UsageProtocol && include) {
		b.data(head + `[],"usage":` + usage + `}`)
	}
	b.raw([]byte("data: [DONE]\n\n"))
	return b.a
}

func renderResponses(p Profile, stream bool) *answer {
	usage := fmt.Sprintf(`,"usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":0},"output_tokens":%d,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":%d}`,
		p.InputTokens, p.Tokens, p.total())
	if p.Usage == UsageOff {
		usage = ""
	}
	message := `{"id":"msg_perf","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"` + words(p.Tokens) + `","annotations":[]}]}`
	response := func(status, output, tail string) string {
		return `{"id":"` + idPlaceholder + `","object":"response","created_at":1700000000,"status":"` + status + `","model":"perf","output":[` + output + `]` + tail + `}`
	}
	if !stream {
		return body("application/json", response("completed", message, usage))
	}
	b := newSSE()
	sequence := 0
	event := func(kind, rest string) {
		b.event(kind, `{"type":"`+kind+`","sequence_number":`+strconv.Itoa(sequence)+`,`+rest+`}`)
		sequence++
	}
	event("response.created", `"response":`+response("in_progress", "", ""))
	event("response.in_progress", `"response":`+response("in_progress", "", ""))
	event("response.output_item.added", `"output_index":0,"item":{"id":"msg_perf","type":"message","status":"in_progress","role":"assistant","content":[]}`)
	event("response.content_part.added", `"item_id":"msg_perf","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}`)
	b.tokens(p.Tokens, []byte("event: response.output_text.delta\ndata: "+
		`{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_perf","output_index":0,"content_index":0,"delta":"`+wordToken+`"}`+"\n\n"))
	sequence += p.Tokens
	event("response.output_text.done", `"item_id":"msg_perf","output_index":0,"content_index":0,"text":"`+words(p.Tokens)+`"`)
	event("response.content_part.done", `"item_id":"msg_perf","output_index":0,"content_index":0,"part":{"type":"output_text","text":"`+words(p.Tokens)+`","annotations":[]}`)
	event("response.output_item.done", `"output_index":0,"item":`+message)
	event("response.completed", `"response":`+response("completed", message, usage))
	return b.a
}

func renderAnthropic(p Profile, stream bool) *answer {
	usage := fmt.Sprintf(`,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}`, p.InputTokens, p.Tokens)
	startUsage := fmt.Sprintf(`,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}`, p.InputTokens)
	deltaUsage := fmt.Sprintf(`,"usage":{"output_tokens":%d}`, p.Tokens)
	if p.Usage == UsageOff {
		usage, startUsage, deltaUsage = "", "", ""
	}
	if !stream {
		return body("application/json", `{"id":"msg_perf","type":"message","role":"assistant","model":"perf","content":[{"type":"text","text":"`+words(p.Tokens)+
			`"}],"stop_reason":"end_turn","stop_sequence":null`+usage+`}`)
	}
	b := newSSE()
	b.event("message_start", `{"type":"message_start","message":{"id":"msg_perf","type":"message","role":"assistant","model":"perf","content":[],"stop_reason":null,"stop_sequence":null`+startUsage+`}}`)
	b.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	b.event("ping", `{"type":"ping"}`)
	b.tokens(p.Tokens, []byte("event: content_block_delta\ndata: "+`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+wordToken+`"}}`+"\n\n"))
	b.event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	b.event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null}`+deltaUsage+`}`)
	b.event("message_stop", `{"type":"message_stop"}`)
	return b.a
}

func renderGemini(p Profile, stream bool) *answer {
	metadata := func(candidates int) string {
		if p.Usage == UsageOff {
			return ""
		}
		return fmt.Sprintf(`,"usageMetadata":{"promptTokenCount":%d,"candidatesTokenCount":%d,"totalTokenCount":%d}`, p.InputTokens, candidates, p.InputTokens+candidates)
	}
	if !stream {
		return body("application/json", `{"candidates":[{"content":{"parts":[{"text":"`+words(p.Tokens)+`"}],"role":"model"},"finishReason":"STOP","index":0}]`+
			metadata(p.Tokens)+`,"modelVersion":"perf"}`)
	}
	// Gemini repeats the running usage in every chunk, so every frame differs.
	b := newSSE()
	b.a.tokenFrom = 0
	for index := 1; index <= p.Tokens; index++ {
		finish := ""
		if index == p.Tokens {
			finish = `,"finishReason":"STOP"`
		}
		b.raw([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"` + wordToken + `"}],"role":"model"}` + finish + `,"index":0}]` + metadata(index) + `,"modelVersion":"perf"}` + "\r\n\r\n"))
	}
	b.a.tokenTo = len(b.a.frames)
	return b.a
}
