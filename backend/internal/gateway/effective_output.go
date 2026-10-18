package gateway

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// Metrics inspect only declared output locations. Neither a heartbeat nor a
// metadata string mentioning "delta" can establish first effective output.
// This uses the observer's bounded raw-member scratch, never a per-frame map.
func outputTagIs(raw []byte, value string) bool {
	if len(raw) == len(value)+2 && raw[0] == '"' && raw[len(raw)-1] == '"' && string(raw[1:len(raw)-1]) == value {
		return true
	}
	if bytes.IndexByte(raw, '\\') >= 0 {
		return textValue(raw) == value
	}
	return false
}

func nonemptyOutputString(raw []byte) bool {
	return len(raw) > 2 && raw[0] == '"' && raw[len(raw)-1] == '"'
}

func eachOutputElement(raw []byte, visit func([]byte)) {
	at := skipSpace(raw, 0)
	if at >= len(raw) || raw[at] != '[' {
		return
	}
	at = skipSpace(raw, at+1)
	for n := 0; n < maxBillingItems && at < len(raw) && raw[at] != ']'; n++ {
		end, err := skipValue(raw, at)
		if err != nil {
			return
		}
		visit(raw[at:end])
		at = skipSpace(raw, end)
		if at < len(raw) && raw[at] == ',' {
			at = skipSpace(raw, at+1)
		}
	}
}

const (
	outputText uint8 = 1 << iota
	outputTool
	outputReasoning
	outputOpaque
	outputMedia
)

func (o *Observer) markOutput(kind string) string {
	switch kind {
	case "text":
		o.frameOutputMask |= outputText
	case "tool":
		o.frameOutputMask |= outputTool
	case "reasoning":
		o.frameOutputMask |= outputReasoning
	case "opaque":
		o.frameOutputMask |= outputOpaque
	case "audio", "image":
		o.frameOutputMask |= outputMedia
	}
	return kind
}

func (o *Observer) outputPart(raw []byte) string {
	return o.outputPartAt(raw, 0)
}

func (o *Observer) outputPartAt(raw []byte, depth int) string {
	if depth > 2 {
		return ""
	}
	part := o.objectOf(raw)
	switch tag := part.get("type"); {
	case outputTagIs(tag, "output_text") || outputTagIs(tag, "text"):
		if nonemptyOutputString(part.get("text")) {
			return o.markOutput("text")
		}
	case outputTagIs(tag, "thinking"):
		if nonemptyOutputString(part.get("thinking")) {
			return o.markOutput("reasoning")
		}
	case outputTagIs(tag, "redacted_thinking"):
		if nonemptyOutputString(part.get("data")) {
			return o.markOutput("opaque")
		}
	case outputTagIs(tag, "reasoning"):
		if nonemptyOutputString(part.get("encrypted_content")) {
			return o.markOutput("opaque")
		}
	case outputTagIs(tag, "compaction") || outputTagIs(tag, "context_compaction"):
		if nonemptyOutputString(part.get("encrypted_content")) {
			return o.markOutput("opaque")
		}
	case outputTagIs(tag, "function_call") || outputTagIs(tag, "custom_tool_call") || outputTagIs(tag, "tool_use"):
		if nonemptyOutputString(part.get("name")) || nonemptyOutputString(part.get("arguments")) {
			return o.markOutput("tool")
		}
	case outputTagIs(tag, "message"):
		kind := ""
		eachOutputElement(part.get("content"), func(v []byte) {
			if k := o.outputPartAt(v, depth+1); kind == "" {
				kind = k
			}
		})
		return kind
	}
	return ""
}

func (o *Observer) chatOutput(root jsonObject) string {
	kind := ""
	choices := 0
	eachOutputElement(root.get("choices"), func(raw []byte) {
		choices++
		choice := o.objectOf(raw)
		if index, ok := count(choice.get("index")); ok && index > 0 {
			o.multipleOutputs = true
		}
		value := choice.get("delta")
		if len(value) == 0 {
			value = choice.get("message")
		}
		delta := o.objectOf(value)
		current := ""
		if nonemptyOutputString(delta.get("content")) {
			current = "text"
		}
		if nonemptyOutputString(delta.get("reasoning_content")) || nonemptyOutputString(delta.get("reasoning")) {
			o.markOutput("reasoning")
			if current == "" {
				current = "reasoning"
			}
		}
		if current == "" {
			eachOutputElement(delta.get("content"), func(v []byte) {
				if k := o.outputPart(v); current == "" {
					current = k
				}
			})
		}
		if current == "" {
			eachOutputElement(delta.get("tool_calls"), func(v []byte) {
				tool := o.objectOf(v)
				function := o.objectOf(tool.get("function"))
				if nonemptyOutputString(function.get("name")) || nonemptyOutputString(function.get("arguments")) {
					current = "tool"
				}
			})
		}
		if nonemptyOutputString(o.objectOf(delta.get("audio")).get("data")) {
			o.markOutput("audio")
			if current == "" {
				current = "audio"
			}
		}
		o.markOutput(current)
		if kind == "" {
			kind = current
		}
	})
	if choices > 1 {
		o.multipleOutputs = true
	}
	return kind
}

func (o *Observer) responseOutput(root jsonObject) string {
	switch tag := root.get("type"); {
	case outputTagIs(tag, "response.output_text.delta") || outputTagIs(tag, "response.refusal.delta"):
		if nonemptyOutputString(root.get("delta")) {
			return o.markOutput("text")
		}
	case outputTagIs(tag, "response.output_text.done") || outputTagIs(tag, "response.refusal.done"):
		if nonemptyOutputString(root.get("text")) {
			return o.markOutput("text")
		}
	case outputTagIs(tag, "response.reasoning_text.delta") || outputTagIs(tag, "response.reasoning_summary_text.delta"):
		if nonemptyOutputString(root.get("delta")) {
			return o.markOutput("reasoning")
		}
	case outputTagIs(tag, "response.reasoning_text.done") || outputTagIs(tag, "response.reasoning_summary_text.done"):
		if nonemptyOutputString(root.get("text")) {
			return o.markOutput("reasoning")
		}
	case outputTagIs(tag, "response.function_call_arguments.delta") || outputTagIs(tag, "response.custom_tool_call_input.delta"):
		if nonemptyOutputString(root.get("delta")) {
			return o.markOutput("tool")
		}
	case outputTagIs(tag, "response.audio.delta") || outputTagIs(tag, "response.output_audio.delta"):
		if nonemptyOutputString(root.get("delta")) {
			return o.markOutput("audio")
		}
	case outputTagIs(tag, "response.image_generation_call.partial_image"):
		if nonemptyOutputString(root.get("partial_image_b64")) {
			return o.markOutput("image")
		}
	case outputTagIs(tag, "response.output_item.added") || outputTagIs(tag, "response.output_item.done"):
		return o.outputPart(root.get("item"))
	case outputTagIs(tag, "response.content_part.added") || outputTagIs(tag, "response.content_part.done"):
		return o.outputPart(root.get("part"))
	case outputTagIs(tag, "response.completed") || outputTagIs(tag, "response.incomplete"):
		return o.responseItemsOutput(o.objectOf(root.get("response")))
	}
	if !outputTagIs(root.get("object"), "response") && !outputTagIs(root.get("object"), "response.compaction") {
		return ""
	}
	return o.responseItemsOutput(root)
}

func (o *Observer) responseItemsOutput(root jsonObject) string {
	kind := ""
	eachOutputElement(root.get("output"), func(raw []byte) {
		if k := o.outputPart(raw); kind == "" {
			kind = k
		}
	})
	return kind
}

func (o *Observer) anthropicOutput(root jsonObject) string {
	switch tag := root.get("type"); {
	case outputTagIs(tag, "content_block_delta"):
		delta := o.objectOf(root.get("delta"))
		switch tag := delta.get("type"); {
		case outputTagIs(tag, "text_delta"):
			if nonemptyOutputString(delta.get("text")) {
				return o.markOutput("text")
			}
		case outputTagIs(tag, "thinking_delta"):
			if nonemptyOutputString(delta.get("thinking")) {
				return o.markOutput("reasoning")
			}
		case outputTagIs(tag, "input_json_delta"):
			if nonemptyOutputString(delta.get("partial_json")) {
				return o.markOutput("tool")
			}
		case outputTagIs(tag, "signature_delta"):
			if nonemptyOutputString(delta.get("signature")) {
				return o.markOutput("opaque")
			}
		}
	case outputTagIs(tag, "content_block_start"):
		return o.outputPart(root.get("content_block"))
	case outputTagIs(tag, "message"):
		kind := ""
		eachOutputElement(root.get("content"), func(raw []byte) {
			if k := o.outputPart(raw); kind == "" {
				kind = k
			}
		})
		return kind
	}
	return ""
}

func (o *Observer) geminiOutput(root jsonObject) string {
	kind := ""
	candidates := 0
	eachOutputElement(root.get("candidates"), func(raw []byte) {
		candidates++
		candidate := o.objectOf(raw)
		if index, ok := count(candidate.get("index")); ok && index > 0 {
			o.multipleOutputs = true
		}
		content := o.objectOf(candidate.get("content"))
		eachOutputElement(content.get("parts"), func(raw []byte) {
			part := o.objectOf(raw)
			current := ""
			if nonemptyOutputString(part.get("text")) {
				current = "text"
				if string(part.get("thought")) == "true" {
					current = "reasoning"
				}
			}
			if current == "" && nonemptyOutputString(o.objectOf(part.get("functionCall")).get("name")) {
				current = "tool"
			}
			if current == "" && nonemptyOutputString(o.objectOf(part.get("executableCode")).get("code")) {
				current = "tool"
			}
			if current == "" {
				inline := o.objectOf(part.get("inlineData"))
				if nonemptyOutputString(inline.get("data")) {
					switch mime := textValue(inline.get("mimeType")); {
					case len(mime) >= 6 && mime[:6] == "image/":
						current = "image"
					case len(mime) >= 6 && mime[:6] == "audio/":
						current = "audio"
					}
				}
			}
			o.markOutput(current)
			if kind == "" {
				kind = current
			}
		})
	})
	if candidates > 1 {
		o.multipleOutputs = true
	}
	return kind
}

func (o *Observer) trackEffectiveOutput(raw []byte, at time.Time) {
	o.members = o.members[:0]
	o.frameOutputMask = 0
	root := o.objectOf(raw)
	kind := ""
	if o.format == channel.FormatAnthropic && outputTagIs(root.get("type"), "message_stop") && o.outputEndAt.IsZero() && json.Valid(raw) {
		o.outputEndAt = at
	}
	switch o.format {
	case channel.FormatOpenAIChat:
		kind = o.chatOutput(root)
	case channel.FormatOpenAIResponses:
		kind = o.responseOutput(root)
	case channel.FormatAnthropic:
		kind = o.anthropicOutput(root)
	case channel.FormatGemini:
		kind = o.geminiOutput(root)
	case channel.FormatOpenAIImages:
		eachOutputElement(root.get("data"), func(raw []byte) {
			item := o.objectOf(raw)
			if nonemptyOutputString(item.get("b64_json")) || nonemptyOutputString(item.get("url")) {
				kind = "image"
			}
		})
		if nonemptyOutputString(root.get("b64_json")) || nonemptyOutputString(root.get("partial_image_b64")) {
			kind = "image"
		}
	}
	o.markOutput(kind)
	if kind != "" && json.Valid(raw) {
		if o.firstOutputAt.IsZero() {
			o.firstOutputAt = at
			o.outputKind = kind
		}
		if o.frameOutputMask&(outputText|outputTool) != 0 && o.firstVisibleOutputAt.IsZero() {
			o.firstVisibleOutputAt = at
		}
		o.reasoningOutput = o.reasoningOutput || o.frameOutputMask&outputReasoning != 0
		o.opaqueOutput = o.opaqueOutput || o.frameOutputMask&outputOpaque != 0
		o.mediaOutput = o.mediaOutput || o.frameOutputMask&outputMedia != 0
		o.lastOutputAt = at
	}
	o.members = o.members[:0]
}
