package detection

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func layeredManifest(t *testing.T) (gatewaycontract.Manifest, *gatewaycontract.Requirement) {
	t.Helper()
	m, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for si := range m.Suites {
		for ri := range m.Suites[si].Requirements {
			r := &m.Suites[si].Requirements[ri]
			if r.ID == "codex-special.RL13" {
				r.Assertions = []string{"upstream_auth", "platform_auth", "cli_activation"}
				r.AssertionLayers = map[string][]string{AssertionID(r.ID, 0): {"upstream_protocol"}, AssertionID(r.ID, 1): {"gateway_proxy"}, AssertionID(r.ID, 2): {"official_cli"}}
				return m, r
			}
		}
	}
	t.Fatal("missing fixed Codex prerequisite")
	return m, nil
}

func TestProofLayersKeepTheirOwnAssertionsAndOriginalIDs(t *testing.T) {
	m, req := layeredManifest(t)
	r, err := NewRegistry(m)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Plan(Target{Binding: market.EvidenceBinding{TargetKind: "group", TargetID: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"upstream_protocol": AssertionID(req.ID, 0), "gateway_proxy": AssertionID(req.ID, 1), "official_cli": AssertionID(req.ID, 2)}
	found := 0
	for _, item := range p.Items {
		if len(item.Requirements) != 1 || item.Requirements[0] != req.ID {
			continue
		}
		found++
		if len(item.Assertions) != 1 || item.Assertions[0] != want[item.Layer] || len(item.AssertionLabels) != 1 || item.Implemented {
			t.Fatalf("layer inherited foreign assertions or lost pending status: %+v", item)
		}
	}
	if found != 3 {
		t.Fatal("required proof layer disappeared", found)
	}
}

func TestAssertionLayerRestrictionsCannotEraseCoverage(t *testing.T) {
	for _, change := range []func(*gatewaycontract.Requirement){
		func(r *gatewaycontract.Requirement) { r.AssertionLayers["foreign/a1"] = []string{"official_cli"} },
		func(r *gatewaycontract.Requirement) { r.AssertionLayers[AssertionID(r.ID, 0)] = []string{"unknown"} },
		func(r *gatewaycontract.Requirement) {
			r.AssertionLayers[AssertionID(r.ID, 0)] = []string{"upstream_protocol", "upstream_protocol"}
		},
		func(r *gatewaycontract.Requirement) {
			for id := range r.AssertionLayers {
				r.AssertionLayers[id] = []string{"upstream_protocol"}
			}
		},
	} {
		m, req := layeredManifest(t)
		change(req)
		if m.Validate() == nil {
			t.Fatal("invalid or erased proof layer accepted")
		}
	}
}

func TestProjectionPreservesNonconsecutiveLayerAssertionID(t *testing.T) {
	j := projectionFixture()
	item := &j.Plan.Items[0]
	item.Layer = "official_cli"
	item.Assertions = []string{"protocol.code/a3"}
	item.AssertionLabels = map[string]string{"protocol.code/a3": "cli_activation"}
	r := &j.Results[0]
	r.Layer = "official_cli"
	r.State = "pass"
	r.Reason = "ok"
	r.Assertions = []Assertion{{ID: "protocol.code/a3", State: "pass", Reason: "ok", AttemptIDs: []string{"actual-request"}}}
	p := Project(j, j.Target.Binding)
	if !p.Valid || !p.Supports("official_cli", []string{"protocol.code/cli_activation"}) || p.Supports("upstream_protocol", []string{"protocol.code/cli_activation"}) {
		t.Fatalf("nonconsecutive proof ID was lost or changed layer: %+v", p)
	}
}

func TestClientAndPlatformExecutionFailuresCannotCompleteCreationReport(t *testing.T) {
	for _, reason := range []string{"detector_error", "client_disabled", "platform_unavailable", "suite_pending", "cancelled", "interrupted", "invalid_request"} {
		for _, assertionOnly := range []bool{false, true} {
			j := projectionFixture()
			if !Complete(j) {
				t.Fatal("invalid base fixture")
			}
			if assertionOnly {
				j.Results[0].Assertions[0].State, j.Results[0].Assertions[0].Reason = "inconclusive", reason
			} else {
				j.Results[0].State, j.Results[0].Reason = "inconclusive", reason
			}
			if Complete(j) || Project(j, j.Target.Binding).Valid {
				t.Fatal("unexecuted client/platform became complete supplier evidence", reason, assertionOnly)
			}
		}
	}
}
