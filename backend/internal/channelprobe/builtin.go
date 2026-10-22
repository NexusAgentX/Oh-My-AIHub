package channelprobe

const toolPageURL = "https://example.com/"

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
