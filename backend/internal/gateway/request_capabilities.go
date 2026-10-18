package gateway

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func requestTag(raw []byte) string {
	if len(raw) > 512 {
		return "unverified"
	}
	return textValue(raw)
}

func requestDataURL(raw []byte) bool {
	if bytes.HasPrefix(raw, []byte(`"data:`)) {
		return true
	}
	// Decode at most the prefix. Never allocate/decode an entire inline image.
	if len(raw) < 2 || raw[0] != '"' {
		return false
	}
	at := 1
	for _, want := range []byte("data:") {
		if at >= len(raw) {
			return false
		}
		got := raw[at]
		at++
		if got == '\\' {
			if at+5 > len(raw) || raw[at] != 'u' {
				return false
			}
			at++
			n := 0
			for range 4 {
				c := raw[at]
				at++
				digit := -1
				switch {
				case c >= '0' && c <= '9':
					digit = int(c - '0')
				case c >= 'a' && c <= 'f':
					digit = int(c-'a') + 10
				case c >= 'A' && c <= 'F':
					digit = int(c-'A') + 10
				}
				if digit < 0 {
					return false
				}
				n = n*16 + digit
			}
			if n > 127 {
				return false
			}
			got = byte(n)
		}
		if got != want {
			return false
		}
	}
	return true
}

// Compiled once from native request fields, without decoding message/tool
// bodies into maps or storing them. Every requirement must be witnessed by the
// same candidate. The finite IDs refer to retained manifest assertions.
type nativeRequirements struct {
	capabilities []CapabilityRequirement
	maxOutput    int64
	protocol     string
	mcp, compact bool
}

func compileNativeRequirements(format channel.Format, body []byte, stream bool, path string) nativeRequirements {
	out := nativeRequirements{protocol: nativeProtocol(format)}
	var scratch Observer
	root := scratch.objectOf(body)
	if tier := requestTag(root.get("service_tier")); format != channel.FormatOpenAIImages && tier != "" && tier != "auto" && actualMetricTier(format, tier) != "unknown" {
		id := out.protocol + ".cache_tier/service_tier/" + actualMetricTier(format, tier)
		if strings.HasPrefix(path, "/backend-api/codex/") {
			id = "codex-special.RL24/service_tier/" + actualMetricTier(format, tier)
		}
		out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: id})
	}
	if strings.HasPrefix(path, "/backend-api/codex/") {
		return out
	}
	add := func(family, label string) {
		id := out.protocol + "." + family + "/" + label
		for _, v := range out.capabilities {
			if v.ID == id {
				return
			}
		}
		out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: id})
	}
	// A supplied sampling/stop parameter is an explicit contract requirement,
	// including zero. Missing and JSON null carry no such requirement. Use the
	// independent report assertion rather than a generic output-scenario pass.
	present := func(raw []byte) bool { return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
	if format == channel.FormatOpenAIChat {
		for _, field := range []string{"temperature", "top_p", "stop"} {
			if present(root.get(field)) {
				add("output", field)
			}
		}
	}
	if format == channel.FormatAnthropic {
		if present(root.get("stop_sequences")) {
			add("output", "stop_sequences")
		}
		if present(root.get("system")) {
			add("base", "role_and_instruction_semantics")
		}
	}
	if format == channel.FormatOpenAIResponses {
		text := scratch.objectOf(root.get("text"))
		if present(text.get("verbosity")) {
			add("output", "text.verbosity")
		}
		if present(root.get("instructions")) {
			add("base", "role_and_instruction_semantics")
		}
	}
	if format == channel.FormatOpenAIImages {
		endpoint := imageCapabilityEndpoint(path)
		out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/actual_image_model"})
		if stream {
			out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/native_sse_lifecycle"})
		}
		if endpoint == "images.edits" {
			if mask := root.get("mask"); len(mask) > 0 && string(mask) != "null" && string(mask) != `""` {
				out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/masked_edit_preserves_unmasked_region"})
			}
			inputs := 0
			for _, field := range []string{"image", "images"} {
				eachElement(root.get(field), func([]byte) { inputs++ })
			}
			if inputs > 1 {
				out.capabilities = append(out.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/inline_url_mask_multiple_images"})
			}
		}
		return out
	}
	if stream {
		add("stream", "text_lifecycle")
	}
	// Explicit numeric output limits use a proven lower bound, not a quote's
	// advertised context or mere acceptance of a parameter value.
	limit := root.get("max_completion_tokens")
	if format == channel.FormatOpenAIResponses {
		limit = root.get("max_output_tokens")
	}
	if format == channel.FormatAnthropic {
		limit = root.get("max_tokens")
	}
	if n, ok := count(limit); ok {
		out.maxOutput = n
	}
	if format == channel.FormatOpenAIChat {
		if n, ok := count(root.get("n")); ok && n > 1 {
			add("multi", "native_multiple_candidates")
		}
	}
	var tools func([]byte, int)
	tools = func(raw []byte, depth int) {
		if depth > 6 {
			return
		}
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		t := scratch.objectOf(raw)
		kind := requestTag(t.get("type"))
		if format == channel.FormatGemini {
			if len(t.get("functionDeclarations")) > 0 {
				add("tools", "native_declarations")
				add("tools", "call_result_association")
			}
			for field, feature := range map[string]string{"googleSearch": "search", "codeExecution": "code", "urlContext": "url_context"} {
				if t.has(field) {
					label := map[string]string{"search": "native_server_search_request_execution_result", "code": "native_in_call_execution_and_result", "url_context": "native_url_context_and_result"}[feature]
					add(feature, label)
				}
			}
			return
		}
		switch {
		case kind == "function" || format == channel.FormatAnthropic && (kind == "" || kind == "custom"):
			add("tools", "native_declarations")
			add("tools", "call_result_association")
			f := scratch.objectOf(t.get("function"))
			if string(t.get("strict")) == "true" || string(f.get("strict")) == "true" {
				add("tools", "strict_schema")
			}
		case kind == "custom":
			add("tools", "custom_tool_round_trip")
		case kind == "namespace":
			add("tools", "namespace_round_trip")
			eachElement(t.get("tools"), func(raw []byte) { tools(raw, depth+1) })
		case kind == "web_search" || kind == "web_search_preview":
			add("search", "native_server_search_request_execution_result")
		case kind == "code_interpreter":
			add("code", "native_in_call_execution_and_result")
		case kind == "image_generation":
			add("image_tool", "native_image_output")
		case kind == "mcp":
			out.mcp = true
			add("mcp", "authorization_and_tool_round_trip")
		case kind == "web_search_20250305":
			add("server_tools", kind)
		case kind == "code_execution_20250825":
			add("server_tools", kind)
		case kind == "mcp_toolset":
			out.mcp = true
			add("mcp", "mcp_toolset")
			add("mcp", "mcp_tool_use_and_mcp_tool_result")
		default:
			// Legal unknown extensions remain opaque. Do not infer a known
			// capability (or a made-up failure capability) from arbitrary text.
		}
	}
	eachElement(root.get("tools"), func(raw []byte) { tools(raw, 0) })
	choice := root.get("tool_choice")
	choiceText := requestTag(choice)
	c := scratch.objectOf(choice)
	if choiceText == "" {
		choiceText = requestTag(c.get("type"))
	}
	switch choiceText {
	case "auto", "none", "required":
		add("tools", choiceText)
	case "any":
		add("tools", "required")
	case "function", "tool":
		add("tools", "named_tool")
	case "allowed_tools":
		add("tools", "allowed_tools")
	}
	if v := string(root.get("parallel_tool_calls")); v == "true" || v == "false" {
		add("tools", "parallel_tool_calls_"+v)
	}
	if v := string(c.get("disable_parallel_tool_use")); v == "true" {
		add("tools", "parallel_tool_calls_false")
	} else if v == "false" {
		add("tools", "parallel_tool_calls_true")
	}
	output := scratch.objectOf(root.get("response_format"))
	if format == channel.FormatOpenAIResponses {
		text := scratch.objectOf(root.get("text"))
		output = scratch.objectOf(text.get("format"))
	}
	switch requestTag(output.get("type")) {
	case "json_object":
		add("output", "json_object")
	case "json_schema":
		add("output", "json_schema_strict")
	}
	if r := scratch.objectOf(root.get("reasoning")); r.has("effort") {
		if format == channel.FormatOpenAIResponses {
			add("output", "reasoning.effort")
		} else {
			add("output", "reasoning_effort")
		}
	}
	if root.has("reasoning_effort") {
		add("output", "reasoning_effort")
	}
	if root.has("thinking") {
		add("reasoning", "native_current_configuration")
	}
	if format == channel.FormatAnthropic {
		config := scratch.objectOf(root.get("output_config"))
		if config.has("format") {
			add("output", "output_config.format")
		}
		if config.has("effort") {
			add("reasoning", "native_current_configuration")
		}
		if root.has("mcp_servers") {
			out.mcp = true
			add("mcp", "mcp_servers:url")
			add("mcp", "mcp_tool_use_and_mcp_tool_result")
		}
		management := scratch.objectOf(root.get("context_management"))
		eachElement(management.get("edits"), func(raw []byte) {
			e := scratch.objectOf(raw)
			if strings.HasPrefix(requestTag(e.get("type")), "compact_") {
				out.compact = true
				add("count_compact", "complete_compacted_window_replay")
			}
		})
	}
	if format == channel.FormatGemini {
		config := scratch.objectOf(root.get("generationConfig"))
		for _, field := range []string{"temperature", "topP", "topK", "stopSequences"} {
			if present(config.get(field)) {
				add("output", field)
			}
		}
		if present(root.get("systemInstruction")) {
			add("base", "role_and_instruction_semantics")
		}
		if n, ok := count(config.get("maxOutputTokens")); ok {
			out.maxOutput = n
		}
		if n, ok := count(config.get("candidateCount")); ok && n > 1 {
			add("multi", "native_multiple_candidates")
		}
		if config.has("responseMimeType") {
			add("output", "responseMimeType")
		}
		if config.has("responseJsonSchema") || config.has("responseSchema") {
			add("output", "responseJsonSchema")
		}
		if config.has("thinkingConfig") {
			add("reasoning", "native_current_configuration")
		}
		eachElement(config.get("responseModalities"), func(raw []byte) {
			switch requestTag(raw) {
			case "IMAGE":
				add("image_tool", "native_image_output")
			case "AUDIO":
				add("audio", "native_output_when_applicable")
			}
		})
		toolConfig := scratch.objectOf(root.get("toolConfig"))
		f := scratch.objectOf(toolConfig.get("functionCallingConfig"))
		switch requestTag(f.get("mode")) {
		case "AUTO":
			add("tools", "auto")
		case "NONE":
			add("tools", "none")
		case "ANY":
			add("tools", "required")
		case "VALIDATED":
			add("tools", "VALIDATED")
		}
		if f.has("allowedFunctionNames") {
			add("tools", "allowedFunctionNames")
		}
	}
	images := 0
	var part func([]byte, int)
	part = func(raw []byte, depth int) {
		if depth > 6 {
			return
		}
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		p := scratch.objectOf(raw)
		kind := requestTag(p.get("type"))
		role := requestTag(p.get("role"))
		if (format == channel.FormatOpenAIChat || format == channel.FormatOpenAIResponses) && (role == "system" || role == "developer") {
			add("base", "role_and_instruction_semantics")
		}
		media := func(family string, remote bool) {
			label := "native_inline_content_recognition"
			if family == "vision" {
				images++
				label = "inline_content_recognition"
				if remote {
					label = "url_or_uri_content_recognition"
				}
			}
			if family == "document" && remote {
				label = "native_url_when_supported"
			}
			if family == "audio" {
				label = "native_inline_input_when_applicable"
				if format == channel.FormatOpenAIResponses {
					label = "vendor_extension.input_audio"
				}
			}
			if family == "video" {
				label = "inline_video_content_recognition"
				if remote {
					label = "url_video_content_recognition"
				}
			}
			add(family, label)
		}
		switch kind {
		case "image_url", "input_image":
			v := p.get("image_url")
			if len(v) > 0 && v[0] != '"' {
				image := scratch.objectOf(v)
				v = image.get("url")
			}
			media("vision", !requestDataURL(v))
		case "image":
			source := scratch.objectOf(p.get("source"))
			media("vision", requestTag(source.get("type")) == "url")
		case "input_audio":
			media("audio", false)
		case "file", "input_file", "document":
			if p.has("file_url") {
				media("document", true)
			} else {
				source := scratch.objectOf(p.get("source"))
				media("document", requestTag(source.get("type")) == "url")
			}
		case "thinking", "redacted_thinking", "reasoning":
			if p.has("signature") || p.has("encrypted_content") || kind == "redacted_thinking" {
				add("reasoning", "complete_opaque_signature_or_encrypted_replay")
			}
		case "compaction":
			add("count_compact", "complete_compacted_window_replay")
		case "tool_result", "function_call_output", "custom_tool_call_output":
			add("tools", "call_result_association")
		}
		if p.has("thoughtSignature") {
			add("reasoning", "complete_opaque_signature_or_encrypted_replay")
		}
		if p.has("functionCall") || p.has("functionResponse") {
			add("tools", "call_result_association")
		}
		if p.has("tool_calls") || role == "tool" {
			add("tools", "call_result_association")
		}
		if p.has("cache_control") {
			add("cache_tier", "cache_options_acceptance")
		}
		for _, field := range []string{"inlineData", "fileData"} {
			if p.has(field) {
				m := scratch.objectOf(p.get(field))
				mime := requestTag(m.get("mimeType"))
				family := "document"
				switch {
				case strings.HasPrefix(mime, "image/"):
					family = "vision"
				case strings.HasPrefix(mime, "audio/"):
					family = "audio"
				case strings.HasPrefix(mime, "video/"):
					family = "video"
				}
				media(family, field == "fileData")
			}
		}
		for _, field := range []string{"content", "parts"} {
			eachElement(p.get(field), func(raw []byte) { part(raw, depth+1) })
		}
	}
	for _, field := range []string{"messages", "input", "contents"} {
		eachElement(root.get(field), func(raw []byte) { part(raw, 0) })
	}
	if images > 1 {
		add("vision", "multiple_images_when_native")
	}
	if root.has("audio") {
		add("audio", "native_output_when_applicable")
	}
	eachElement(root.get("modalities"), func(raw []byte) {
		if requestTag(raw) == "audio" {
			add("audio", "native_output_when_applicable")
		}
	})
	return out
}

func imageCapabilityEndpoint(path string) string {
	if strings.HasSuffix(path, "/edits") {
		return "images.edits"
	}
	if strings.HasSuffix(path, "/variations") {
		return "images.variations"
	}
	return "images.generations"
}

func addMultipartImageRequirements(req *nativeRequirements, plan *imageMultipart, path string) {
	endpoint := imageCapabilityEndpoint(path)
	req.capabilities = append(req.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/native_multipart"})
	if endpoint == "images.edits" {
		if plan.mask {
			req.capabilities = append(req.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/masked_edit_preserves_unmasked_region"})
		}
		if plan.inputImages > 1 {
			req.capabilities = append(req.capabilities, CapabilityRequirement{Layer: "upstream_protocol", ID: endpoint + "/inline_url_mask_multiple_images"})
		}
	}
}

// Resolve only relevant headers with the same remove/set precedence as actual
// outbounds. Do not allocate a full outbound header block while ranking.
func effectiveCapabilityHeader(client http.Header, advanced channel.Advanced, name string) string {
	value := client.Get(name)
	for _, v := range strings.Split(client.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(v), name) {
			value = ""
		}
	}
	for _, v := range advanced.HeaderRules.Remove {
		if strings.EqualFold(v, name) {
			value = ""
		}
	}
	for _, v := range advanced.HeaderRules.Set {
		if strings.EqualFold(v.Name, name) {
			value = v.Value
		}
	}
	return value
}

func missingRequestCapability(c Candidate, req nativeRequirements, headers http.Header) string {
	for _, cap := range req.capabilities {
		if !cap.supportedBy(c.Evidence) {
			return cap.ID
		}
	}
	if req.maxOutput > 0 && c.VerifiedCapacity[req.protocol+".length_search@upstream_protocol/max_output"] < req.maxOutput {
		return "verified_max_output_insufficient_or_unknown"
	}
	if req.protocol == "native-messages" && (len(req.capabilities) > 0 || req.maxOutput > 0) {
		if effectiveCapabilityHeader(headers, c.Advanced, "Anthropic-Version") != "2023-06-01" {
			return "protocol_version_not_verified"
		}
		beta := effectiveCapabilityHeader(headers, c.Advanced, "Anthropic-Beta")
		contains := func(want string) bool {
			for _, v := range strings.Split(beta, ",") {
				if strings.TrimSpace(v) == want {
					return true
				}
			}
			return false
		}
		if req.mcp && !contains("mcp-client-2025-11-20") {
			return "mcp_beta_not_verified"
		}
		if req.compact && !contains("compact-2026-09-04") {
			return "compact_beta_not_verified"
		}
	}
	return ""
}
