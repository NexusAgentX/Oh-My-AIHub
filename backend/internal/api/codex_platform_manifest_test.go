package api

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// Ownership registry for the platform-only manifest. Go executes the named
// concrete regressions separately, once each; this test prevents a manifest
// change from silently losing its actual handler/client verification owner.
// This is never a real-upstream result or a supplier creation bypass.
func TestCodexPlatformManifestHasConcreteRegressionOwners(t *testing.T) {
	type verifier func(*testing.T)
	gateway := map[string]verifier{
		"codex-special.RL23/encrypted_reasoning_cross_channel": TestCodexOpaqueContinuationCrossesChannelsAndCredentials,
		"codex-special.RL23/signature_cross_channel":           TestCodexOpaqueContinuationCrossesChannelsAndCredentials,
		"codex-special.RL23/compaction_cross_channel":          TestCodexOpaqueContinuationCrossesChannelsAndCredentials,
		"codex-special.RL23/notes_cross_channel":               TestCodexNotesCrossGroupsWithoutSourceStickinessAndIsolateKeys,
		"codex-special.RL23/history_cross_channel":             TestOfficialCodexContextCrossesProductionChannelsAndRestores,
		"codex-special.RL23/reference_scope_enforced":          TestCodexTrueReferencesKeepOwnerAndCredentialScope,
		"codex-special.RL26/account_key_isolation":             TestCodexCollidingContextsRemainIsolatedAfterCredentialRotation,
		"codex-special.RL26/context_collision":                 TestCodexCollidingContextsRemainIsolatedAfterCredentialRotation,
		"codex-special.RL26/foreign_reference_denied":          TestCodexTrueReferencesKeepOwnerAndCredentialScope,
		"codex-special.RL26/credential_rotation":               TestCodexCollidingContextsRemainIsolatedAfterCredentialRotation,
		"codex-special.RL26/key_revocation":                    TestCodexTrueReferencesKeepOwnerAndCredentialScope,
		"codex-special.RL26/fixed_egress":                      TestCodexFixedEgressAndFailureSecretsUseProductionHandlers,
		"codex-special.RL26/secret_redaction":                  TestCodexFixedEgressAndFailureSecretsUseProductionHandlers,
	}
	cli := map[string]verifier{
		"codex-special.RL23/encrypted_reasoning_cross_channel": TestOfficialCodexOpaqueStateCrossesProductionChannels,
		"codex-special.RL23/compaction_cross_channel":          TestOfficialCodexOpaqueStateCrossesProductionChannels,
		"codex-special.RL23/notes_cross_channel":               TestOfficialCodexContextCrossesProductionChannelsAndRestores,
		"codex-special.RL23/history_cross_channel":             TestOfficialCodexContextCrossesProductionChannelsAndRestores,
		"codex-special.RL26/account_key_isolation":             TestOfficialCodexContextCrossesProductionChannelsAndRestores,
		"codex-special.RL26/credential_rotation":               TestOfficialCodexContextCrossesProductionChannelsAndRestores,
		"codex-special.RL26/key_revocation":                    TestOfficialCodexRevokedKeyCannotReachUpstream,
	}
	owners := map[string]map[string]verifier{"gateway_proxy": gateway, "official_cli": cli}
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, suite := range manifest.Suites {
		if suite.ID != "codex-special.gateway" {
			continue
		}
		if suite.Target != "gateway" || suite.Feature != "F8" {
			t.Fatal("platform regressions entered supplier scope")
		}
		for _, req := range suite.Requirements {
			for i, label := range req.Assertions {
				for _, layer := range suite.EvidenceLayers {
					id := req.ID + "/" + label
					verify, exists := owners[layer][id]
					applicable := req.AssertionInLayer(detection.AssertionID(req.ID, i), layer)
					if applicable != exists || exists && verify == nil {
						t.Fatal("missing/inapplicable platform proof owner", layer, id)
					}
					if applicable {
						matched++
					}
				}
			}
		}
	}
	if matched != len(gateway)+len(cli) {
		t.Fatal("platform coverage registry contains stale owners", matched)
	}
}
