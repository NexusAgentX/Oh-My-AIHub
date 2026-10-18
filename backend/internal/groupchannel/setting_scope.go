package groupchannel

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"

func scopedSettingCapability(setting detection.SettingEvidence) string {
	capability := setting.RoutingCapability()
	if capability == "" {
		return ""
	}
	if setting.ScenarioID == "codex-special.RL24@upstream_protocol" {
		return capability
	}
	// Chat, public Responses, Messages and Gemini cannot borrow each other's
	// actual-tier observations merely because the canonical tier name matches.
	for _, protocol := range []string{"openai-chat-completions", "openai-responses", "native-messages", "native-generate-content"} {
		if setting.ScenarioID == protocol+".cache_tier@upstream_protocol" {
			return protocol + ".cache_tier/" + capability
		}
	}
	return ""
}
