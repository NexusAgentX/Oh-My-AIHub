package detection

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const nestedSchemaJSON = `{"type":"object","properties":{"label":{"type":"string","enum":["violet"]},"meta":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false},"items":{"type":"array","items":{"type":"object","properties":{"value":{"type":"integer","enum":[7]}},"required":["value"],"additionalProperties":false}}},"required":["label","meta","items"],"additionalProperties":false}`
const structuredPrompt = `Return only a JSON object with label="violet", meta={"ok":true}, and items=[{"value":7}]. No Markdown or explanation.`

var compiledNativeSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	resource, err := jsonschema.UnmarshalJSON(strings.NewReader(nestedSchemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err = c.AddResource("synthetic.json", resource); err != nil {
		return nil, err
	}
	return c.Compile("synthetic.json")
})

func nativeNestedSchema() nativeObject {
	var o map[string]any
	_ = json.Unmarshal([]byte(nestedSchemaJSON), &o)
	return object(o)
}
func matchesNativeSchema(value any) bool {
	schema, err := compiledNativeSchema()
	if err != nil {
		return false
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 64<<10 {
		return false
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	return err == nil && schema.Validate(decoded) == nil
}
func structuredMatches(out nativeOutcome) bool {
	if !out.Reply.Structure || !nativeEndedNormally(out.Reply) {
		return false
	}
	o, err := decodeNative([]byte(out.Reply.Text))
	return err == nil && matchesNativeSchema(o) && str(o["label"]) == "violet" && object(o["meta"])["ok"] == true && len(list(o["items"])) == 1
}
func nativeFormat(protocol string, body nativeObject, strict bool) {
	format := nativeObject{"type": "json_object"}
	if strict {
		format = nativeObject{"type": "json_schema", "name": "synthetic_nested", "strict": true, "schema": nativeNestedSchema()}
	}
	switch protocol {
	case "openai-chat-completions":
		if strict {
			body["response_format"] = nativeObject{"type": "json_schema", "json_schema": nativeObject{"name": "synthetic_nested", "strict": true, "schema": nativeNestedSchema()}}
		} else {
			body["response_format"] = format
		}
	case "openai-responses":
		body["text"] = nativeObject{"format": format}
	case "native-messages":
		body["output_config"] = nativeObject{"format": nativeObject{"type": "json_schema", "schema": nativeNestedSchema()}}
	case "native-generate-content":
		config := nativeObject{"responseMimeType": "application/json"}
		if strict {
			config["responseJsonSchema"] = nativeNestedSchema()
		}
		body["generationConfig"] = config
	}
}
func (n *nativeScene) output(ctx context.Context) {
	if n.protocol != "native-messages" {
		body := baseNative(n.protocol, n.s.Target.Model, structuredPrompt)
		nativeFormat(n.protocol, body, false)
		out := n.infer(ctx, body, false, true)
		label := "json_object"
		if n.protocol == "native-generate-content" {
			label = "responseMimeType"
		}
		n.check(label, out, structuredMatches(out))
	}
	body := baseNative(n.protocol, n.s.Target.Model, structuredPrompt)
	nativeFormat(n.protocol, body, true)
	strict := n.infer(ctx, body, false, true)
	strictLabel := "json_schema_strict"
	if n.protocol == "native-messages" {
		strictLabel = "output_config.format"
	} else if n.protocol == "native-generate-content" {
		strictLabel = "responseJsonSchema"
	}
	n.check(strictLabel, strict, structuredMatches(strict))
	n.check("schema_nested_enum_required", strict, structuredMatches(strict))
	if n.protocol != "native-messages" {
		limited := baseNative(n.protocol, n.s.Target.Model, "Write the integers from 1 to 10000, separated by spaces. Continue until your output token limit. No tools and no explanation.")
		outputLimit(n.protocol, limited, 16)
		out := n.infer(ctx, limited, false, true)
		label := "max_completion_tokens"
		if n.protocol == "openai-responses" {
			label = "max_output_tokens"
		} else if n.protocol == "native-generate-content" {
			label = "maxOutputTokens"
		}
		n.check(label, out, out.Reply.Structure)
		if out.State == "pass" && nativeEndedNormally(out.Reply) {
			n.uncertain("controlled_length_termination", out)
		} else {
			n.check("controlled_length_termination", out, nativeEndedForLength(out.Reply))
		}
	}
	if n.protocol != "openai-responses" {
		body := baseNative(n.protocol, n.s.Target.Model, "Copy exactly the following text, without quotes or explanation: ALPHA_STOP_OMEGA")
		label := "stop"
		switch n.protocol {
		case "openai-chat-completions":
			body["stop"] = []any{"_STOP_"}
		case "native-messages":
			body["stop_sequences"] = []any{"_STOP_"}
			label = "stop_sequences"
		case "native-generate-content":
			body["generationConfig"] = nativeObject{"stopSequences": []any{"_STOP_"}}
			label = "stopSequences"
		}
		out := n.infer(ctx, body, false, true)
		valid := contentMatches(out, "ALPHA")
		if n.protocol == "native-messages" {
			valid = valid && len(out.Reply.Candidates) == 1 && out.Reply.Candidates[0].End == "stop_sequence" && str(out.Reply.Object["stop_sequence"]) == "_STOP_"
		}
		n.check(label, out, valid)
	}
	var parameters []string
	switch n.protocol {
	case "openai-chat-completions":
		parameters = []string{"temperature", "top_p", "reasoning_effort"}
	case "openai-responses":
		parameters = []string{"reasoning.effort", "text.verbosity"}
	case "native-generate-content":
		parameters = []string{"temperature", "topP", "topK"}
	}
	// Each independently routed parameter has its own assertion. Finite fixed
	// representatives replace a Cartesian product and make unsupported effort
	// independent from otherwise supported sampling controls.
	for _, parameter := range parameters {
		body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
		switch n.protocol {
		case "openai-chat-completions":
			switch parameter {
			case "temperature":
				body[parameter] = .2
			case "top_p":
				body[parameter] = .8
			case "reasoning_effort":
				body[parameter] = "low"
			}
		case "openai-responses":
			if parameter == "reasoning.effort" {
				body["reasoning"] = nativeObject{"effort": "low"}
			} else {
				body["text"] = nativeObject{"verbosity": "low"}
			}
		case "native-generate-content":
			config := nativeObject{}
			switch parameter {
			case "temperature":
				config[parameter] = .2
			case "topP":
				config[parameter] = .8
			case "topK":
				config[parameter] = 20
			}
			body["generationConfig"] = config
		}
		out := n.infer(ctx, body, false, true)
		n.check(parameter, out, contentMatches(out, nativeMarker))
	}
}
