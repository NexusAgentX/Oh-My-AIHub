package detection

import (
	"context"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func (r *Registry) RegisterCodexGateway() error {
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F8" {
			continue
		}
		for _, req := range suite.Requirements {
			if _, implemented := r.definitions[req.ID+"@upstream_protocol"]; !implemented {
				continue
			}
			if err := r.Register(Definition{ID: req.ID + "@gateway_proxy", SuiteID: suite.ID, Requirements: []string{req.ID}, Layer: "gateway_proxy", CodeVersion: engineVersion + ":" + gateway.ProbeImplementationVersion(), Run: func(ctx context.Context, s *Session) Result {
				return runCodexGateway(ctx, s, req)
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func runCodexGateway(ctx context.Context, s *Session, req gatewaycontract.Requirement) Result {
	// Bootstrap is an actual upstream request, not a report or a platform
	// identity shortcut. The native group prerequisites can reuse this request.
	s.allowed["codex.models"] = true
	bootstrap := nativeScene{s: s, protocol: "openai-responses", checks: map[string]Assertion{}}
	catalog, _ := bootstrap.codexCatalog(ctx)
	model := s.Target.Model
	if model == "" {
		for _, raw := range list(catalog.Reply.Object["models"]) {
			if slug := str(object(raw)["slug"]); gatewaycontract.ValidModelID(slug) {
				model = slug
				break
			}
		}
	}
	b, err := newCodexBridge(s, model, bootstrap.witnesses)
	if err != nil {
		return Result{State: "invalid"}
	}
	defer b.close()
	proxy := *s
	proxy.Target = s.Target
	if proxy.Target.Model != "" {
		proxy.Target.Model = codexProbeModel
	}
	proxy.exchange, proxy.usage = b.exchange, Usage{}
	proxy.retained, proxy.retainedBytes = nil, 0
	defer func() { proxy.secret = nil; proxy.clear() }()
	n := nativeScene{s: &proxy, protocol: "openai-responses", layer: "gateway_proxy", requirement: req, checks: map[string]Assertion{}, witnesses: slices.Clone(bootstrap.witnesses)}
	var result Result
	switch req.ID {
	case "codex-special.RL13":
		who := n.call(ctx, Request{EndpointID: "identity.whoami", Path: "/v1/user-auth-credential/whoami", Category: "query"}, false)
		n.check("platform_identity", who, str(who.Reply.Object["chatgpt_user_id"]) == "aihub-user-probe-consumer" && str(who.Reply.Object["chatgpt_account_id"]) == "aihub-account-probe-consumer" && str(who.Reply.Object["chatgpt_plan_type"]) == "aihub")
		accounts := n.call(ctx, Request{EndpointID: "identity.accounts", Path: "/backend-api/wham/accounts/check", Category: "query"}, false)
		rows := list(accounts.Reply.Object["accounts"])
		n.check("platform_identity", accounts, len(rows) == 1 && str(object(rows[0])["id"]) == "aihub-account-probe-consumer" && object(rows[0])["plan_type"] == nil)
		out, _ := n.codexCatalog(ctx)
		n.check("credential_separation", out, b.separated && b.requests > 0)
		result = n.finish()
	case "codex-special.RL15":
		b.store.row.Candidate.Evidence = map[string]bool{"upstream_protocol/codex-special.RL15/native_catalog": true}
		out, valid := n.codexCatalog(ctx)
		acl, evidence := valid, valid
		for _, raw := range list(out.Reply.Object["models"]) {
			m := object(raw)
			acl = acl && str(m["slug"]) == codexProbeModel && str(m["display_name"]) == codexProbeModel
			for _, field := range []string{"context_window", "max_context_window", "auto_compact_token_limit"} {
				evidence = evidence && m[field] == nil
			}
			for _, field := range []string{"use_responses_lite", "supports_experimental_context", "supports_search_tool"} {
				evidence = evidence && m[field] == false
			}
			evidence = evidence && len(list(m["service_tiers"])) == 0 && len(list(m["additional_speed_tiers"])) == 0
		}
		acl = acl && out.Reply.Object["accounts"] == nil
		n.check("model_acl_filter", out, acl)
		n.check("evidence_metadata", out, evidence)
		result = n.finish()
	default:
		result = n.runCodex(ctx)
	}
	s.usage.Add(proxy.usage)
	return result
}
