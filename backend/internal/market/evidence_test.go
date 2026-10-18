package market

import "testing"

func TestLateEvidenceCannotAuthorizeChangedInputs(t *testing.T) {
	baseline := EvidenceBinding{TargetKind: "group_model", TargetID: "model-row", TargetRevision: 1, CapabilityDigest: "inputs-v1", ManifestVersion: "scope-v1", CodeVersion: "suite-v1", CredentialScope: "protected-scope", ParameterVersion: "params-v1"}
	current := baseline
	current.TargetRevision = 4
	if !CompatibleBinding(baseline, current) {
		t.Fatal("unchanged inputs after price/address revision cannot reuse evidence")
	}
	for name, mutate := range map[string]func(*EvidenceBinding){
		"inputs":     func(v *EvidenceBinding) { v.CapabilityDigest = "inputs-v2" },
		"target":     func(v *EvidenceBinding) { v.TargetID = "other" },
		"kind":       func(v *EvidenceBinding) { v.TargetKind = "group" },
		"manifest":   func(v *EvidenceBinding) { v.ManifestVersion = "scope-v2" },
		"suite":      func(v *EvidenceBinding) { v.CodeVersion = "suite-v2" },
		"settings":   func(v *EvidenceBinding) { v.ParameterVersion = "params-v2" },
		"credential": func(v *EvidenceBinding) { v.CredentialScope = "other" },
		"future":     func(v *EvidenceBinding) { v.TargetRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			v := current
			mutate(&v)
			if CompatibleBinding(baseline, v) {
				t.Fatal("stale or foreign evidence accepted")
			}
		})
	}
}
