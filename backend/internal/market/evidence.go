package market

// CompatibleBinding accepts an older revision only when its capability inputs
// are identical. A price, address or display edit is not an engine change.
// The caller must still check report completeness, support state and ACL; there
// is no invented age-based expiry policy.
func CompatibleBinding(evidence, current EvidenceBinding) bool {
	return evidence.TargetKind == current.TargetKind && evidence.TargetID == current.TargetID &&
		evidence.TargetID != "" && evidence.TargetRevision > 0 && evidence.TargetRevision <= current.TargetRevision &&
		evidence.CapabilityDigest != "" && evidence.CapabilityDigest == current.CapabilityDigest &&
		evidence.ManifestVersion != "" && evidence.ManifestVersion == current.ManifestVersion &&
		evidence.CodeVersion != "" && evidence.CodeVersion == current.CodeVersion &&
		evidence.ParameterVersion != "" && evidence.ParameterVersion == current.ParameterVersion &&
		evidence.CredentialScope != "" && evidence.CredentialScope == current.CredentialScope
}
