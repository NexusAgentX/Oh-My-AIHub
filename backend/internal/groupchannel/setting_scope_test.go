package groupchannel

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"testing"
)

func TestSettingCapabilityKeepsItsMeasuredProtocol(t *testing.T) {
	e := detection.SettingEvidence{ScenarioID: "openai-chat-completions.cache_tier@upstream_protocol", Layer: "upstream_protocol", Kind: "service_tier", Configured: "openai:fast", Actual: "openai:fast", State: "pass", Accepted: true, UsageReliable: true}
	if scopedSettingCapability(e) != "openai-chat-completions.cache_tier/service_tier/openai:fast" {
		t.Fatal("lost measured Chat protocol")
	}
	e.ScenarioID = "openai-responses.cache_tier@upstream_protocol"
	if scopedSettingCapability(e) != "openai-responses.cache_tier/service_tier/openai:fast" {
		t.Fatal("lost measured Responses protocol")
	}
	e.ScenarioID = "codex-special.RL24@upstream_protocol"
	if scopedSettingCapability(e) != "codex-special.RL24/service_tier/openai:fast" {
		t.Fatal("lost native Codex scope")
	}
	e.Layer = "official_cli"
	if scopedSettingCapability(e) != "" {
		t.Fatal("client proof advertised native tier")
	}
}
