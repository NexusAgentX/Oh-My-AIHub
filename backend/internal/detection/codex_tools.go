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
