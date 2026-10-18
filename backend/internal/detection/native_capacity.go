package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func (n *nativeScene) tokenCount(ctx context.Context, body nativeObject) (*int64, nativeOutcome) {
	id, path, key := "", "", "input_tokens"
	payload := nativeObject{}
	switch n.protocol {
	case "openai-responses":
		id, path = "responses.count", "/v1/responses/input_tokens"
		for _, k := range []string{"model", "input", "instructions", "tools", "tool_choice", "reasoning", "text"} {
			if body[k] != nil {
				payload[k] = body[k]
			}
		}
	case "native-messages":
		id, path = "messages.count", "/v1/messages/count_tokens"
		for _, k := range []string{"model", "messages", "system", "thinking", "tools", "tool_choice"} {
			if body[k] != nil {
				payload[k] = body[k]
			}
		}
	case "native-generate-content":
		id, path, key = "gemini.countTokens", "/v1beta/models/"+url.PathEscape(n.s.Target.Model)+":countTokens", "totalTokens"
		request := nativeObject{"model": "models/" + strings.TrimPrefix(n.s.Target.Model, "models/")}
		for _, k := range []string{"contents", "systemInstruction", "tools", "toolConfig", "generationConfig", "cachedContent"} {
			if body[k] != nil {
				request[k] = body[k]
			}
		}
		payload["generateContentRequest"] = request
	default:
		return nil, nativeOutcome{}
	}
	encoded, _ := json.Marshal(payload)
	out := n.call(ctx, Request{EndpointID: id, Path: path, Body: encoded, Headers: http.Header{"Content-Type": {"application/json"}}, Category: "count"}, true)
	if out.State == "pass" {
		if tokens, valid := fieldInt(out.Reply.Object, key); valid {
			return &tokens, out
		}
	}
	return nil, out
}

func nativeLengthError(protocol, kind string, out nativeOutcome) bool {
	if out.Response.Status != 400 && out.Response.Status != 422 {
		return false
	}
	o, err := decodeNative(out.Response.Body)
	if err != nil || !nativeError(protocol, o) {
		return false
	}
	e := object(o["error"])
	code := str(e["code"])
	m := strings.ToLower(str(e["message"]))
	if kind == "context" {
		return code == "context_length_exceeded" || strings.Contains(m, "prompt is too long") || strings.Contains(m, "maximum context length") || strings.Contains(m, "input token count") && strings.Contains(m, "exceeds")
	}
	return slices.Contains([]string{"max_tokens_exceeded", "max_output_tokens_exceeded", "max_tokens_too_large"}, code) || strings.Contains(m, "max_tokens") && strings.Contains(m, "greater than") || strings.Contains(m, "maxoutputtokens") && (strings.Contains(m, "exceed") || strings.Contains(m, "out of range")) || strings.Contains(m, "max_output_tokens") && strings.Contains(m, "maximum")
}
func (n *nativeScene) capacityProbe(ctx context.Context, kind string, point int64) CapacityProbe {
	p := CapacityProbe{Reason: "external_error", AttemptIDs: []string{}}
	var body nativeObject
	var counted *int64
	if kind == "context" {
		// The padding target is an estimate until native count/response usage is
		// observed. The report records this separately from exact actual tokens.
		if point > (n.s.limits.RequestBytes-4096)/4 {
			id, path, query := nativeEndpoint(n.protocol, n.s.Target.Model, false)
			out := n.call(ctx, Request{EndpointID: id, Path: path, Query: query, Category: "generate", Oversized: true}, false)
			p.Reason = out.Reason
			p.AttemptIDs = append(p.AttemptIDs, out.Response.AttemptID)
			p.RequestedApproximate = true
			return p
		}
		prompt := "The first synthetic marker is HEAD-VIOLET-137. Ignore the following padding.\n" + strings.Repeat(" pad", int(point)) + "\nThe last synthetic marker is TAIL-COPPER-926. Reply only with the first and last marker, separated by a space."
		body = baseNative(n.protocol, n.s.Target.Model, prompt)
		outputLimit(n.protocol, body, 64)
		p.RequestedApproximate = true
		var countOutcome nativeOutcome
		counted, countOutcome = n.tokenCount(ctx, body)
		if countOutcome.Response.AttemptID != "" {
			p.AttemptIDs = append(p.AttemptIDs, countOutcome.Response.AttemptID)
		}
		if counted != nil && *counted >= point {
			overhead := *counted - point
			p.InputOverhead = &overhead
		}
	} else {
		body = baseNative(n.protocol, n.s.Target.Model, "Write the integers 1, 2, 3, 4 and continue upward, separated by spaces, until the output token limit. No tools, headings, explanations or refusal. Keep generating as many integers as possible.")
		outputLimit(n.protocol, body, point)
	}
	out := n.infer(ctx, body, false, false)
	p.Reason = out.Reason
	p.AttemptIDs = append(p.AttemptIDs, out.Response.AttemptID)
	p.Accepted = out.State == "pass" && out.Reply.Structure
	p.LengthError = nativeLengthError(n.protocol, kind, out)
	if p.LengthError {
		p.Reason = "unsupported"
		if kind == "max_output" {
			v := point
			p.FailureUpper = &v
		} else if counted != nil {
			if upper, valid := addNative(*counted, 64); valid {
				p.FailureUpper = &upper
				// A preflight count may differ from generation accounting. Retain
				// the estimate, but do not promote it to a proven token upper.
				p.Approximate = true
			}
		}
	}
	if out.Reply.Usage.Reliable {
		u := out.Reply.Usage
		if kind == "context" {
			input := u.Input
			if n.protocol == "native-messages" {
				input, _ = addNative(input, u.CacheRead)
				input, _ = addNative(input, u.CacheWrite)
			}
			actual, valid := addNative(input, u.Output)
			if valid {
				p.Actual = &actual
			}
			p.Semantic = contentMatches(out, "HEAD-VIOLET-137 TAIL-COPPER-926") && nativeEndedNormally(out.Reply)
		} else {
			actual := u.Output
			p.Actual = &actual
			numbers := outputTaskNumbers(out.Reply.Text)
			sequential := len(numbers) > 0 && strings.HasPrefix(strings.TrimSpace(out.Reply.Text), "1")
			for i, v := range numbers {
				sequential = sequential && v == i+1
			}
			p.Semantic = sequential && out.Reply.Structure && len(numbers) >= max(1, min(4, int(min(point, 16)/4))) && (nativeEndedNormally(out.Reply) || nativeEndedForLength(out.Reply))
		}
	}
	return p
}
func outputTaskNumbers(text string) []int {
	// This is a synthetic output-task semantic check, not fingerprint scoring.
	out := make([]int, 0, 32)
	for i := 0; i < len(text) && len(out) < 32; {
		if text[i] < '0' || text[i] > '9' {
			i++
			continue
		}
		start := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		v, err := strconv.Atoi(text[start:i])
		if err == nil {
			out = append(out, v)
		}
	}
	return out
}
func (n *nativeScene) capacity(ctx context.Context, kind string) CapacityReport {
	settings, declared := n.s.Parameters.Context, n.s.Target.ContextWindow
	if kind == "max_output" {
		settings, declared = n.s.Parameters.MaxOutput, n.s.Target.MaxOutputTokens
	}
	input, _ := json.Marshal(struct {
		Kind, Protocol, Model string
		Settings              gatewaycontract.SearchSettings
		Declared              *int64
	}{kind, n.protocol, n.s.Target.Model, settings, declared})
	sum := sha256.Sum256(input)
	key := "capacity-" + hex.EncodeToString(sum[:])
	if raw := n.s.Recall(key); len(raw) > 0 {
		var r CapacityReport
		if json.Unmarshal(raw, &r) == nil {
			return r
		}
	}
	r, err := SearchCapacity(ctx, settings, kind, "tokens", declared, func(ctx context.Context, v int64) CapacityProbe { return n.capacityProbe(ctx, kind, v) })
	if err == nil {
		encoded, _ := json.Marshal(r)
		_ = n.s.Remember(key, encoded)
	}
	return r
}
func (n *nativeScene) lengths(ctx context.Context, onlyContext bool) {
	kinds := []string{"context", "max_output"}
	if onlyContext {
		kinds = kinds[:1]
	}
	for _, kind := range kinds {
		if ctx.Err() != nil {
			return
		}
		r := n.capacity(ctx, kind)
		n.result.Capacity = append(n.result.Capacity, r)
		for _, step := range r.Steps {
			for _, id := range step.AttemptIDs {
				if !slices.Contains(n.witnesses, id) {
					n.witnesses = append(n.witnesses, id)
				}
			}
		}
		if len(r.Steps) == 0 {
			continue
		}
		last := r.Steps[len(r.Steps)-1]
		out := nativeOutcome{State: "pass", Reason: "ok", Response: Response{AttemptID: last.AttemptIDs[len(last.AttemptIDs)-1]}}
		if last.Reason != "ok" && !last.LengthError {
			out.State = "inconclusive"
			out.Reason = last.Reason
		}
		for _, label := range n.requirement.Assertions {
			switch label {
			case "parameter_acceptance_separate":
				if r.ParameterAcceptedLower == nil && r.ParameterRejectedUpper == nil {
					n.uncertain(label, out)
				} else {
					n.check(label, out, true)
				}
			case "configured_bisection_or_trisection":
				n.check(label, out, r.Completed)
			case "external_error_not_length_upper":
				n.check(label, out, true)
			case "measured_lower_and_proven_failure_upper":
				if r.VerifiedLower == nil || r.Stop == "non_monotonic" || r.Stop == "unconfirmed" {
					n.uncertain(label, out)
				} else {
					n.check(label, out, true)
				}
			case "maximum_output_actual_generation_separate_from_parameter_acceptance":
				if kind == "context" {
					continue
				}
				if r.VerifiedLower == nil {
					n.uncertain(label, out)
				} else {
					n.check(label, out, true)
				}
			}
		}
	}
}
