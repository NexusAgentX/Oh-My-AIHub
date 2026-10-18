package gateway

import (
	"encoding/json"
	"strings"
	"unicode"
)

func validCodexAuxiliaryContext(object map[string]json.RawMessage) bool {
	var context struct {
		Session string `json:"session_id"`
		Agent   string `json:"current_agent_name"`
	}
	if json.Unmarshal(object["context"], &context) != nil {
		return false
	}
	return context.Session != "" && len(context.Session) <= 256 && strings.TrimSpace(context.Agent) != "" && len(context.Agent) <= 256 && !strings.ContainsFunc(context.Agent, unicode.IsControl)
}

func codexContextRequirements(id string) []CapabilityRequirement {
	var requirement, operation string
	switch {
	case id == "codex.notes.thread_hint":
		requirement, operation = "RL17", "thread_hint"
	case strings.HasPrefix(id, "codex.notes."):
		requirement, operation = "RL18", strings.TrimPrefix(id, "codex.notes.")
	case strings.HasPrefix(id, "codex.history."):
		requirement, operation = "RL19", strings.TrimPrefix(id, "codex.history.")
	default:
		return nil
	}
	return []CapabilityRequirement{{Layer: "upstream_protocol", ID: "codex-special." + requirement + "/" + operation}, {Layer: "upstream_protocol", ID: "codex-special." + requirement + "/context_isolation"}}
}
