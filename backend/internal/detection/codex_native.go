package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

// RegisterCodexNative claims only executable upstream scenarios. Neither a
// direct upstream response nor a successful catalog read supplies gateway or
// official-CLI evidence. Unimplemented scenarios keep their pending entries.
func (r *Registry) RegisterCodexNative() error {
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F8" {
			continue
		}
		for _, req := range suite.Requirements {
			if !slices.Contains([]string{"codex-special.RL01", "codex-special.RL02", "codex-special.RL03", "codex-special.RL04", "codex-special.RL05", "codex-special.RL06", "codex-special.RL07", "codex-special.RL08", "codex-special.RL09", "codex-special.RL12", "codex-special.RL13", "codex-special.RL14", "codex-special.RL15", "codex-special.RL16", "codex-special.RL17", "codex-special.RL18", "codex-special.RL19", "codex-special.RL20", "codex-special.RL21", "codex-special.RL22", "codex-special.RL24", "codex-special.RL25"}, req.ID) {
				continue
			}
			if err := r.Register(Definition{ID: req.ID + "@upstream_protocol", SuiteID: suite.ID, Requirements: []string{req.ID}, Layer: "upstream_protocol", CodeVersion: engineVersion, Run: func(ctx context.Context, s *Session) Result {
				n := nativeScene{s: s, protocol: "openai-responses", layer: "upstream_protocol", requirement: req, checks: map[string]Assertion{}}
				return n.runCodex(ctx)
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *nativeScene) runCodex(ctx context.Context) Result {
	switch n.requirement.ID {
	case "codex-special.RL01", "codex-special.RL02", "codex-special.RL03", "codex-special.RL08", "codex-special.RL12":
		n.codexTools(ctx)
	case "codex-special.RL05":
		n.codexReasoningContext(ctx)
	case "codex-special.RL04":
		n.codexSequentialTools(ctx)
	case "codex-special.RL07":
		n.codexToolExtensions(ctx)
	case "codex-special.RL06":
		n.codexInstructions(ctx)
	case "codex-special.RL09":
		n.codexReasoningReplay(ctx)
	case "codex-special.RL13":
		out, valid := n.codexCatalog(ctx)
		n.check("provider_api_auth", out, valid)
	case "codex-special.RL15":
		out, valid := n.codexCatalog(ctx)
		n.check("native_catalog", out, valid)
	case "codex-special.RL16":
		n.codexSearch(ctx)
	case "codex-special.RL18":
		n.codexNotes(ctx)
	case "codex-special.RL17":
		n.codexThreadHint(ctx)
	case "codex-special.RL19":
		n.codexHistory(ctx)
	case "codex-special.RL25":
		n.codexImages(ctx)
	case "codex-special.RL14":
		n.codexEncoding(ctx)
	case "codex-special.RL24":
		n.codexTiers(ctx)
	case "codex-special.RL20":
		n.codexCompact(ctx, false)
	case "codex-special.RL21", "codex-special.RL22":
		n.codexCompact(ctx, true)
	}
	return n.finish()
}

func (n *nativeScene) codexCatalog(ctx context.Context) (nativeOutcome, bool) {
	// The pinned CLI supplies its version when reading this native catalog.
	// This response remains private worker memory; identity and model metadata
	// are never copied into the report or treated as capability evidence.
	out := n.call(ctx, Request{EndpointID: "codex.models", Path: "/backend-api/codex/models", Query: url.Values{"client_version": {"0.163.0"}}, Category: "query"}, true)
	models, present := out.Reply.Object["models"].([]any)
	if !present || len(models) > 10000 {
		return out, false
	}
	valid := true
	seen := make(map[string]bool, len(models))
	for _, raw := range models {
		model := object(raw)
		slug := str(model["slug"])
		valid = valid && gatewaycontract.ValidModelID(slug) && !seen[slug]
		seen[slug] = true
	}
	return out, valid
}

func codexBase(model string) nativeObject {
	body := baseNative("openai-responses", model, nativeBasicPrompt)
	body["instructions"] = "Follow the user's request exactly."
	body["stream"] = true
	return body
}

func (n *nativeScene) codexInfer(ctx context.Context, body nativeObject, encoding string) nativeOutcome {
	plain, err := json.Marshal(body)
	if err != nil {
		return nativeOutcome{State: "inconclusive", Reason: "detector_error"}
	}
	wire, err := nativeencoding.Encode(ctx, encoding, plain)
	if err != nil {
		// Codec capacity/failure is local, not evidence against the supplier.
		return nativeOutcome{State: "inconclusive", Reason: "detector_error"}
	}
	headers := http.Header{"Content-Type": {"application/json"}}
	if encoding != "" {
		headers.Set("Content-Encoding", encoding)
	}
	return n.call(ctx, Request{EndpointID: "codex.responses", Path: "/backend-api/codex/responses", Headers: headers, Body: wire, Category: "generate"}, true)
}

func codexMarker(out nativeOutcome) bool {
	return contentMatches(out, nativeMarker) && nativeEndedNormally(out.Reply)
}

func (n *nativeScene) codexEncoding(ctx context.Context) {
	body := codexBase(n.s.Target.Model)
	compressed := n.codexInfer(ctx, body, "zstd")
	n.check("content_encoding_zstd", compressed, codexMarker(compressed))
	n.check("native_zstd_request", compressed, codexMarker(compressed))
	plain := n.codexInfer(ctx, body, "")
	n.check("uncompressed_request", plain, codexMarker(plain))
	if n.layer != "official_cli" {
		for _, encoding := range []string{"zstd", ""} {
			raw, _ := json.Marshal(body)
			wire, err := nativeencoding.Encode(ctx, encoding, raw)
			if err != nil {
				continue
			}
			headers := http.Header{"Content-Type": {"application/json"}}
			label := "public_uncompressed_request"
			if encoding != "" {
				headers.Set("Content-Encoding", encoding)
				label = "public_zstd_request"
			}
			out := n.call(ctx, Request{EndpointID: "responses.create", Path: "/v1/responses", Headers: headers, Body: wire, Category: "generate"}, true)
			n.check(label, out, codexMarker(out))
		}
	}
}

func codexServiceTiers(profile TestProfile) []string {
	tiers := []string{"openai:default"}
	for _, tier := range profile.ServiceTiers {
		if strings.HasPrefix(tier, "openai:") && canonicalNativeTier("openai-responses", nativeRequestedTier(tier)) != "" && !slices.Contains(tiers, tier) {
			tiers = append(tiers, tier)
		}
	}
	return tiers
}

func (n *nativeScene) codexTiers(ctx context.Context) {
	for _, configured := range codexServiceTiers(n.s.Target.Profile) {
		if ctx.Err() != nil {
			return
		}
		wire := nativeRequestedTier(configured)
		body := codexBase(n.s.Target.Model)
		body["service_tier"] = wire
		out := n.codexInfer(ctx, body, "")
		accepted := out.State == "pass" && codexMarker(out)
		n.check("requested_service_tier", out, accepted)
		actual := observedTier(n.protocol, out)
		expected := canonicalNativeTier(n.protocol, wire)
		matched := accepted && actual != "" && actual == expected
		if accepted && !matched {
			n.uncertain("actual_service_tier", out)
		} else {
			n.check("actual_service_tier", out, matched)
		}
		n.check("reliable_tier_usage", out, accepted && actual != "" && out.Reply.Usage.Reliable)
		setting := SettingEvidence{Kind: "service_tier", Configured: configured, Wire: wire, Actual: actual, State: out.State, Reason: out.Reason, Accepted: accepted, UsageReliable: out.Reply.Usage.Reliable, AttemptIDs: []string{out.Response.AttemptID}}
		if out.State == "pass" {
			switch {
			case !accepted:
				setting.State, setting.Reason = "fail", "assertion_failed"
			case !matched || !out.Reply.Usage.Reliable:
				setting.State, setting.Reason = "inconclusive", "not_observed"
			}
		}
		n.result.Settings = append(n.result.Settings, setting)
	}
}
