package detection

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"

func codexProbeToolKinds(tools []any, wire *codexWireFacts) {
	for _, raw := range tools {
		tool := object(raw)
		if str(tool["type"]) == "namespace" && str(tool["name"]) == "synthetic" && syntheticToolDeclarations(list(tool["tools"])) > 0 {
			wire.NamespaceDeclarations++
		}
		if str(tool["type"]) == "custom" && str(tool["name"]) == "apply_patch" {
			wire.CustomDeclarations++
		}
	}
}

// The isolated CLI may receive only a canonical empty patch. It can return its
// real no-op diagnostic, but an upstream cannot make this probe modify a file.
func safeCLINoopOutput(root nativeObject) bool {
	if len(list(root["output"])) == 0 {
		return false
	}
	for _, raw := range list(root["output"]) {
		item := object(raw)
		switch str(item["type"]) {
		case "message", "reasoning":
		case "custom_tool_call":
			if str(item["name"]) != "apply_patch" || str(item["input"]) != codexprobe.NoopPatch || str(item["call_id"]) == "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
