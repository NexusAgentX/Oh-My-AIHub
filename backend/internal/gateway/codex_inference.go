package gateway

import (
	"bytes"
	"strings"
)

var codexCompactionResume = CapabilityRequirement{Layer: "upstream_protocol", ID: "codex-special.RL20/compaction_continuation", Alternative: "codex-special.RL21/compaction_continuation"}

// Only the registered Codex request surface uses these extension requirements.
// Plain inference keeps its existing scan and capability rules.
func codexInferenceRequirements(path string, body []byte, encoding string) []CapabilityRequirement {
	if !strings.HasPrefix(path, "/backend-api/codex/") {
		return nil
	}
	result := []CapabilityRequirement{}
	add := func(rl, operation string) {
		id := "codex-special." + rl + "/" + operation
		for _, r := range result {
			if r.ID == id {
				return
			}
		}
		result = append(result, CapabilityRequirement{Layer: "upstream_protocol", ID: id})
	}
	switch path {
	case "/backend-api/codex/images/generations":
		add("RL25", "native_generation")
		return result
	case "/backend-api/codex/images/edits":
		add("RL25", "native_edit")
		return result
	case "/backend-api/codex/responses/compact":
		add("RL20", "native_compact")
		return result
	case "/backend-api/codex/alpha/search":
		add("RL16", "native_search")
		return result
	case "/backend-api/codex/responses":
		if encoding == "" {
			add("RL14", "uncompressed_request")
		}
	default:
		return nil
	}
	var scratch Observer
	root := scratch.objectOf(body)
	input := root.get("input")
	tools := root.get("tools")
	reasoning := root.get("reasoning")
	if bytes.Equal(bytes.TrimSpace(root.get("parallel_tool_calls")), []byte("false")) {
		add("RL04", "parallel_calls_disabled")
	}
	mark := len(scratch.members)
	if r := scratch.objectOf(reasoning); textValue(r.get("context")) == "all_turns" {
		add("RL05", "multi_turn_continuation")
	}
	scratch.members = scratch.members[:mark]
	tool := func(raw []byte) {
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		o := scratch.objectOf(raw)
		switch textValue(o.get("type")) {
		case "function":
			add("RL03", "standard_tools_roundtrip")
		case "namespace":
			add("RL07", "namespace_tools_roundtrip")
		case "custom":
			add("RL07", "custom_tools_roundtrip")
		}
	}
	eachElement(tools, tool)
	eachElement(input, func(raw []byte) {
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		o := scratch.objectOf(raw)
		switch textValue(o.get("type")) {
		case "additional_tools":
			add("RL02", "additional_tools_declaration")
			eachElement(o.get("tools"), tool)
		case "compaction_trigger":
			add("RL21", "compaction_trigger")
		case "context_compaction":
			add("RL21", "compaction_continuation")
		case "compaction":
			found := false
			for _, required := range result {
				found = found || required == codexCompactionResume
			}
			if !found {
				result = append(result, codexCompactionResume)
			}
		case "reasoning":
			if len(o.get("encrypted_content")) > 0 {
				add("RL09", "encrypted_reasoning_continued")
			}
		}
		if textValue(o.get("role")) == "developer" {
			add("RL06", "developer_instruction")
		}
	})
	return result
}
