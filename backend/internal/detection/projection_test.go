package detection

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func projectionFixture() Job {
	params := testParameters()
	report := "report-synthetic"
	item := Planned{ID: "protocol.code@upstream_protocol", SuiteID: "protocol.group_model", Requirements: []string{"protocol.code"}, Assertions: []string{"protocol.code/a1", "protocol.code/a2"}, AssertionLabels: map[string]string{"protocol.code/a1": "execution", "protocol.code/a2": "receipt"}, Implemented: true, Layer: "upstream_protocol"}
	binding := market.EvidenceBinding{TargetKind: "group_model", TargetID: "target", TargetRevision: 1, CapabilityDigest: "engine-inputs", ManifestVersion: "manifest", CodeVersion: "registry", ParameterVersion: parametersVersion(params), CredentialScope: "test-key-range"}
	j := Job{ReportID: &report, Target: Target{Binding: binding}, Parameters: params, State: "finished", Plan: Plan{ManifestVersion: "manifest", RegistryVersion: "registry", Items: []Planned{item}}}
	j.Results = []Result{{ScenarioID: item.ID, State: "partial", Reason: "not_observed", Source: "real_upstream", Layer: "upstream_protocol", Assertions: []Assertion{{ID: item.Assertions[0], State: "pass", Reason: "ok", AttemptIDs: []string{"actual-request"}}, {ID: item.Assertions[1], State: "inconclusive", Reason: "not_observed", AttemptIDs: []string{"actual-request"}}}, Attempts: []Attempt{{ID: "actual-request", Sent: true, ResponseReceived: true, Status: 200}}}}
	return j
}
func TestProjectionKeepsIndependentCapabilitiesAndRejectsStaleOrSyntheticProof(t *testing.T) {
	j := projectionFixture()
	current := j.Target.Binding
	current.TargetRevision = 2
	p := Project(j, current)
	if !p.Valid || !p.Supports("upstream_protocol", []string{"protocol.code/execution"}) || p.Supports("upstream_protocol", []string{"protocol.code/receipt"}) || p.Supports("gateway_proxy", []string{"protocol.code/execution"}) || p.Supports("upstream_protocol", []string{"unknown"}) {
		t.Fatalf("independent proof lost: %+v", p)
	}
	for _, change := range []func(*Job, *market.EvidenceBinding){
		func(j *Job, _ *market.EvidenceBinding) { j.Results[0].Source = "synthetic" },
		func(j *Job, _ *market.EvidenceBinding) { j.ReportID = nil },
		func(j *Job, _ *market.EvidenceBinding) { j.State = "interrupted" },
		func(j *Job, _ *market.EvidenceBinding) { j.ExecutionActive = true },
		func(_ *Job, c *market.EvidenceBinding) { c.CapabilityDigest = "changed" },
		func(_ *Job, c *market.EvidenceBinding) { c.ManifestVersion = "changed" },
		func(_ *Job, c *market.EvidenceBinding) { c.CodeVersion = "changed" },
		func(_ *Job, c *market.EvidenceBinding) { c.ParameterVersion = "changed" },
		func(_ *Job, c *market.EvidenceBinding) { c.CredentialScope = "foreign-key" },
		func(j *Job, _ *market.EvidenceBinding) { j.Results[0].Attempts[0].ResponseReceived = false },
	} {
		j = projectionFixture()
		current = j.Target.Binding
		change(&j, &current)
		if Project(j, current).Valid {
			t.Fatal("invalid proof entered routing snapshot")
		}
	}
}
func TestProjectionNeverUsesDeclaredOrAcceptedCapacityAsVerifiedLimit(t *testing.T) {
	j := projectionFixture()
	declared, accepted, actual := int64(1_000_000), int64(999_999), int64(120)
	c := CapacityReport{Kind: "context", Unit: "tokens", Settings: j.Parameters.Context, Declared: &declared, ParameterAcceptedLower: &accepted, Completed: true, Stop: "rounds", Steps: []CapacityStep{}}
	j.Results[0].Capacity = []CapacityReport{c}
	p := Project(j, j.Target.Binding)
	if !p.Valid || len(p.Capacity) != 0 {
		t.Fatal("declaration or accepted parameter became verified capacity")
	}
	j.Results[0].Capacity[0].VerifiedLower = &actual
	p = Project(j, j.Target.Binding)
	if len(p.Capacity) != 0 {
		t.Fatal("unwitnessed lower bound accepted")
	}
	j.Results[0].Capacity[0].Steps = []CapacityStep{{Requested: j.Parameters.Context.Low, Round: 1, CapacityProbe: CapacityProbe{Accepted: true, Semantic: true, Actual: &actual, Reason: "ok", AttemptIDs: []string{"actual-request"}}}}
	p = Project(j, j.Target.Binding)
	if !p.Valid || len(p.Capacity) != 1 || p.Capacity[0].VerifiedLower != 120 {
		t.Fatalf("exact observed capacity missing: %+v", p)
	}
	j.Results[0].Capacity[0].Steps[0].Approximate = true
	p = Project(j, j.Target.Binding)
	if len(p.Capacity) != 0 {
		t.Fatal("approximate sample became hard capacity")
	}
}
