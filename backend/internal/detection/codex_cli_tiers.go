package detection

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func runCodexCLITiers(ctx context.Context, s *Session, req gatewaycontract.Requirement, probePath, binaryPath string) Result {
	n := nativeScene{s: s, protocol: "openai-responses", layer: "official_cli", requirement: req, checks: map[string]Assertion{}}
	for _, configured := range codexServiceTiers(s.Target.Profile) {
		if ctx.Err() != nil {
			break
		}
		requested := nativeRequestedTier(configured)
		proof := runCodexCLIProfileWithTier(ctx, s, "tier", requested, probePath, binaryPath)
		n.witnesses = append(n.witnesses, proof.Witnesses...)
		out := nativeOutcome{State: proof.State, Reason: proof.Reason}
		if len(proof.Witnesses) > 0 {
			out.Response.AttemptID = proof.Witnesses[len(proof.Witnesses)-1]
		}
		wire := proof.Wire.RequestedTier
		// The pinned CLI deliberately omits the explicit default sentinel.
		// Record the omission instead of inventing a transmitted "default" field.
		sent := wire == requested || requested == "default" && wire == ""
		if wire == "" {
			wire = "omitted"
		}
		accepted := proof.Client.Completed && proof.Client.MarkerMatched && sent && proof.Wire.LiteRequests+proof.Wire.StandardRequests == 1
		actual := proof.Wire.ActualTier
		reliable := proof.Wire.ReliableUsage == 1
		matched := accepted && actual == canonicalNativeTier("openai-responses", requested)
		n.check("requested_service_tier", out, accepted)
		if accepted && !matched {
			n.uncertain("actual_service_tier", out)
		} else {
			n.check("actual_service_tier", out, matched)
		}
		n.check("reliable_tier_usage", out, accepted && actual != "" && reliable)
		setting := SettingEvidence{Kind: "service_tier", Configured: configured, Wire: wire, Actual: actual, State: out.State, Reason: out.Reason, Accepted: accepted, UsageReliable: reliable, AttemptIDs: []string{out.Response.AttemptID}}
		if out.State == "pass" {
			if !accepted {
				setting.State, setting.Reason = "fail", "assertion_failed"
			} else if !matched || !reliable {
				setting.State, setting.Reason = "inconclusive", "not_observed"
			}
		}
		n.result.Settings = append(n.result.Settings, setting)
	}
	return n.finish()
}
