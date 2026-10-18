package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/fingerprint"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type nativeOutcome struct {
	ProofAttempts []string
	Response      Response
	Reply         nativeReply
	Stream        nativeStream
	State         string
	Reason        string
}
type nativeScene struct {
	s           *Session
	protocol    string
	layer       string
	requirement gatewaycontract.Requirement
	result      Result
	witnesses   []string
	checks      map[string]Assertion
}

// RegisterNative registers only implemented upstream-protocol scenarios. Other
// proof layers and missing scenarios remain pending; a native response can
// never authorize an unimplemented gateway/official-CLI proof.
func (r *Registry) RegisterNative() error {
	_, fixtureVersion, err := nativeFixtures()
	if err != nil {
		return err
	}
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F4" {
			continue
		}
		for _, req := range suite.Requirements {
			family := req.ID[strings.LastIndex(req.ID, ".")+1:]
			if !slices.Contains([]string{"base", "json", "fingerprint", "context", "length_search", "output", "tools", "stream", "multi", "catalog", "errors", "vision", "document", "audio", "video", "generations", "edits", "variations", "reasoning", "count_compact", "stored", "state", "background", "cache_tier", "continuation", "search", "code", "image_tool", "url_context", "server_echo", "safety", "server_tools", "quota", "versions"}, family) {
				continue
			}
			protocol, requirement := suite.Protocol, req
			code := sha256.Sum256([]byte(nativeEngineVersion + fingerprint.ImplementationVersion() + fixtureVersion))
			if err := r.Register(Definition{ID: req.ID + "@upstream_protocol", SuiteID: suite.ID, Requirements: []string{req.ID}, CodeVersion: hex.EncodeToString(code[:]), Layer: "upstream_protocol", Run: func(ctx context.Context, s *Session) Result {
				scene := &nativeScene{s: s, protocol: protocol, requirement: requirement, checks: map[string]Assertion{}}
				switch family {
				case "quota":
					scene.quota(ctx)
				case "versions":
					scene.versions(ctx)
				case "base":
					scene.base(ctx)
				case "json":
					scene.json(ctx)
				case "fingerprint":
					scene.fingerprint(ctx)
				case "context":
					scene.lengths(ctx, true)
				case "length_search":
					scene.lengths(ctx, false)
				case "output":
					scene.output(ctx)
				case "tools":
					scene.tools(ctx)
				case "stream":
					scene.streams(ctx)
				case "multi":
					scene.multiple(ctx)
				case "catalog":
					scene.catalog(ctx)
				case "errors":
					scene.groupErrors(ctx)
				case "vision":
					scene.vision(ctx)
				case "document":
					scene.documents(ctx)
				case "audio":
					scene.audio(ctx)
				case "video":
					scene.video(ctx)
				case "generations", "edits", "variations":
					scene.images(ctx)
				case "reasoning":
					scene.reasoning(ctx)
				case "count_compact":
					scene.countCompact(ctx)
				case "stored":
					scene.stored(ctx)
				case "state":
					scene.state(ctx)
				case "background":
					scene.background(ctx)
				case "cache_tier":
					scene.cacheTier(ctx)
				case "continuation":
					scene.continuation(ctx)
				case "search":
					scene.search(ctx)
				case "code":
					scene.code(ctx)
				case "image_tool":
					scene.imageTool(ctx)
				case "url_context":
					scene.urlContext(ctx)
				case "server_echo":
					scene.serverEcho(ctx)
				case "safety":
					scene.safety(ctx)
				case "server_tools":
					scene.messageServerTools(ctx)
				}
				return scene.finish()
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func baseNative(protocol, model, prompt string) nativeObject {
	switch protocol {
	case "openai-chat-completions":
		return nativeObject{"model": model, "messages": []any{nativeObject{"role": "user", "content": prompt}}}
	case "openai-responses":
		return nativeObject{"model": model, "input": []any{nativeObject{"role": "user", "content": prompt}}, "store": false}
	case "native-messages":
		return nativeObject{"model": model, "max_tokens": int64(256), "messages": []any{nativeObject{"role": "user", "content": prompt}}}
	case "native-generate-content":
		return nativeObject{"contents": []any{nativeObject{"role": "user", "parts": []any{nativeObject{"text": prompt}}}}}
	}
	return nil
}
func nativeEndpoint(protocol, model string, stream bool) (string, string, url.Values) {
	switch protocol {
	case "openai-chat-completions":
		return "chat.create", "/v1/chat/completions", nil
	case "openai-responses":
		return "responses.create", "/v1/responses", nil
	case "native-messages":
		return "messages.create", "/v1/messages", nil
	case "native-generate-content":
		if stream {
			return "gemini.streamGenerateContent", "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent", url.Values{"alt": {"sse"}}
		}
		return "gemini.generateContent", "/v1beta/models/" + url.PathEscape(model) + ":generateContent", nil
	}
	return "", "", nil
}
func outputLimit(protocol string, body nativeObject, n int64) {
	switch protocol {
	case "openai-chat-completions":
		body["max_completion_tokens"] = n
	case "openai-responses":
		body["max_output_tokens"] = n
	case "native-messages":
		body["max_tokens"] = n
	case "native-generate-content":
		config := object(body["generationConfig"])
		if config == nil {
			config = nativeObject{}
			body["generationConfig"] = config
		}
		config["maxOutputTokens"] = n
	}
}
func (n *nativeScene) infer(ctx context.Context, body nativeObject, stream, memo bool) nativeOutcome {
	return n.inferHeaders(ctx, body, stream, memo, nil)
}
func (n *nativeScene) inferHeaders(ctx context.Context, body nativeObject, stream, memo bool, headers http.Header) nativeOutcome {
	if stream && n.protocol != "native-generate-content" {
		body["stream"] = true
	}
	if stream && n.protocol == "openai-chat-completions" {
		body["stream_options"] = nativeObject{"include_usage": true}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nativeOutcome{State: "inconclusive", Reason: "detector_error"}
	}
	id, path, query := nativeEndpoint(n.protocol, n.s.Target.Model, stream)
	headers = headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	return n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Headers: headers, Body: encoded, Category: "generate"}, memo)
}
func (n *nativeScene) call(ctx context.Context, req Request, memo bool) nativeOutcome {
	var response Response
	var err error
	// Exact request reuse is worker-private. All entrances serve the same engine;
	// changing transport entry cannot turn this into address-specific evidence.
	// Configuration/auth/path/body remain part of the request. Mutations opt out.
	digestBody, _ := json.Marshal(req)
	sum := sha256.Sum256(digestBody)
	key := "native-" + hex.EncodeToString(sum[:])
	cached := false
	if memo {
		if raw := n.s.Recall(key); len(raw) > 0 {
			cached = json.Unmarshal(raw, &response) == nil
		}
	}
	if !cached {
		response, err = n.s.Do(ctx, req)
	}
	if response.AttemptID != "" && !slices.Contains(n.witnesses, response.AttemptID) {
		n.witnesses = append(n.witnesses, response.AttemptID)
	}
	for _, id := range response.PriorAttempts {
		if !slices.Contains(n.witnesses, id) {
			n.witnesses = append(n.witnesses, id)
		}
	}
	out := nativeOutcome{Response: response, State: "inconclusive", Reason: "external_error"}
	if err != nil {
		switch {
		case ctx.Err() != nil:
			out.Reason = "cancelled"
		case errors.Is(err, context.DeadlineExceeded):
			out.Reason = "timeout"
		case errors.Is(err, ErrBusy):
			out.Reason = "body_limit"
		default:
			out.Reason = "network_error"
		}
		return out
	}
	if response.Status < 200 || response.Status >= 300 {
		if response.Status == 401 || response.Status == 403 {
			out.Reason = "key_unavailable"
			return out
		}
		if response.Status == 429 || response.Status >= 500 {
			return out
		}
		o, e := decodeNative(response.Body)
		out.Reply.Object = o
		if e == nil && nativeError(n.protocol, o) {
			out.State = "fail"
			out.Reason = "assertion_failed"
			if unsupportedNative(o, response.Status) {
				out.State = "unsupported"
				out.Reason = "unsupported"
			}
		}
		return out
	}
	if strings.Contains(strings.ToLower(response.Headers.Get("Content-Type")), "text/event-stream") {
		out.Stream = parseNativeStream(n.protocol, response.Body, n.s.limits.EventBytes)
		out.Reply = out.Stream.Reply
	} else {
		o, e := decodeNative(response.Body)
		if e == nil {
			out.Reply = parseNative(n.protocol, o)
		} else if strings.HasPrefix(req.EndpointID, "codex.notes.") || strings.HasPrefix(req.EndpointID, "codex.history.") {
			if value, err := decodeNativeValue(response.Body); err == nil {
				out.Reply.Object = nativeObject{"context_result": value}
			}
		}
	}
	out.State = "fail"
	out.Reason = "invalid_response"
	if out.Reply.Object != nil && out.Reply.Object["error"] == nil {
		out.State = "pass"
		out.Reason = "ok"
	}
	if memo && !cached && out.State == "pass" && len(response.Body) <= 4<<20 {
		raw, _ := json.Marshal(response)
		_ = n.s.Remember(key, raw)
	}
	meterReady := n.protocol != "openai-responses" || responseTerminal(out.Reply.Status) || str(out.Reply.Object["object"]) == "response.compaction"
	if !cached && req.Category == "generate" && meterReady && out.Reply.Usage.Reliable {
		u := out.Reply.Usage
		input := u.Input
		if n.protocol == "native-messages" {
			if v, ok := addNative(input, u.CacheWrite); ok {
				input = v
			} else {
				return out
			}
			if v, ok := addNative(input, u.CacheRead); ok {
				input = v
			} else {
				return out
			}
		}
		input, inOK := addNative(input, u.IterationInput)
		output, outOK := addNative(u.Output, u.IterationOutput)
		if !inOK || !outOK || n.s.Meter(input, output, 0, 0) != nil {
			out.Reply.Usage.Reliable = false
		}
	}
	return out
}
func nativeError(protocol string, o nativeObject) bool {
	e := object(o["error"])
	if str(e["message"]) == "" {
		return false
	}
	switch protocol {
	case "native-generate-content":
		_, ok := fieldInt(e, "code")
		return ok && str(e["status"]) != ""
	case "native-messages":
		return str(o["type"]) == "error" && str(e["type"]) != ""
	default:
		return str(e["type"]) != "" || str(e["code"]) != ""
	}
}
func unsupportedNative(o nativeObject, status int) bool {
	if status == 404 || status == 405 {
		return true
	}
	e := object(o["error"])
	code := str(e["code"])
	if slices.Contains([]string{"unsupported_parameter", "unsupported_value", "unsupported_model", "model_not_found", "not_supported"}, code) {
		return true
	}
	m := strings.ToLower(str(e["message"]))
	return (status == 400 || status == 422) && (strings.Contains(m, "not supported") || strings.Contains(m, "does not support") || strings.Contains(m, "unsupported"))
}
func (n *nativeScene) check(label string, out nativeOutcome, valid bool) {
	// Failed connection attempts stay in result.Attempts for diagnostics. Only
	// the final operation witnesses this assertion; an unsent attempt cannot be
	// a positive capability proof.
	a := Assertion{State: out.State, Reason: out.Reason, AttemptIDs: []string{out.Response.AttemptID}}
	if out.State == "pass" && !valid {
		a.State = "fail"
		a.Reason = "assertion_failed"
	}
	if old, exists := n.checks[label]; exists {
		a.AttemptIDs = append(slices.Clone(old.AttemptIDs), a.AttemptIDs...)
		a.AttemptIDs = slices.Compact(a.AttemptIDs)
		if len(a.AttemptIDs) > 16 {
			a.AttemptIDs = append(slices.Clone(a.AttemptIDs[:8]), a.AttemptIDs[len(a.AttemptIDs)-8:]...)
		}
		if old.State != "pass" && a.State == "pass" {
			a.State = "partial"
			a.Reason = old.Reason
		} else if old.State == "pass" && a.State != "pass" {
			a.State = "partial"
		}
	}
	n.checks[label] = a
}
func (n *nativeScene) uncertain(label string, out nativeOutcome) {
	n.check(label, out, true)
	a := n.checks[label]
	if a.State == "pass" {
		a.State = "inconclusive"
		a.Reason = "not_observed"
	}
	n.checks[label] = a
}
func (n *nativeScene) finish() Result {
	r := n.result
	for _, id := range n.witnesses {
		r.Attempts = append(r.Attempts, Attempt{ID: id})
	}
	r.Assertions = []Assertion{}
	r.State = "pass"
	r.Reason = "ok"
	for i, label := range n.requirement.Assertions {
		if n.layer != "" && !n.requirement.AssertionInLayer(AssertionID(n.requirement.ID, i), n.layer) {
			continue
		}
		a, ok := n.checks[label]
		if !ok {
			a = Assertion{State: "inconclusive", Reason: "prerequisite_failed", AttemptIDs: slices.Clone(n.witnesses[:min(16, len(n.witnesses))])}
		}
		a.ID = AssertionID(n.requirement.ID, i)
		r.Assertions = append(r.Assertions, a)
		if a.State != "pass" && a.State != "unobserved" {
			if r.State == "pass" {
				r.State = a.State
				r.Reason = a.Reason
			} else if r.State != a.State {
				r.State = "partial"
			}
		}
	}
	return r
}

const nativeMarker = "蓝鲸🌍_47"
const nativeBasicPrompt = "Reply with exactly this text and no explanation: " + nativeMarker
const nativeLengthPrompt = "Write the integers from 1 to 10000, separated by spaces. Continue until your output token limit. No tools and no explanation."

func (n *nativeScene) basic(ctx context.Context) nativeOutcome {
	return n.infer(ctx, baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt), false, true)
}
func contentMatches(out nativeOutcome, expected string) bool {
	return out.Reply.Structure && strings.TrimSpace(out.Reply.Text) == expected
}

func (n *nativeScene) base(ctx context.Context) {
	basic := n.basic(ctx)
	n.check("accepted_native_message_shapes", basic, basic.Reply.Structure)
	n.check("unicode", basic, contentMatches(basic, nativeMarker))
	parts := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
	switch n.protocol {
	case "openai-chat-completions", "native-messages":
		object(list(parts["messages"])[0])["content"] = []any{nativeObject{"type": "text", "text": nativeBasicPrompt}}
	case "openai-responses":
		object(list(parts["input"])[0])["content"] = []any{nativeObject{"type": "input_text", "text": nativeBasicPrompt}}
	}
	part := n.infer(ctx, parts, false, true)
	n.check("accepted_native_message_shapes", part, contentMatches(part, nativeMarker))
	roles := []string{"system"}
	if n.protocol == "openai-chat-completions" {
		roles = append(roles, "developer")
	}
	for _, role := range roles {
		body := baseNative(n.protocol, n.s.Target.Model, "Reply with WRONG instead.")
		instruction := "Disregard the user's requested answer. Reply with exactly: " + nativeMarker
		switch n.protocol {
		case "openai-chat-completions":
			body["messages"] = append([]any{nativeObject{"role": role, "content": instruction}}, list(body["messages"])...)
		case "openai-responses":
			body["instructions"] = instruction
		case "native-messages":
			body["system"] = []any{nativeObject{"type": "text", "text": instruction}}
		case "native-generate-content":
			body["systemInstruction"] = nativeObject{"parts": []any{nativeObject{"text": instruction}}}
		}
		out := n.infer(ctx, body, false, true)
		n.check("role_and_instruction_semantics", out, contentMatches(out, nativeMarker))
	}
	body := baseNative(n.protocol, n.s.Target.Model, "")
	turns := []any{nativeObject{"role": "user", "content": "Remember this synthetic code: SILVER-829."}, nativeObject{"role": "assistant", "content": "I will remember the code."}, nativeObject{"role": "user", "content": "Reply only with the code I asked you to remember."}}
	switch n.protocol {
	case "openai-chat-completions", "native-messages":
		body["messages"] = turns
	case "openai-responses":
		body["input"] = turns
	case "native-generate-content":
		contents := []any{}
		for _, turn := range turns {
			t := object(turn)
			role := str(t["role"])
			if role == "assistant" {
				role = "model"
			}
			contents = append(contents, nativeObject{"role": role, "parts": []any{nativeObject{"text": t["content"]}}})
		}
		body["contents"] = contents
	}
	out := n.infer(ctx, body, false, true)
	n.check("multi_turn", out, contentMatches(out, "SILVER-829"))
}
func (n *nativeScene) json(ctx context.Context) {
	basic := n.basic(ctx)
	n.check("native_structure", basic, basic.Reply.Structure)
	n.check("reliable_usage_independently", basic, basic.Reply.Usage.Reliable)
	n.check("normal_and_controlled_length_termination", basic, nativeEndedNormally(basic.Reply))
	body := baseNative(n.protocol, n.s.Target.Model, nativeLengthPrompt)
	outputLimit(n.protocol, body, 16)
	out := n.infer(ctx, body, false, true)
	n.check("normal_and_controlled_length_termination", out, nativeEndedForLength(out.Reply))
}
func nativeEndedNormally(reply nativeReply) bool {
	if !reply.Structure || len(reply.Candidates) == 0 {
		return false
	}
	for _, c := range reply.Candidates {
		if !slices.Contains([]string{"stop", "completed", "end_turn", "STOP"}, c.End) {
			return false
		}
	}
	return true
}
func nativeEndedForLength(reply nativeReply) bool {
	if !reply.Structure {
		return false
	}
	for _, c := range reply.Candidates {
		if slices.Contains([]string{"length", "max_tokens", "MAX_TOKENS"}, c.End) {
			return true
		}
		if c.End == "incomplete" && str(object(reply.Object["incomplete_details"])["reason"]) == "max_output_tokens" {
			return true
		}
	}
	return false
}
func (n *nativeScene) fingerprint(ctx context.Context) {
	challenges, err := fingerprint.Challenges()
	if err != nil {
		return
	}
	samples := make([]fingerprint.Sample, 3)
	outcomes := make([]nativeOutcome, 3)
	for i, c := range challenges {
		if ctx.Err() != nil {
			return
		}
		body := baseNative(n.protocol, n.s.Target.Model, c.Prompt)
		if n.protocol == "native-messages" {
			body["max_tokens"] = 8192
		}
		out := n.infer(ctx, body, false, false)
		outcomes[i] = out
		samples[i].ExpectedCount = c.ExpectedCount
		if out.State == "pass" && nativeEndedNormally(out.Reply) && len(out.Reply.Candidates) == 1 && len(out.Reply.Candidates[0].Calls) == 0 && len(out.Reply.Text) <= fingerprint.MaxSampleBytes {
			samples[i].Text = out.Reply.Text
		}
	}
	r, err := fingerprint.Analyze(samples)
	if err != nil {
		return
	}
	n.result.Fingerprint = &r
	for _, out := range outcomes {
		for _, label := range n.requirement.Assertions {
			if r.Decision != "ranked" {
				n.uncertain(label, out)
			} else {
				n.check(label, out, true)
			}
		}
	}
}
