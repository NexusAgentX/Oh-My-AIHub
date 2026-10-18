package detection

import "context"

func codexToolBody(model string, lite bool) nativeObject {
	body := codexBase(model)
	body["input"] = []any{nativeObject{"role": "user", "content": nativeToolPrompt}}
	declaration := nativeTool("openai-responses", false)
	if lite {
		body["input"] = append([]any{nativeObject{"type": "additional_tools", "role": "developer", "tools": []any{nativeObject{"type": "namespace", "name": "functions", "description": "Synthetic tools.", "tools": []any{declaration}}}}}, list(body["input"])...)
	} else {
		body["tools"] = []any{declaration}
	}
	return body
}

func codexToolCalls(out nativeOutcome, lite bool) bool {
	if !validCalls(out, true) {
		return false
	}
	expected := ""
	if lite {
		expected = "functions"
	}
	for _, call := range out.Reply.Candidates[0].Calls {
		if call.Namespace != expected {
			return false
		}
	}
	return true
}

func (n *nativeScene) codexToolRoundTrip(ctx context.Context, body nativeObject, first nativeOutcome, lite bool) nativeOutcome {
	if first.State != "pass" || !codexToolCalls(first, lite) {
		return first
	}
	next := n.toolReplayBody(body, first.Reply)
	if next == nil {
		return first
	}
	return n.codexInfer(ctx, next, "")
}

func (n *nativeScene) codexTools(ctx context.Context) {
	id := n.requirement.ID
	liteBody := codexToolBody(n.s.Target.Model, true)
	var lite, liteReply, standard, standardReply nativeOutcome
	if id != "codex-special.RL03" && id != "codex-special.RL12" {
		lite = n.codexInfer(ctx, liteBody, "")
		if id == "codex-special.RL02" {
			liteReply = n.codexToolRoundTrip(ctx, liteBody, lite, true)
		}
	} else {
		standardBody := codexToolBody(n.s.Target.Model, false)
		standard = n.codexInfer(ctx, standardBody, "")
		standardReply = n.codexToolRoundTrip(ctx, standardBody, standard, false)
		lite = n.codexInfer(ctx, liteBody, "")
		liteReply = n.codexToolRoundTrip(ctx, liteBody, lite, true)
		n.linkedCheck("standard_tools_roundtrip", standard, standardReply, codexToolCalls(standard, false) && contentMatches(standardReply, nativeToolResult) && nativeEndedNormally(standardReply.Reply))
		n.linkedCheck("lite_tools_roundtrip", lite, liteReply, codexToolCalls(lite, true) && contentMatches(liteReply, nativeToolResult) && nativeEndedNormally(liteReply.Reply))
		n.checks["standard_native_roundtrip"] = n.checks["standard_tools_roundtrip"]
		n.checks["lite_native_roundtrip"] = n.checks["lite_tools_roundtrip"]
		if id == "codex-special.RL12" {
			n.codexCrossReplay(ctx, standardBody, standard, liteBody, lite)
		}
	}
	switch id {
	case "codex-special.RL01":
		for _, label := range []string{"actual_protocol_marker", "native_lite_request", "marker_preserved"} {
			n.check(label, lite, codexToolCalls(lite, true))
		}
	case "codex-special.RL02":
		n.check("additional_tools_declaration", lite, codexToolCalls(lite, true))
		n.check("tool_call_association", lite, codexToolCalls(lite, true))
		n.linkedCheck("tool_result_continuation", lite, liteReply, codexToolCalls(lite, true) && contentMatches(liteReply, nativeToolResult) && nativeEndedNormally(liteReply.Reply))
	case "codex-special.RL08":
		textBody := cloneNative(liteBody)
		textBody["input"] = []any{list(liteBody["input"])[0], nativeObject{"role": "user", "content": nativeBasicPrompt}}
		textBody["tool_choice"] = "none"
		text := n.codexInfer(ctx, textBody, "")
		n.check("sse_lifecycle", lite, lite.Stream.Valid && lite.Stream.Terminal)
		n.check("sse_lifecycle", text, text.Stream.Valid && text.Stream.Terminal)
		n.check("text_utf8_deltas", text, text.Stream.Valid && text.Stream.TextLifecycle && codexMarker(text))
		n.check("tool_argument_deltas", lite, lite.Stream.Valid && lite.Stream.ToolLifecycle && codexToolCalls(lite, true))
		n.check("terminal_usage", lite, lite.Stream.Valid && lite.Stream.Terminal && lite.Reply.Usage.Reliable)
		n.check("terminal_usage", text, text.Stream.Valid && text.Stream.Terminal && text.Reply.Usage.Reliable)
	}
}

func (n *nativeScene) codexCrossReplay(ctx context.Context, standardBody nativeObject, standard nativeOutcome, liteBody nativeObject, lite nativeOutcome) {
	for _, transition := range []struct {
		body  nativeObject
		first nativeOutcome
		lite  bool
	}{{liteBody, standard, false}, {standardBody, lite, true}} {
		if !codexToolCalls(transition.first, transition.lite) {
			n.uncertain("compatible_replay", transition.first)
			continue
		}
		body := cloneNative(transition.body)
		if transition.lite {
			// Preserve the produced namespace in a native standard declaration.
			body["tools"] = []any{nativeObject{"type": "namespace", "name": "functions", "description": "Synthetic tools.", "tools": []any{nativeTool(n.protocol, false)}}}
		}
		next := n.toolReplayBody(body, transition.first.Reply)
		out := n.codexInfer(ctx, next, "")
		n.linkedCheck("compatible_replay", transition.first, out, contentMatches(out, nativeToolResult) && nativeEndedNormally(out.Reply))
	}
}

func (n *nativeScene) codexSequentialTools(ctx context.Context) {
	body := codexToolBody(n.s.Target.Model, false)
	body["parallel_tool_calls"] = false
	body["input"] = []any{nativeObject{"role": "user", "content": `Invoke synthetic_echo exactly twice with label="violet", meta={"ok":true}, items=[{"value":7}]. Both invocations are required. After both results, reply with exactly their result text. Do not invent results.`}}
	first := n.codexInfer(ctx, body, "")
	n.check("parallel_parameter_accepted", first, first.Reply.Structure)
	current := first
	seen := map[string]bool{}
	for turn := 0; turn < 3; turn++ {
		if !validCalls(current, true) || len(current.Reply.Candidates[0].Calls) != 1 {
			n.check("parallel_calls_disabled", current, false)
			return
		}
		id := current.Reply.Candidates[0].Calls[0].ID
		if seen[id] {
			n.check("parallel_calls_disabled", current, false)
			return
		}
		seen[id] = true
		n.check("parallel_calls_disabled", current, true)
		body = n.toolReplayBody(body, current.Reply)
		if body == nil {
			n.uncertain("parallel_calls_disabled", current)
			return
		}
		// This scenario needs a second actual invocation; do not disable all
		// tools after the first result as ordinary one-call probes do.
		if len(seen) == 1 {
			delete(body, "tool_choice")
		}
		out := n.codexInfer(ctx, body, "")
		if len(seen) == 2 {
			n.linkedCheck("parallel_calls_disabled", first, out, contentMatches(out, nativeToolResult) && nativeEndedNormally(out.Reply))
			return
		}
		current = out
	}
}

func (n *nativeScene) codexToolExtensions(ctx context.Context) {
	for _, custom := range []bool{false, true} {
		body := codexToolBody(n.s.Target.Model, false)
		label := "namespace_tools_roundtrip"
		if custom {
			label = "custom_tools_roundtrip"
			body["input"] = []any{nativeObject{"role": "user", "content": "Call synthetic_raw with exactly the plaintext input synthetic-input. After receiving its result, reply with exactly its text."}}
			body["tools"] = []any{nativeObject{"type": "custom", "name": "synthetic_raw", "description": "Synthetic plaintext echo.", "format": nativeObject{"type": "text"}}}
		} else {
			body["tools"] = []any{nativeObject{"type": "namespace", "name": "synthetic", "description": "Synthetic tools.", "tools": []any{nativeTool(n.protocol, false)}}}
		}
		first := n.codexInfer(ctx, body, "")
		valid := first.Reply.Structure && len(first.Reply.Candidates) == 1 && len(first.Reply.Candidates[0].Calls) == 1
		if valid {
			call := first.Reply.Candidates[0].Calls[0]
			if custom {
				valid = call.ID != "" && call.Name == "synthetic_raw" && call.Custom == "synthetic-input"
			} else {
				valid = validCalls(first, true) && call.Namespace == "synthetic"
			}
		}
		n.check("argument_fragments", first, valid && first.Stream.Valid && first.Stream.ToolLifecycle)
		if !valid {
			n.check(label, first, false)
			n.check("call_result_association", first, false)
			continue
		}
		next := n.toolReplayBody(body, first.Reply)
		if next == nil {
			n.uncertain(label, first)
			n.uncertain("call_result_association", first)
			continue
		}
		out := n.codexInfer(ctx, next, "")
		passed := contentMatches(out, nativeToolResult) && nativeEndedNormally(out.Reply)
		n.linkedCheck(label, first, out, passed)
		n.linkedCheck("call_result_association", first, out, passed)
	}
}
