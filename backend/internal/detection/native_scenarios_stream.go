package detection

import "context"

func nativeReasoningConfig(protocol string, body nativeObject) {
	switch protocol {
	case "openai-responses":
		body["reasoning"] = nativeObject{"effort": "low", "summary": "auto"}
		body["include"] = []any{"reasoning.encrypted_content"}
	case "native-messages":
		body["thinking"] = nativeObject{"type": "adaptive"}
		body["output_config"] = nativeObject{"effort": "low"}
		body["max_tokens"] = 2048
	case "native-generate-content":
		body["generationConfig"] = nativeObject{"thinkingConfig": nativeObject{"includeThoughts": true, "thinkingLevel": "low"}}
	}
}
func (n *nativeScene) streams(ctx context.Context) {
	text := n.infer(ctx, baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt), true, true)
	n.check("text_lifecycle", text, text.Stream.Valid && text.Stream.TextLifecycle && contentMatches(text, nativeMarker))
	n.check("indexes_utf8_and_terminal_usage", text, text.Stream.Valid && text.Reply.Usage.Reliable)
	n.check("no_induced_network_failure", text, true)
	body := toolBody(n.protocol, n.s.Target.Model, "required", false)
	tools := n.infer(ctx, body, true, true)
	n.check("tool_lifecycle_when_applicable", tools, tools.Stream.Valid && tools.Stream.ToolLifecycle && validCalls(tools, true))
	if tools.Stream.Valid && validCalls(tools, true) {
		n.roundTrip(ctx, "tool_lifecycle_when_applicable", body, tools)
	}
	if n.protocol != "openai-chat-completions" {
		body := baseNative(n.protocol, n.s.Target.Model, "Calculate 37 times 43 and verify the result before answering.")
		nativeReasoningConfig(n.protocol, body)
		out := n.infer(ctx, body, true, true)
		if out.Stream.Valid && out.Reply.Structure && !out.Stream.ReasoningLifecycle {
			n.unobserved("reasoning_lifecycle_when_applicable", out)
		} else {
			n.check("reasoning_lifecycle_when_applicable", out, out.Stream.Valid && out.Stream.ReasoningLifecycle)
		}
	}
}
func (n *nativeScene) multiple(ctx context.Context) {
	for _, stream := range []bool{false, true} {
		body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
		if n.protocol == "openai-chat-completions" {
			body["n"] = 2
		} else {
			body["generationConfig"] = nativeObject{"candidateCount": 2}
		}
		out := n.infer(ctx, body, stream, true)
		valid := out.Reply.Structure && len(out.Reply.Candidates) == 2
		for _, candidate := range out.Reply.Candidates {
			valid = valid && candidate.Text == nativeMarker && candidate.End != ""
		}
		if stream {
			valid = valid && out.Stream.Valid
		}
		n.check("native_multiple_candidates", out, valid)
		n.check("independent_indexes_and_termination", out, valid)
		if out.Reply.Usage.Reliable {
			n.check("no_cross_candidate_usage_duplication", out, valid)
		} else {
			n.uncertain("no_cross_candidate_usage_duplication", out)
		}
	}
}
