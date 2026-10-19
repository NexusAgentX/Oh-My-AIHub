package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestRequestedCapabilitiesReferToIndependentRetainedAssertions(t *testing.T) {
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, suite := range m.Suites {
		for _, r := range suite.Requirements {
			for _, a := range r.Assertions {
				registered[r.ID+"/"+a] = true
			}
		}
	}
	for _, tc := range []struct {
		format channel.Format
		body   string
	}{
		{channel.FormatOpenAIChat, `{"stream":true,"n":2,"parallel_tool_calls":false,"tools":[{"type":"function","function":{"name":"echo","strict":true}}],"tool_choice":"required","response_format":{"type":"json_schema"},"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,synthetic"}},{"type":"image_url","image_url":{"url":"https://example.invalid/synthetic"}}]}]}`},
		{channel.FormatOpenAIResponses, `{"tools":[{"type":"custom"},{"type":"namespace","tools":[{"type":"function","name":"echo"}]},{"type":"mcp"},{"type":"web_search"},{"type":"code_interpreter"},{"type":"image_generation"}],"reasoning":{"effort":"high"},"text":{"format":{"type":"json_object"}},"input":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"compaction","encrypted_content":"whole"}]}`},
		{channel.FormatAnthropic, `{"tools":[{"name":"echo","input_schema":{"type":"object"}},{"type":"web_search_20250305"},{"type":"code_execution_20250825"},{"type":"mcp_toolset"}],"mcp_servers":[{"type":"url","url":"https://example.invalid"}],"thinking":{"type":"enabled"},"output_config":{"format":{"type":"json_schema"}},"messages":[{"role":"assistant","content":[{"type":"thinking","signature":"opaque"}]}]}`},
		{channel.FormatGemini, `{"tools":[{"functionDeclarations":[{"name":"echo"}]},{"googleSearch":{}},{"codeExecution":{}},{"urlContext":{}}],"toolConfig":{"functionCallingConfig":{"mode":"VALIDATED","allowedFunctionNames":["echo"]}},"generationConfig":{"candidateCount":2,"responseMimeType":"application/json","responseJsonSchema":{"type":"object"},"responseModalities":["IMAGE","AUDIO"],"thinkingConfig":{}},"contents":[{"parts":[{"thoughtSignature":"opaque"},{"inlineData":{"mimeType":"image/png","data":"synthetic"}},{"fileData":{"mimeType":"video/mp4","fileUri":"https://example.invalid"}}]}]}`},
	} {
		req := compileNativeRequirements(tc.format, []byte(tc.body), true, "/v1/native")
		if len(req.capabilities) < 4 {
			t.Fatalf("features were not extracted: %s %+v", tc.format, req)
		}
		for _, cap := range req.capabilities {
			if !registered[cap.ID] {
				t.Fatalf("capability has no retained report assertion: %s", cap.ID)
			}
		}
	}
}

func TestRequiredCapabilitiesCannotBeUnionedAcrossCandidates(t *testing.T) {
	req := compileNativeRequirements(channel.FormatOpenAIResponses, []byte(`{"tools":[{"type":"function","name":"echo"}],"text":{"format":{"type":"json_schema"}}}`), false, "/v1/responses")
	left, right := Candidate{GroupID: "group-a", Evidence: map[string]bool{}}, Candidate{GroupID: "group-b", Evidence: map[string]bool{}}
	for i, cap := range req.capabilities {
		if i%2 == 0 {
			left.Evidence[cap.Layer+"/"+cap.ID] = true
		} else {
			right.Evidence[cap.Layer+"/"+cap.ID] = true
		}
	}
	if missingRequestCapability(left, req, nil) == "" || missingRequestCapability(right, req, nil) == "" {
		t.Fatal("cross-group union invented a usable candidate")
	}
	for k, v := range right.Evidence {
		left.Evidence[k] = v
	}
	if missingRequestCapability(left, req, nil) != "" {
		t.Fatal("same candidate could not satisfy all independent capabilities")
	}
	left.Evidence = map[string]bool{} // stale/unknown proof is not support.
	if missingRequestCapability(left, req, nil) == "" {
		t.Fatal("missing evidence silently fell back")
	}
	plain := compileNativeRequirements(channel.FormatOpenAIResponses, []byte(`{"input":"literal tools, image_url, encrypted_content are ordinary text"}`), false, "/v1/responses")
	if missingRequestCapability(left, plain, nil) != "" {
		t.Fatal("unrelated unknown capability disabled ordinary inference")
	}
	extension := compileNativeRequirements(channel.FormatOpenAIResponses, []byte(`{"tools":[{"type":"future_legal_extension","payload":{"any":"opaque"}}],"future_option":{"image_url":"ordinary text"}}`), false, "/v1/responses")
	if len(extension.capabilities) != 0 || missingRequestCapability(left, extension, nil) != "" {
		t.Fatal("legal unknown extension was converted into a guessed hard requirement")
	}
}

func TestExplicitSamplingStopsAndInstructionsNeedTheirOwnEvidence(t *testing.T) {
	for _, tc := range []struct {
		format channel.Format
		body   string
		proof  string
	}{
		{channel.FormatOpenAIChat, `{"temperature":0}`, "openai-chat-completions.output/temperature"},
		{channel.FormatOpenAIChat, `{"top_p":0.8}`, "openai-chat-completions.output/top_p"},
		{channel.FormatOpenAIChat, `{"stop":["STOP"]}`, "openai-chat-completions.output/stop"},
		{channel.FormatOpenAIChat, `{"messages":[{"role":"system","content":"Synthetic instruction"}]}`, "openai-chat-completions.base/role_and_instruction_semantics"},
		{channel.FormatOpenAIChat, `{"messages":[{"role":"developer","content":"Synthetic instruction"}]}`, "openai-chat-completions.base/role_and_instruction_semantics"},
		{channel.FormatOpenAIResponses, `{"text":{"verbosity":"low"}}`, "openai-responses.output/text.verbosity"},
		{channel.FormatOpenAIResponses, `{"instructions":"Synthetic instruction"}`, "openai-responses.base/role_and_instruction_semantics"},
		{channel.FormatOpenAIResponses, `{"input":[{"role":"developer","content":"Synthetic instruction"}]}`, "openai-responses.base/role_and_instruction_semantics"},
		{channel.FormatGemini, `{"generationConfig":{"temperature":0}}`, "native-generate-content.output/temperature"},
		{channel.FormatGemini, `{"generationConfig":{"topP":0.8}}`, "native-generate-content.output/topP"},
		{channel.FormatGemini, `{"generationConfig":{"topK":20}}`, "native-generate-content.output/topK"},
		{channel.FormatGemini, `{"generationConfig":{"stopSequences":["STOP"]}}`, "native-generate-content.output/stopSequences"},
		{channel.FormatGemini, `{"systemInstruction":{"parts":[{"text":"Synthetic instruction"}]}}`, "native-generate-content.base/role_and_instruction_semantics"},
		{channel.FormatAnthropic, `{"stop_sequences":["STOP"]}`, "native-messages.output/stop_sequences"},
		{channel.FormatAnthropic, `{"system":"Synthetic instruction"}`, "native-messages.base/role_and_instruction_semantics"},
	} {
		t.Run(string(tc.format)+"/"+tc.proof, func(t *testing.T) {
			req := compileNativeRequirements(tc.format, []byte(tc.body), false, "/v1/native")
			c := Candidate{Evidence: map[string]bool{}}
			if got := missingRequestCapability(c, req, nil); got != tc.proof {
				t.Fatal("request escaped its explicit support requirement", got, tc.proof)
			}
			c.Evidence["upstream_protocol/"+tc.proof] = true
			headers := http.Header{"Anthropic-Version": {"2023-06-01"}}
			if got := missingRequestCapability(c, req, headers); got != "" {
				t.Fatal("matching independent evidence did not authorize the request", got)
			}
		})
	}
	for _, format := range []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatGemini, channel.FormatAnthropic} {
		req := compileNativeRequirements(format, []byte(`{"temperature":null,"top_p":null,"stop":null,"instructions":null,"system":null,"systemInstruction":null,"generationConfig":{"temperature":null,"topP":null,"topK":null,"stopSequences":null},"text":{"verbosity":null}}`), false, "/v1/native")
		if len(req.capabilities) != 0 {
			t.Fatal("absent optional values invented a hard capability requirement", format, req)
		}
	}
}

func TestImageTransportMaskAndMultipleInputsRemainIndependent(t *testing.T) {
	req := compileNativeRequirements(channel.FormatOpenAIImages, []byte(`{"images":[{"image_url":"https://example.invalid/one"},{"image_url":"https://example.invalid/two"}],"mask":{"image_url":"https://example.invalid/mask"}}`), false, "/v1/images/edits")
	c := Candidate{GroupID: "group", Evidence: map[string]bool{"upstream_protocol/images.edits/actual_image_model": true}}
	if missingRequestCapability(c, req, nil) == "" {
		t.Fatal("generic image generation authorized mask/multiple-input editing")
	}
	for _, cap := range req.capabilities {
		c.Evidence[cap.Layer+"/"+cap.ID] = true
	}
	if missingRequestCapability(c, req, nil) != "" {
		t.Fatal("verified JSON edit could not route")
	}
	addMultipartImageRequirements(&req, &imageMultipart{mask: true, inputImages: 2}, "/v1/images/edits")
	if missingRequestCapability(c, req, nil) != "images.edits/native_multipart" {
		t.Fatal("JSON evidence authorized multipart")
	}
}

func TestCapabilityHeaderPreconditionsUseEffectiveOutgoingRules(t *testing.T) {
	req := compileNativeRequirements(channel.FormatAnthropic, []byte(`{"max_tokens":32,"mcp_servers":[{"type":"url","url":"https://example.invalid"}],"tools":[{"type":"mcp_toolset"}]}`), false, "/v1/messages")
	c := Candidate{GroupID: "group", Evidence: map[string]bool{}, VerifiedCapacity: map[string]int64{"native-messages.length_search@upstream_protocol/max_output": 32}}
	for _, cap := range req.capabilities {
		c.Evidence[cap.Layer+"/"+cap.ID] = true
	}
	headers := http.Header{"Anthropic-Version": {"2023-06-01"}, "Anthropic-Beta": {"mcp-client-2025-11-20"}}
	if got := missingRequestCapability(c, req, headers); got != "" {
		t.Fatal(got)
	}
	headers.Set("Anthropic-Version", "unverified")
	if got := missingRequestCapability(c, req, headers); got != "protocol_version_not_verified" {
		t.Fatal("mismatched version borrowed support", got)
	}
	c.Advanced.HeaderRules.Set = []channel.HeaderSet{{Name: "Anthropic-Version", Value: "2023-06-01"}}
	if got := missingRequestCapability(c, req, headers); got != "" {
		t.Fatal("approved outgoing rule did not restore tested header", got)
	}
	headers.Del("Anthropic-Beta")
	if got := missingRequestCapability(c, req, headers); got != "mcp_beta_not_verified" {
		t.Fatal(got)
	}
	headers.Set("Anthropic-Beta", "mcp-client-2025-11-20")
	c.VerifiedCapacity = map[string]int64{"native-messages.length_search@upstream_protocol/max_output": 31}
	if got := missingRequestCapability(c, req, headers); got != "verified_max_output_insufficient_or_unknown" {
		t.Fatal("advertised/accepted size was treated as achieved capacity", got)
	}
}

func TestServiceTierEvidenceCannotCrossNativeSurfaces(t *testing.T) {
	public := compileNativeRequirements(channel.FormatOpenAIResponses, []byte(`{"service_tier":"priority"}`), false, "/v1/responses")
	native := compileNativeRequirements(channel.FormatOpenAIResponses, []byte(`{"service_tier":"priority"}`), false, "/backend-api/codex/responses")
	c := Candidate{GroupID: "group", Evidence: map[string]bool{"upstream_protocol/openai-responses.cache_tier/service_tier/openai:fast": true}}
	if missingRequestCapability(c, public, nil) != "" || missingRequestCapability(c, native, nil) == "" {
		t.Fatal("public and Codex tier evidence were merged")
	}
}

func TestInlineImageClassificationDoesNotDecodeOrRetainLargeData(t *testing.T) {
	for _, prefix := range []string{`"data:`, `"\u0064ata:`} {
		if !requestDataURL([]byte(prefix + strings.Repeat("x", 1<<20) + `"`)) {
			t.Fatal("inline URL was classified as remote")
		}
	}
	req := compileNativeRequirements(channel.FormatOpenAIChat, []byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,`+strings.Repeat("x", 1<<20)+`"}}]}]}`), false, "/v1/chat/completions")
	if len(req.capabilities) != 1 || !strings.HasSuffix(req.capabilities[0].ID, "/inline_content_recognition") {
		t.Fatal("large inline image changed classification", req)
	}
}

func BenchmarkNativeRequirementsInlineImage(b *testing.B) {
	for _, size := range []int{1024, 2 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := []byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("x", size) + `"}}]}]}`)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				compileNativeRequirements(channel.FormatOpenAIChat, body, false, "/v1/chat/completions")
			}
		})
	}
}
