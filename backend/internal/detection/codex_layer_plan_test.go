package detection

import (
	"slices"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestFixedCodexV1HasNoInventedCLIRequirementAndAggregateKeepsV2Dependencies(t *testing.T) {
	r, _, _ := codexTestSession(t, "https://example.com")
	p, err := r.Plan(Target{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "synthetic"}, Model: "synthetic-model", ModelType: "text"})
	if err != nil {
		t.Fatal(err)
	}
	v1, cli := []string{}, false
	for _, item := range p.Items {
		if slices.Contains(item.Requirements, "codex-special.RL20") {
			v1 = append(v1, item.Layer)
		}
		if slices.Contains(item.Requirements, "codex-special.RL10") && item.Layer == "official_cli" {
			cli = true
			if len(item.Dependencies) != 2 {
				t.Fatal("CLI aggregate lost applicable V2/background proof", item)
			}
			for _, id := range item.Dependencies {
				if id != "codex-special.RL21@official_cli" && id != "codex-special.RL22@official_cli" {
					t.Fatal("aggregate borrowed another layer or fabricated CLI V1", item)
				}
			}
		}
	}
	slices.Sort(v1)
	if !cli || !slices.Equal(v1, []string{"gateway_proxy", "upstream_protocol"}) {
		t.Fatal("V1 protocol/gateway coverage changed", v1)
	}
}
