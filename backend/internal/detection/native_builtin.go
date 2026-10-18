package detection

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

const toolPageURL = "https://example.com/"
const toolPageTitle = "Example Domain"
const codeAnswer = "3973"

func builtinBody(protocol, model, kind string) nativeObject {
	prompt := "Use the web search tool to find the title of the IANA example.com webpage. Reply only with the page title, without Markdown."
	if kind == "url_context" {
		prompt = "Use the URL context tool to retrieve " + toolPageURL + " and reply only with its page title, without Markdown."
	}
	if kind == "code" {
		prompt = "Use code execution to compute and print 137 * 29. Reply only with the printed decimal integer."
	}
	if kind == "image_tool" {
		prompt = "Generate one simple image of a blue circle on a white background. No text."
	}
	body := baseNative(protocol, model, prompt)
	outputLimit(protocol, body, 2048)
	switch protocol {
	case "openai-responses":
		tool := nativeObject{"type": "web_search"}
		body["include"] = []any{"web_search_call.action.sources"}
		if kind == "code" {
			tool = nativeObject{"type": "code_interpreter", "container": nativeObject{"type": "auto"}}
			body["include"] = []any{"code_interpreter_call.outputs"}
		}
		if kind == "image_tool" {
			tool = nativeObject{"type": "image_generation", "output_format": "png", "quality": "low"}
			delete(body, "include")
		}
		body["tools"], body["tool_choice"], body["max_tool_calls"] = []any{tool}, nativeObject{"type": tool["type"]}, 1
	case "native-messages":
		tool := nativeObject{"type": "web_search_20250305", "name": "web_search", "max_uses": 1}
		if kind == "code" {
			tool = nativeObject{"type": "code_execution_20250825", "name": "code_execution"}
		}
		body["tools"] = []any{tool}
	case "native-generate-content":
		key := map[string]string{"search": "googleSearch", "url_context": "urlContext", "code": "codeExecution"}[kind]
		if kind == "image_tool" {
			body["generationConfig"] = nativeObject{"responseModalities": []any{"TEXT", "IMAGE"}, "imageConfig": nativeObject{"aspectRatio": "1:1"}}
		} else {
			body["tools"] = []any{nativeObject{key: nativeObject{}}}
		}
	}
	return body
}

func publicToolURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}
func responseItems(out nativeOutcome, kind string) []nativeObject {
	var result []nativeObject
	for _, raw := range list(out.Reply.Object["output"]) {
		item := object(raw)
		if str(item["type"]) == kind {
			result = append(result, item)
		}
	}
	return result
}
func nativeParts(out nativeOutcome) []any {
	if len(out.Reply.Candidates) != 1 {
		return nil
	}
	return out.Reply.Candidates[0].Parts
}
func geminiCandidate(out nativeOutcome) nativeObject {
	data := list(out.Reply.Object["candidates"])
	if len(data) != 1 {
		return nil
	}
	return object(data[0])
}
func (n *nativeScene) builtinCheck(label string, out nativeOutcome, observed, valid bool) {
	if out.State == "pass" && !observed {
		n.uncertain(label, out)
	} else {
		n.check(label, out, valid)
	}
	if len(out.ProofAttempts) > 0 {
		a := n.checks[label]
		a.AttemptIDs = slices.Compact(append(slices.Clone(out.ProofAttempts), a.AttemptIDs...))
		n.checks[label] = a
	}
}
func (n *nativeScene) toolMeter(out nativeOutcome, images, tools int64) {
	key := "tool-meter-" + out.Response.AttemptID
	if len(n.s.Recall(key)) > 0 {
		return
	}
	if n.s.Remember(key, []byte{1}) == nil {
		_ = n.s.Meter(0, 0, images, tools)
	}
}

func responseSearch(out nativeOutcome) (bool, bool) {
	calls := responseItems(out, "web_search_call")
	valid := false
	for _, call := range calls {
		action := object(call["action"])
		sources := list(action["sources"])
		goodSource := false
		for _, raw := range sources {
			source := object(raw)
			goodSource = goodSource || publicToolURL(str(source["url"]))
		}
		valid = valid || str(call["id"]) != "" && str(call["status"]) == "completed" && str(action["type"]) == "search" && (str(action["query"]) != "" || len(list(action["queries"])) > 0) && goodSource
	}
	return len(calls) > 0, valid && contentMatches(out, toolPageTitle) && nativeEndedNormally(out.Reply)
}
func geminiSearch(out nativeOutcome) (bool, bool) {
	metadata := object(geminiCandidate(out)["groundingMetadata"])
	queries := list(metadata["webSearchQueries"])
	source := false
	for _, raw := range list(metadata["groundingChunks"]) {
		source = source || publicToolURL(str(object(object(raw)["web"])["uri"]))
	}
	return metadata != nil, source && len(queries) > 0 && contentMatches(out, toolPageTitle) && nativeEndedNormally(out.Reply)
}
func (n *nativeScene) search(ctx context.Context) {
	out := n.infer(ctx, builtinBody(n.protocol, n.s.Target.Model, "search"), false, true)
	observed, valid := responseSearch(out)
	if n.protocol == "native-generate-content" {
		observed, valid = geminiSearch(out)
	}
	n.builtinCheck("native_server_search_request_execution_result", out, observed, valid)
	n.builtinCheck("reliable_usage_only", out, observed, valid && out.Reply.Usage.Reliable)
	if n.protocol == "openai-responses" && valid {
		count := int64(0)
		for _, call := range responseItems(out, "web_search_call") {
			if str(call["status"]) == "completed" {
				count++
			}
		}
		n.toolMeter(out, 0, count)
	}
}

func responseCode(out nativeOutcome) (bool, bool) {
	calls := responseItems(out, "code_interpreter_call")
	valid := false
	for _, call := range calls {
		for _, raw := range list(call["outputs"]) {
			output := object(raw)
			valid = valid || str(call["id"]) != "" && str(call["status"]) == "completed" && str(call["code"]) != "" && str(output["type"]) == "logs" && strings.TrimSpace(str(output["logs"])) == codeAnswer
		}
	}
	return len(calls) > 0, valid && contentMatches(out, codeAnswer) && nativeEndedNormally(out.Reply)
}
func geminiCode(out nativeOutcome) (bool, bool) {
	var code nativeObject
	observed, valid := false, false
	for _, raw := range nativeParts(out) {
		part := object(raw)
		if item := object(part["executableCode"]); item != nil {
			code = item
			observed = true
		}
		if result := object(part["codeExecutionResult"]); result != nil {
			observed = true
			valid = valid || code != nil && str(code["code"]) != "" && str(code["language"]) == "PYTHON" && str(result["outcome"]) == "OUTCOME_OK" && strings.TrimSpace(str(result["output"])) == codeAnswer && str(code["id"]) == str(result["id"])
		}
	}
	return observed, valid && contentMatches(out, codeAnswer) && nativeEndedNormally(out.Reply)
}
func (n *nativeScene) code(ctx context.Context) {
	out := n.infer(ctx, builtinBody(n.protocol, n.s.Target.Model, "code"), false, true)
	observed, valid := responseCode(out)
	if n.protocol == "native-generate-content" {
		observed, valid = geminiCode(out)
	}
	n.builtinCheck("native_in_call_execution_and_result", out, observed, valid)
	// Neither standard native response promises a billable fresh-container or
	// elapsed-time receipt. Missing receipts stay unknown; do not synthesize a
	// session charge from the first appearance of a container ID.
	n.uncertain("reliable_receipt_only", out)
	n.check("no_container_crud", out, true)
	if valid {
		n.toolMeter(out, 0, 1)
	}
}

func (n *nativeScene) imageTool(ctx context.Context) {
	out := n.infer(ctx, builtinBody(n.protocol, n.s.Target.Model, "image_tool"), false, true)
	observed, verified := false, int64(0)
	if n.protocol == "openai-responses" {
		for _, item := range responseItems(out, "image_generation_call") {
			observed = true
			if _, ok := inspectImage(str(item["result"])); ok && str(item["status"]) == "completed" {
				verified++
			}
		}
	} else {
		for _, raw := range nativeParts(out) {
			data := object(object(raw)["inlineData"])
			if strings.HasPrefix(str(data["mimeType"]), "image/") {
				observed = true
				p, ok := inspectImage(str(data["data"]))
				if ok && ((p.Format == "png" && str(data["mimeType"]) == "image/png") || (p.Format == "jpeg" && str(data["mimeType"]) == "image/jpeg")) {
					verified++
				}
			}
		}
	}
	valid := out.Reply.Structure && nativeEndedNormally(out.Reply) && verified > 0
	n.builtinCheck("native_image_output", out, observed, valid)
	n.builtinCheck("actual_outputs_and_reliable_usage", out, observed, valid && out.Reply.Usage.Reliable)
	if valid {
		n.toolMeter(out, verified, 1)
	}
}

func (n *nativeScene) urlContext(ctx context.Context) {
	out := n.infer(ctx, builtinBody(n.protocol, n.s.Target.Model, "url_context"), false, true)
	metadata := object(geminiCandidate(out)["urlContextMetadata"])
	valid := false
	for _, raw := range list(metadata["urlMetadata"]) {
		item := object(raw)
		valid = valid || str(item["retrievedUrl"]) == toolPageURL && str(item["urlRetrievalStatus"]) == "URL_RETRIEVAL_STATUS_SUCCESS"
	}
	n.builtinCheck("native_url_context_and_result", out, metadata != nil, valid && contentMatches(out, toolPageTitle) && nativeEndedNormally(out.Reply))
}

func geminiEchoParts(out nativeOutcome, kind string) bool {
	calls := map[string]nativeObject{}
	results := map[string]bool{}
	for _, raw := range nativeParts(out) {
		part := object(raw)
		if call := object(part["toolCall"]); str(call["toolType"]) == kind {
			id := str(call["id"])
			if calls[id] != nil || object(call["args"]) == nil {
				return false
			}
			calls[id] = call
		}
		if response := object(part["toolResponse"]); str(response["toolType"]) == kind {
			id := str(response["id"])
			if results[id] || calls[id] == nil || object(response["response"]) == nil {
				return false
			}
			results[id] = true
		}
	}
	return len(calls) > 0 && len(calls) == len(results)
}
func (n *nativeScene) serverEcho(ctx context.Context) {
	for _, kind := range []string{"search", "url_context"} {
		body := builtinBody(n.protocol, n.s.Target.Model, kind)
		out := n.infer(ctx, body, false, true)
		typeName, label := "GOOGLE_SEARCH_WEB", "ToolCall_ToolResponse_GOOGLE_SEARCH_WEB"
		if kind == "url_context" {
			typeName, label = "URL_CONTEXT", "ToolCall_ToolResponse_URL_CONTEXT"
		}
		valid := out.Reply.Structure && geminiEchoParts(out, typeName)
		observed := out.Reply.ToolKinds["toolCall"] || out.Reply.ToolKinds["toolResponse"]
		n.builtinCheck(label, out, observed, valid)
		if !valid {
			n.uncertain("matching_id_and_echo_native_parts", out)
			continue
		}
		next := n.plainReplayBody(body, out.Reply, "Repeat only the page title from the previous result. Do not call a tool again.")
		if next == nil {
			n.uncertain("matching_id_and_echo_native_parts", out)
			continue
		}
		resumed := n.infer(ctx, next, false, true)
		n.linkedCheck("matching_id_and_echo_native_parts", out, resumed, contentMatches(resumed, toolPageTitle) && nativeEndedNormally(resumed.Reply))
	}
}

func (n *nativeScene) safety(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
	body["safetySettings"] = []any{nativeObject{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_ONLY_HIGH"}, nativeObject{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_ONLY_HIGH"}}
	out := n.infer(ctx, body, false, true)
	n.check("native_safety_configuration", out, contentMatches(out, nativeMarker) && nativeEndedNormally(out.Reply))
	observed, valid := false, true
	if feedback := object(out.Reply.Object["promptFeedback"]); feedback != nil {
		observed = true
		valid = str(feedback["blockReason"]) != "" || len(list(feedback["safetyRatings"])) > 0
	}
	for _, rating := range list(geminiCandidate(out)["safetyRatings"]) {
		r := object(rating)
		observed = true
		valid = valid && str(r["category"]) != "" && slices.Contains([]string{"NEGLIGIBLE", "LOW", "MEDIUM", "HIGH"}, str(r["probability"]))
	}
	if observed {
		n.check("feedback_only_if_observed", out, valid)
	} else {
		n.unobserved("feedback_only_if_observed", out)
	}
}
