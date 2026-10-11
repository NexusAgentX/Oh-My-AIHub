package gateway

import "testing"

// Error bodies as the four formats' upstreams (and the relays in front of
// them) send them. They are 4xx answers outside the fallback status list, or
// bodies a relay put there; the 404 forms fall back by status anyway but must
// be recognised the same way.
func TestModelUnavailableRecognisesEachFormatsMissingModelError(t *testing.T) {
	cases := []struct{ name, body string }{
		{"openai chat code", `{"error":{"message":"The model ` + "`gpt-9`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","param":null,"code":"model_not_found"}}`},
		{"openai responses code", `{"error":{"message":"The requested model 'gpt-9' does not exist.","type":"invalid_request_error","param":"model","code":"model_not_found"}}`},
		{"anthropic shape from a relay", `{"type":"error","error":{"type":"invalid_request_error","message":"The model ` + "`claude-9-opus`" + ` is not supported by this API."},"request_id":"req_011"}`},
		{"gemini not found status", `{"error":{"code":404,"message":"models/gemini-9 is not found for API version v1beta, or is not supported for generateContent. Call ListModels to see the list of available models and their supported methods.","status":"NOT_FOUND"}}`},
		{"gemini status on a relay's 400", `{"error":{"code":400,"message":"no such thing","status":"NOT_FOUND"}}`},
		{"gemini streaming wraps the error in an array", `[{"error":{"code":404,"message":"gone","status":"NOT_FOUND"}}]`},
		{"gemini message alone", `{"error":{"code":400,"message":"models/gemini-9 is not found for API version v1beta","status":"INVALID_ARGUMENT"}}`},

		{"code invalid_model", `{"error":{"message":"bad","code":"invalid_model"}}`},
		{"code model_not_available", `{"error":{"code":"model_not_available"}}`},
		{"code unsupported_model", `{"error":{"message":"x","type":"invalid_request_error","code":"unsupported_model"}}`},
		{"code in other case", `{"error":{"code":"Model_Not_Found"}}`},
		{"top level code", `{"code":"model_not_found","message":"x"}`},

		{"message with the model quoted", `{"error":{"message":"model 'llama9' not found","type":"invalid_request_error"}}`},
		{"message without a name", `{"error":{"message":"Model not found"}}`},
		{"unsupported model prefix", `{"error":{"message":"Unsupported model: gpt-9"}}`},
		{"no such model", `{"error":{"message":"No such model: gpt-9"}}`},
		{"unknown model", `{"error":{"message":"Unknown model gpt-9"}}`},
		{"invalid model (Mistral)", `{"object":"error","message":"Invalid model: mistral-9","type":"invalid_model","param":null,"code":"1500"}`},
		{"model not exist (DeepSeek)", `{"error":{"message":"Model Not Exist","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`},
		{"model does not exist (SiliconFlow)", `{"code":20012,"message":"Model does not exist. Please check it carefully.","data":null}`},
		{"not available", `{"error":{"message":"The model gpt-9 is not available for your plan"}}`},
		{"not a valid model id (OpenRouter)", `{"error":{"message":"gpt-9 is not a valid model ID","code":400}}`},
		{"error as a string (Ollama)", `{"error":"model 'llama9' not found"}`},
		{"top level message", `{"code":400,"message":"The 'gpt-9' model is not supported"}`},
		{"top level detail (ChatGPT backend)", `{"detail":"The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."}`},
		{"upper case", `{"error":{"message":"THE MODEL GPT-9 DOES NOT EXIST"}}`},

		{"chinese does not exist", `{"error":{"message":"模型 gpt-9 不存在或无权限访问","type":"invalid_request_error"}}`},
		{"chinese no channel", `{"error":{"message":"当前分组 default 下对于模型 gpt-9 无可用渠道","type":"new_api_error"}}`},
		{"chinese zhipu", `{"error":{"code":"1211","message":"模型不存在，请检查模型代码。"}}`},
		{"chinese not supported", `{"code":400,"message":"该模型不支持当前接口"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !ModelUnavailable([]byte(tc.body)) {
				t.Fatalf("not recognised: %s", tc.body)
			}
		})
	}
}

// Errors about the request itself must stay the client's business: they are
// handed back and never counted against the channel.
func TestModelUnavailableLeavesRequestErrorsAlone(t *testing.T) {
	cases := []struct{ name, body string }{
		{"openai max_tokens too large", `{"error":{"message":"max_tokens is too large: 1000000. This model supports at most 4096 completion tokens, whereas you provided 1000000.","type":"invalid_request_error","param":"max_tokens","code":null}}`},
		{"openai max_tokens unsupported with this model", `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`},
		{"openai temperature", `{"error":{"message":"Unsupported value: 'temperature' does not support 0.5 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`},
		{"openai bad role", `{"error":{"message":"Invalid value: 'bot'. Supported values are: 'system', 'assistant', 'user', 'function', and 'tool'.","type":"invalid_request_error","param":"messages[0].role","code":"invalid_value"}}`},
		{"openai missing messages", `{"error":{"message":"you must provide a messages parameter","type":"invalid_request_error","param":"messages","code":null}}`},
		{"openai context length", `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 9000 tokens. Please reduce the length of the messages.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`},
		{"openai model without a tool parameter", `{"error":{"message":"The model ` + "`gpt-4`" + ` does not support the 'tools' parameter.","type":"invalid_request_error","param":null,"code":null}}`},
		{"openai other parameter echoing the words", `{"error":{"message":"Invalid value: 'model not found'. Supported values are: 'user'.","type":"invalid_request_error","param":"messages[0].role","code":"invalid_value"}}`},
		{"openai quota", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`},
		{"responses missing input", `{"error":{"message":"Missing required parameter: 'input'.","type":"invalid_request_error","param":"input","code":"missing_required_parameter"}}`},
		{"anthropic max_tokens", `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 100000 > 8192, which is the maximum allowed number of output tokens for claude-3-5-sonnet-20241022"}}`},
		{"anthropic bad messages", `{"type":"error","error":{"type":"invalid_request_error","message":"messages: text content blocks must be non-empty"}}`},
		{"anthropic prefill", `{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`},
		{"anthropic native 404 is told apart by status", `{"type":"error","error":{"type":"not_found_error","message":"model: claude-9-opus"}}`},
		{"gemini bad payload", `{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"foo\" at 'generation_config': Cannot find field.","status":"INVALID_ARGUMENT"}}`},
		{"gemini empty parts", `{"error":{"code":400,"message":"Unable to submit request because it must have a text parameter. Add a text parameter and try again.","status":"INVALID_ARGUMENT"}}`},
		{"gemini max tokens", `{"error":{"code":400,"message":"Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 8193 (exclusive).","status":"INVALID_ARGUMENT"}}`},

		{"numeric code and no message", `{"error":{"code":400}}`},
		{"non string status", `{"error":{"status":404,"message":"nope"}}`},
		{"unrelated code", `{"error":{"code":"invalid_api_key","message":"Incorrect API key provided"}}`},
		{"plain text", `model not found`},
		{"html", `<html><body>model does not exist</body></html>`},
		{"json string", `"model not found"`},
		{"json null", `null`},
		{"empty array", `[]`},
		{"empty object", `{}`},
		{"truncated json", `{"error":{"code":"model_not_found"`},
		{"empty", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ModelUnavailable([]byte(tc.body)) {
				t.Fatalf("recognised as a missing model: %s", tc.body)
			}
		})
	}
}

// Only the start of a long message is read, so a body of the maximum size
// costs no more than a small one.
func TestModelUnavailableReadsOnlyTheStartOfALongMessage(t *testing.T) {
	filler := make([]byte, 100_000)
	for index := range filler {
		filler[index] = 'a'
	}
	body := `{"error":{"message":"` + string(filler) + ` model not found"}}`
	if ModelUnavailable([]byte(body)) {
		t.Fatal("a verdict far past the start of the message must not count")
	}
}
