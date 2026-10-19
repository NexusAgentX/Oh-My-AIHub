package detection

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

const nativeToolPrompt = `Call synthetic_echo with label="violet", meta={"ok":true}, items=[{"value":7}]. After receiving a tool result, reply with exactly its result text and do not call another tool.`
const nativeToolResult = "TOOL-RESULT-6309"

func nativeTool(protocol string, strict bool) nativeObject {
	function := nativeObject{"name": "synthetic_echo", "description": "Returns a synthetic opaque result for the test. Never accesses external resources.", "parameters": nativeNestedSchema()}
	switch protocol {
	case "openai-chat-completions":
		if strict {
			function["strict"] = true
		}
		return nativeObject{"type": "function", "function": function}
	case "openai-responses":
		function["type"] = "function"
		if strict {
			function["strict"] = true
		}
		return function
	case "native-messages":
		delete(function, "parameters")
		function["input_schema"] = nativeNestedSchema()
		if strict {
			function["strict"] = true
		}
		return function
	case "native-generate-content":
		delete(function, "parameters")
		function["parametersJsonSchema"] = nativeNestedSchema()
		return nativeObject{"functionDeclarations": []any{function}}
	}
	return nil
}
func toolChoice(protocol string, body nativeObject, mode string) {
	switch protocol {
	case "openai-chat-completions":
		if mode == "named" {
			body["tool_choice"] = nativeObject{"type": "function", "function": nativeObject{"name": "synthetic_echo"}}
		} else {
			body["tool_choice"] = mode
		}
	case "openai-responses":
		if mode == "named" {
			body["tool_choice"] = nativeObject{"type": "function", "name": "synthetic_echo"}
		} else {
			body["tool_choice"] = mode
		}
	case "native-messages":
		choice := nativeObject{"type": mode}
		if mode == "required" {
			choice["type"] = "any"
		}
		if mode == "named" {
			choice["type"] = "tool"
			choice["name"] = "synthetic_echo"
		}
		body["tool_choice"] = choice
	case "native-generate-content":
		value := strings.ToUpper(mode)
		if mode == "required" || mode == "named" {
			value = "ANY"
		}
		choice := nativeObject{"mode": value}
		if mode == "named" {
			choice["allowedFunctionNames"] = []any{"synthetic_echo"}
		}
		body["toolConfig"] = nativeObject{"functionCallingConfig": choice}
	}
}
func toolBody(protocol, model, mode string, strict bool) nativeObject {
	body := baseNative(protocol, model, nativeToolPrompt)
	body["tools"] = []any{nativeTool(protocol, strict)}
	toolChoice(protocol, body, mode)
	return body
}
func validCalls(out nativeOutcome, required bool) bool {
	if !out.Reply.Structure || len(out.Reply.Candidates) != 1 {
		return false
	}
	calls := out.Reply.Candidates[0].Calls
	if required && len(calls) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, call := range calls {
		if call.Name != "synthetic_echo" || !matchesNativeSchema(call.Arguments) || (call.ID != "" && seen[call.ID]) {
			return false
		}
		if call.ID != "" {
			seen[call.ID] = true
		}
	}
	return true
}
func cloneNative(body nativeObject) nativeObject {
	raw, _ := json.Marshal(body)
	copy, _ := decodeNative(raw)
	return copy
}
func (n *nativeScene) toolReplayBody(body nativeObject, reply nativeReply) nativeObject {
	next := cloneNative(body)
	toolChoice(n.protocol, next, "none")
	if len(reply.Candidates) != 1 {
		return nil
	}
	candidate := reply.Candidates[0]
	switch n.protocol {
	case "openai-chat-completions":
		messages := list(next["messages"])
		if len(candidate.Parts) != 1 {
			return nil
		}
		messages = append(messages, candidate.Parts[0])
		for _, call := range candidate.Calls {
			messages = append(messages, nativeObject{"role": "tool", "tool_call_id": call.ID, "content": nativeToolResult})
		}
		next["messages"] = messages
	case "openai-responses":
		input := append(list(next["input"]), candidate.Parts...)
		for _, call := range candidate.Calls {
			kind := "function_call_output"
			if call.Custom != "" {
				kind = "custom_tool_call_output"
			}
			input = append(input, nativeObject{"type": kind, "call_id": call.ID, "output": nativeToolResult})
		}
		next["input"] = input
	case "native-messages":
		messages := append(list(next["messages"]), nativeObject{"role": "assistant", "content": candidate.Parts})
		results := []any{}
		for _, call := range candidate.Calls {
			results = append(results, nativeObject{"type": "tool_result", "tool_use_id": call.ID, "content": nativeToolResult})
		}
		messages = append(messages, nativeObject{"role": "user", "content": results})
		next["messages"] = messages
	case "native-generate-content":
		contents := append(list(next["contents"]), nativeObject{"role": "model", "parts": candidate.Parts})
		results := []any{}
		for _, call := range candidate.Calls {
			response := nativeObject{"name": call.Name, "response": nativeObject{"result": nativeToolResult}}
			if call.ID != "" {
				response["id"] = call.ID
			}
			results = append(results, nativeObject{"functionResponse": response})
		}
		contents = append(contents, nativeObject{"role": "user", "parts": results})
		next["contents"] = contents
	}
	return next
}
func (n *nativeScene) linkedCheck(label string, first, out nativeOutcome, valid bool) {
	n.check(label, out, valid)
	a := n.checks[label]
	if first.Response.AttemptID != "" && !slices.Contains(a.AttemptIDs, first.Response.AttemptID) {
		a.AttemptIDs = append([]string{first.Response.AttemptID}, a.AttemptIDs...)
	}
	n.checks[label] = a
}
func (n *nativeScene) roundTrip(ctx context.Context, label string, body nativeObject, first nativeOutcome) nativeOutcome {
	if body == nil || first.State != "pass" || len(first.Reply.Candidates) != 1 || len(first.Reply.Candidates[0].Calls) == 0 {
		n.uncertain(label, first)
		return first
	}
	next := n.toolReplayBody(body, first.Reply)
	if next == nil {
		n.uncertain(label, first)
		return first
	}
	out := n.infer(ctx, next, false, true)
	n.linkedCheck(label, first, out, contentMatches(out, nativeToolResult) && nativeEndedNormally(out.Reply))
	return out
}
func (n *nativeScene) tools(ctx context.Context) {
	var named nativeOutcome
	var firstCall nativeOutcome
	var firstBody nativeObject
	for _, mode := range []string{"auto", "none", "required", "named"} {
		body := toolBody(n.protocol, n.s.Target.Model, mode, false)
		out := n.infer(ctx, body, false, true)
		if mode != "none" && firstBody == nil && out.State == "pass" && validCalls(out, true) {
			firstCall, firstBody = out, body
		}
		if mode == "auto" {
			n.check("native_declarations", out, out.Reply.Structure)
			n.check("auto", out, validCalls(out, false))
			continue
		}
		if mode == "none" {
			n.check("none", out, out.Reply.Structure && len(out.Reply.Candidates) == 1 && len(out.Reply.Candidates[0].Calls) == 0)
			continue
		}
		n.check(map[string]string{"named": "named_tool", "required": "required"}[mode], out, validCalls(out, true))
		if mode == "named" {
			named = out
		}
	}
	// Any observed valid native call can prove result association. Named tool
	// selection is independent and must not block an auto/required round trip.
	if firstBody == nil {
		firstCall = named
	}
	resumed := n.roundTrip(ctx, "call_result_association", firstBody, firstCall)
	strictBody := toolBody(n.protocol, n.s.Target.Model, "required", true)
	strictLabel := "strict_schema"
	if n.protocol == "native-generate-content" {
		strictLabel = "VALIDATED"
		toolChoice(n.protocol, strictBody, "validated")
	}
	strict := n.infer(ctx, strictBody, false, true)
	if strict.State == "pass" && len(strict.Reply.Candidates) == 1 && len(strict.Reply.Candidates[0].Calls) == 0 {
		n.uncertain(strictLabel, strict)
	} else {
		n.check(strictLabel, strict, validCalls(strict, true))
	}
	if n.protocol == "native-generate-content" {
		n.check("allowedFunctionNames", named, validCalls(named, true))
		observed := false
		for _, c := range firstCall.Reply.Candidates {
			for _, call := range c.Calls {
				observed = observed || call.ID != ""
			}
		}
		if observed {
			n.linkedCheck("function_call_id_echo_when_present", firstCall, resumed, validCalls(firstCall, true) && contentMatches(resumed, nativeToolResult))
		} else {
			n.unobserved("function_call_id_echo_when_present", firstCall)
		}
	} else {
		for _, enabled := range []bool{false, true} {
			body := toolBody(n.protocol, n.s.Target.Model, "required", false)
			label := "parallel_tool_calls_false"
			if enabled {
				label = "parallel_tool_calls_true"
			}
			if n.protocol == "native-messages" {
				object(body["tool_choice"])["disable_parallel_tool_use"] = !enabled
			} else {
				body["parallel_tool_calls"] = enabled
			}
			out := n.infer(ctx, body, false, true)
			valid := validCalls(out, true)
			if !enabled && len(out.Reply.Candidates) == 1 {
				valid = valid && len(out.Reply.Candidates[0].Calls) <= 1
			}
			n.check(label, out, valid)
		}
	}
	// Request a finite multi-call sample, but lack of multiple calls cannot
	// establish that parallel calling is unsupported.
	multiBody := toolBody(n.protocol, n.s.Target.Model, "required", false)
	prompt := nativeToolPrompt + " Before answering, call synthetic_echo twice independently with those same arguments."
	switch n.protocol {
	case "openai-chat-completions", "native-messages":
		object(list(multiBody["messages"])[0])["content"] = prompt
	case "openai-responses":
		object(list(multiBody["input"])[0])["content"] = prompt
	case "native-generate-content":
		object(list(object(list(multiBody["contents"])[0])["parts"])[0])["text"] = prompt
	}
	multi := n.infer(ctx, multiBody, false, true)
	if multi.State == "pass" && multi.Reply.Structure && len(multi.Reply.Candidates) == 1 && len(multi.Reply.Candidates[0].Calls) > 0 && !validCalls(multi, true) {
		n.check("multiple_calls_and_results_when_observed", multi, false)
	} else if multi.State == "pass" && len(multi.Reply.Candidates) == 1 && len(multi.Reply.Candidates[0].Calls) > 1 && validCalls(multi, true) {
		n.roundTrip(ctx, "multiple_calls_and_results_when_observed", multiBody, multi)
	} else {
		n.unobserved("multiple_calls_and_results_when_observed", multi)
	}
	if n.protocol == "openai-chat-completions" || n.protocol == "openai-responses" {
		n.openAIToolExtensions(ctx)
	}
}
func (n *nativeScene) unobserved(label string, out nativeOutcome) {
	if out.State == "pass" && !out.Reply.Structure {
		n.check(label, out, false)
		return
	}
	n.uncertain(label, out)
	a := n.checks[label]
	if out.State == "pass" {
		for i, l := range n.requirement.Assertions {
			if l == label && slices.Contains(n.requirement.ConditionalAssertions, AssertionID(n.requirement.ID, i)) {
				a.State = "unobserved"
				a.Reason = "not_observed"
			}
		}
	}
	n.checks[label] = a
}
func (n *nativeScene) openAIToolExtensions(ctx context.Context) {
	allowed := toolBody(n.protocol, n.s.Target.Model, "required", false)
	other := cloneNative(nativeTool(n.protocol, false))
	if n.protocol == "openai-chat-completions" {
		object(other["function"])["name"] = "synthetic_other"
	} else {
		other["name"] = "synthetic_other"
	}
	allowed["tools"] = append(list(allowed["tools"]), other)
	if n.protocol == "openai-chat-completions" {
		allowed["tool_choice"] = nativeObject{"type": "allowed_tools", "allowed_tools": nativeObject{"mode": "required", "tools": []any{nativeObject{"type": "function", "function": nativeObject{"name": "synthetic_echo"}}}}}
	} else {
		allowed["tool_choice"] = nativeObject{"type": "allowed_tools", "mode": "required", "tools": []any{nativeObject{"type": "function", "name": "synthetic_echo"}}}
	}
	out := n.infer(ctx, allowed, false, true)
	n.check("allowed_tools", out, validCalls(out, true))
	body := baseNative(n.protocol, n.s.Target.Model, "Call synthetic_raw with exactly the plaintext input synthetic-input. After receiving a tool result, reply with exactly its text.")
	custom := nativeObject{"name": "synthetic_raw", "description": "Synthetic plaintext echo tool.", "format": nativeObject{"type": "text"}}
	if n.protocol == "openai-chat-completions" {
		body["tools"] = []any{nativeObject{"type": "custom", "custom": custom}}
		body["tool_choice"] = nativeObject{"type": "custom", "custom": nativeObject{"name": "synthetic_raw"}}
	} else {
		custom["type"] = "custom"
		body["tools"] = []any{custom}
		body["tool_choice"] = nativeObject{"type": "custom", "name": "synthetic_raw"}
	}
	first := n.infer(ctx, body, false, true)
	valid := first.Reply.Structure && len(first.Reply.Candidates) == 1 && len(first.Reply.Candidates[0].Calls) == 1
	if valid {
		call := first.Reply.Candidates[0].Calls[0]
		valid = call.ID != "" && call.Name == "synthetic_raw" && strings.TrimSpace(call.Custom) == "synthetic-input"
	}
	n.check("custom_tool", first, valid)
	if valid {
		n.roundTrip(ctx, "custom_tool_round_trip", body, first)
	} else {
		n.uncertain("custom_tool_round_trip", first)
	}
	if n.protocol == "openai-responses" {
		body := toolBody(n.protocol, n.s.Target.Model, "required", false)
		body["tools"] = []any{nativeObject{"type": "namespace", "name": "synthetic", "description": "Synthetic read-only tools.", "tools": []any{nativeTool(n.protocol, false)}}}
		first := n.infer(ctx, body, false, true)
		valid := validCalls(first, true)
		if valid {
			for _, call := range first.Reply.Candidates[0].Calls {
				valid = valid && call.Namespace == "synthetic"
			}
		}
		n.check("namespace", first, valid)
		if valid {
			n.roundTrip(ctx, "namespace_round_trip", body, first)
		} else {
			n.uncertain("namespace_round_trip", first)
		}
	}
}
