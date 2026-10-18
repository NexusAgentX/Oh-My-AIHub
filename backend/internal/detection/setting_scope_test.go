package detection

import (
	"slices"
	"testing"
)

func TestTierRoutingProofCannotBorrowAnotherSurfaceOrLayer(t *testing.T) {
	base := SettingEvidence{ScenarioID: "openai-responses.cache_tier@upstream_protocol", Layer: "upstream_protocol", Kind: "service_tier", Configured: "openai:fast", Actual: "openai:fast", State: "pass", Accepted: true, UsageReliable: true}
	if base.RoutingCapability() != "service_tier/openai:fast" {
		t.Fatal("public tier receipt lost")
	}
	base.ScenarioID = "codex-special.RL24@upstream_protocol"
	if base.RoutingCapability() != "codex-special.RL24/service_tier/openai:fast" {
		t.Fatal("native Codex tier was not scoped to its actual surface")
	}
	for _, mutate := range []func(*SettingEvidence){
		func(e *SettingEvidence) { e.Layer = "gateway_proxy" },
		func(e *SettingEvidence) { e.Layer = "official_cli" },
		func(e *SettingEvidence) { e.ScenarioID = "codex-special.RL15@upstream_protocol" },
		func(e *SettingEvidence) { e.Actual = "openai:default" },
		func(e *SettingEvidence) { e.UsageReliable = false },
		func(e *SettingEvidence) { e.State = "partial" },
	} {
		e := base
		mutate(&e)
		if e.RoutingCapability() != "" {
			t.Fatal("unverified/wrong-layer native tier authorized routing", e)
		}
	}
}

func TestCodexTierProfileChangesRequireAllThreeLayersAgain(t *testing.T) {
	p := Plan{}
	for _, layer := range []string{"upstream_protocol", "gateway_proxy", "official_cli"} {
		p.Items = append(p.Items, Planned{ID: "codex-special.RL24@" + layer, Requirements: []string{"codex-special.RL24"}})
	}
	p.Items = append(p.Items, Planned{ID: "unrelated", Requirements: []string{"codex-special.RL06"}})
	ids := profileScenarios(p)
	if len(ids) != 3 || slices.Contains(ids, "unrelated") {
		t.Fatal("new tier reused stale Codex evidence or repeated unrelated scenes", ids)
	}
}
