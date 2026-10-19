package detection

// Reserved context tools must retain their pinned provider schema, including
// optional nullable filters. The pinned JsonSchema serialization drops minimum
// (it is not a supported struct field); copy the wire schema, not raw source.
// Source: Codex 83086188, ext/history-notes/src/tools.rs
// (Apache-2.0; license and notice in backend/licenses/codex).
func codexContextSearchSchema(namespace string) nativeObject {
	if namespace == "history" {
		return nativeObject{"type": "object", "properties": nativeObject{
			"limit":          nativeObject{"type": "integer", "description": "Maximum number of matching items to return."},
			"query":          nativeObject{"type": "string", "encrypted": true, "description": "Case-sensitive literal substring to find in item content."},
			"recent_first":   nativeObject{"type": "boolean", "description": "Whether to return the most recently created matches first."},
			"tool_namespace": nativeObject{"anyOf": []any{nativeObject{"type": "string"}, nativeObject{"type": "null"}}, "description": "Callable namespace to include. When set, non-tool messages are excluded."},
			"role":           nativeObject{"anyOf": []any{nativeObject{"type": "string", "enum": []any{"user", "assistant", "tool", "system", "developer"}}, nativeObject{"type": "null"}}, "description": "Message role to include. Null or omission includes all roles."},
			"agent_name":     nativeObject{"anyOf": []any{nativeObject{"type": "string"}, nativeObject{"type": "null"}}, "description": "Agent whose history to inspect. Omit to use the current agent; otherwise pass an absolute agent name or a name relative to the current agent."},
			"tool_name":      nativeObject{"anyOf": []any{nativeObject{"type": "string"}, nativeObject{"type": "null"}}, "description": "Callable tool name to include. When set, non-tool messages are excluded."},
			"window_id":      nativeObject{"anyOf": []any{nativeObject{"type": "string"}, nativeObject{"type": "null"}}, "description": "Full window ID. Null or omission includes all windows."},
		}, "required": []any{"query"}}
	}
	return nativeObject{"type": "object", "properties": nativeObject{
		"max_matches_per_file": nativeObject{"type": "integer", "description": "Maximum number of matching lines returned per file."},
		"query":                nativeObject{"type": "string", "encrypted": true, "description": "Case-sensitive literal substring to find in note lines."},
		"recent_file_first":    nativeObject{"type": "boolean", "description": "Whether to order matching files by creation time, newest first."},
		"max_files":            nativeObject{"type": "integer", "description": "Maximum number of matching files returned."},
		"path_prefix":          nativeObject{"anyOf": []any{nativeObject{"type": "string"}, nativeObject{"type": "null"}}, "description": "Note path prefix to search."},
	}, "required": []any{"query"}}
}

func codexContextOptionalArgument(name string, value any, properties nativeObject) bool {
	property := object(properties[name])
	if property == nil {
		return false
	}
	if value == nil {
		for _, candidate := range list(property["anyOf"]) {
			if str(object(candidate)["type"]) == "null" {
				return true
			}
		}
		return false
	}
	switch name {
	case "limit", "max_files", "max_matches_per_file":
		n, valid := fieldInt(nativeObject{name: value}, name)
		return valid && n >= 1 && n <= 8
	case "recent_first", "recent_file_first":
		_, valid := value.(bool)
		return valid
	case "agent_name":
		return value == "/root"
	case "role":
		return value == "user"
	default:
		return false // Never let a generated filter broaden the owned seed scope.
	}
}
