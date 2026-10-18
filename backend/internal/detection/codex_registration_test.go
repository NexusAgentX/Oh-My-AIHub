package detection

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestAllApplicableCodexBusinessSuitesRegisterWithFixedArtifacts(t *testing.T) {
	binary, probe := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || probe == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	r, _, _ := codexTestSession(t, "https://synthetic.example")
	if err := r.RegisterCodexGateway(); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterCodexCLI(context.Background(), probe, binary); err != nil {
		t.Fatal(err)
	}
	for _, target := range []Target{
		{Binding: market.EvidenceBinding{TargetKind: "group", TargetID: "synthetic-group"}},
		{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "synthetic-text"}, Model: "synthetic-text", ModelType: "text"},
		{Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "synthetic-image"}, Model: "synthetic-image", ModelType: "image"},
	} {
		plan, err := r.Plan(target)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, item := range plan.Items {
			if !strings.HasPrefix(item.SuiteID, "codex-special.") {
				continue
			}
			found++
			if !item.Implemented || item.CodeVersion == "" || len(item.Assertions) == 0 {
				t.Fatal("Codex creation suite is missing or empty", target.ModelType, item.ID)
			}
			for _, req := range item.Requirements {
				if req == "codex-special.RL23" || req == "codex-special.RL26" || req == "codex-special.RL11" {
					t.Fatal("platform regression or excluded WS entered supplier creation", item.ID)
				}
			}
		}
		if found == 0 {
			t.Fatal("Codex business scope disappeared", target.ModelType)
		}
	}
}
