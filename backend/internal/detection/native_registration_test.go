package detection

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestEveryRetainedF4RequirementHasAnExecutableNativeScenario(t *testing.T) {
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterNative(); err != nil {
		t.Fatal(err)
	}
	f, err := NewMCPFixture("https://gateway.synthetic.example")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterMCP(f); err != nil {
		t.Fatal(err)
	}
	sdk := os.Getenv("AIHUB_TEST_SDK_BINARY")
	if sdk != "" {
		if err = r.RegisterSDK(context.Background(), sdk); err != nil {
			t.Fatal(err)
		}
	}
	suites := map[string]bool{}
	for _, s := range m.Suites {
		if s.Feature == "F4" {
			suites[s.ID] = true
		}
	}
	for _, target := range []struct{ kind, modelType string }{{"group", ""}, {"group_model", "text"}, {"group_model", "image"}} {
		plan, err := r.Plan(Target{Binding: market.EvidenceBinding{TargetKind: target.kind, TargetID: "synthetic-target"}, ModelType: target.modelType})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range plan.Items {
			if !suites[item.SuiteID] {
				continue
			}
			if item.Layer != "upstream_protocol" {
				t.Fatalf("F4 claimed another proof layer: %s", item.ID)
			}
			if !item.Implemented && !(sdk == "" && strings.Contains(item.ID, ".sdk")) {
				t.Fatalf("retained F4 scenario missing: %s", item.ID)
			}
		}
	}
}
