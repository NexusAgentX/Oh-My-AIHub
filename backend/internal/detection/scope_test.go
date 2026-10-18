package detection

import (
	"testing"
	"testing/fstest"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func TestRawDetectorVersionIgnoresCLIOnlyChangesAndRejectsUnknownDependencies(t *testing.T) {
	source := fstest.MapFS{"native.go": {Data: []byte("raw-v1")}, "codex_cli.go": {Data: []byte("cli-v1")}, "codex_bridge.go": {Data: []byte("bridge-v1")}}
	raw, all := implementationDigest(source, "upstream_protocol"), implementationDigest(source, "")
	source["codex_cli.go"] = &fstest.MapFile{Data: []byte("cli-v2")}
	source["codex_bridge.go"] = &fstest.MapFile{Data: []byte("bridge-v2")}
	if implementationDigest(source, "upstream_protocol") != raw || implementationDigest(source, "") == all {
		t.Fatal("CLI change invalidated raw protocol or failed to change full engine")
	}
	source["new_dependency.go"] = &fstest.MapFile{Data: []byte("new unclassified dependency")}
	if implementationDigest(source, "upstream_protocol") == raw {
		t.Fatal("unknown source dependency silently retained proof")
	}
	delete(source, "new_dependency.go")
	source["native.go"] = &fstest.MapFile{Data: []byte("raw-v2")}
	if implementationDigest(source, "upstream_protocol") == raw {
		t.Fatal("actual raw detector change retained proof")
	}
}

func TestScenarioScopeTracksItsOwnEndpointSourceAndAppliedAssertions(t *testing.T) {
	registry, _ := NewRegistry(fixtureManifest("a"))
	item := Planned{Requirements: []string{"a"}, Assertions: []string{"a/a1"}, Layer: "upstream_protocol"}
	scope := func() string { return registry.scopeVersion(registry.manifest.Suites[0], item) }
	original := scope()
	registry.manifest.Version = "unrelated-new-global-version"
	registry.manifest.Sources = append(registry.manifest.Sources, gatewaycontract.Source{ID: "unrelated-cli", Version: "new"})
	registry.manifest.Suites[0].Requirements[0].Assertions[1] = "unrelated_layer_assertion"
	if scope() != original {
		t.Fatal("unrelated manifest/layer changed this assertion's contract")
	}
	for _, change := range []func(){
		func() { registry.manifest.Endpoints[0].Path = "/v2/responses" },
		func() { registry.manifest.Sources[0].Version = "changed-protocol-version" },
		func() { registry.manifest.Suites[0].Requirements[0].Assertions[0] = "changed-native-meaning" },
	} {
		before := scope()
		change()
		if scope() == before {
			t.Fatal("changed endpoint/protocol/assertion retained scope")
		}
	}
}

func TestRoutingProjectionRetainsNativeProofWhenCLIArtifactChanges(t *testing.T) {
	j := projectionFixture()
	j.Plan.Items[0].CodeVersion = "native.v1"
	j.Plan.Items[0].ScopeVersion = "native-scope.v1"
	cli := j.Plan.Items[0]
	cli.ID, cli.Layer, cli.CodeVersion, cli.ScopeVersion = "protocol.code@official_cli", "official_cli", "cli.v1", "cli-scope.v1"
	j.Plan.Items = append(j.Plan.Items, cli)
	result := j.Results[0]
	result.ScenarioID, result.Layer = cli.ID, cli.Layer
	j.Results = append(j.Results, result)
	current := j.Target.Binding
	current.ManifestVersion, current.CodeVersion = "new-global-manifest", "new-global-registry"
	plan := j.Plan
	plan.ManifestVersion, plan.RegistryVersion = current.ManifestVersion, current.CodeVersion
	plan.Items = append([]Planned{}, j.Plan.Items...)
	plan.Items[1].CodeVersion = "cli.v2"
	if !Complete(j) || Project(j, current).Valid {
		t.Fatal("strict creation/global binding gate changed")
	}
	p := ProjectForPlan(j, current, plan)
	if !p.Valid || !p.Supports("upstream_protocol", []string{"protocol.code/execution"}) || p.Supports("official_cli", []string{"protocol.code/execution"}) || p.Supports("upstream_protocol", []string{"protocol.code/receipt"}) {
		t.Fatal("CLI update erased native proof or granted unsupported capability", p)
	}
	baselineCurrent, baselinePlan := current, plan
	for _, change := range []func(){
		func() { plan.Items[0].ScopeVersion = "changed-native-scope" },
		func() { current.CapabilityDigest = "changed-inputs" },
		func() { current.CredentialScope = "foreign-credential" },
	} {
		current, plan = baselineCurrent, baselinePlan
		plan.Items = append([]Planned{}, baselinePlan.Items...)
		change()
		if ProjectForPlan(j, current, plan).Valid {
			t.Fatal("changed native contract/target/credential retained support")
		}
	}
}
