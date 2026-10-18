package gateway

import (
	"bytes"
	"strings"
)

// Only the registered Codex request surface uses these extension requirements.
// Plain inference keeps its existing scan and capability rules.
func codexInferenceRequirements(path string, body []byte) []CapabilityRequirement {
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
		case "namespace":
			add("RL07", "namespace_tools_roundtrip")
		case "custom":
			add("RL07", "custom_tools_roundtrip")
		}
	}
	standardTools := false
	eachElement(tools, func(raw []byte) { standardTools = true; tool(raw) })
	if standardTools {
		add("RL03", "standard_tools_roundtrip")
	}
	lite := false
	eachElement(input, func(raw []byte) {
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		o := scratch.objectOf(raw)
		switch textValue(o.get("type")) {
		case "additional_tools":
			lite = true
			add("RL02", "additional_tools_declaration")
			eachElement(o.get("tools"), tool)
		case "compaction_trigger":
			add("RL21", "compaction_trigger")
		case "context_compaction":
			add("RL21", "compaction_continuation")
		case "reasoning":
			if len(o.get("encrypted_content")) > 0 {
				add("RL09", "encrypted_reasoning_continued")
			}
		}
		if textValue(o.get("role")) == "developer" {
			add("RL06", "developer_instruction")
		}
	})
	if lite {
		add("RL12", "lite_native_roundtrip")
	} else {
		add("RL12", "standard_native_roundtrip")
	}
	return result
}
