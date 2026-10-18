package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// RegisterMCP requires an operator-configured reachable application origin.
// Missing configuration leaves these scenarios pending, never unsupported.
func (r *Registry) RegisterMCP(fixture *MCPFixture) error {
	if fixture == nil {
		return nil
	}
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F4" {
			continue
		}
		for _, req := range suite.Requirements {
			if !strings.HasSuffix(req.ID, ".mcp") {
				continue
			}
			protocol, requirement := suite.Protocol, req
			code := sha256.Sum256([]byte(nativeEngineVersion + fixture.origin + "mcp-json-2025-06-18-v1"))
			if err := r.Register(Definition{ID: req.ID + "@upstream_protocol", SuiteID: suite.ID, Requirements: []string{req.ID}, Layer: "upstream_protocol", CodeVersion: hex.EncodeToString(code[:]), Run: func(ctx context.Context, s *Session) Result {
				n := &nativeScene{s: s, protocol: protocol, requirement: requirement, checks: map[string]Assertion{}}
				return n.mcp(ctx, fixture)
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (n *nativeScene) mcp(ctx context.Context, f *MCPFixture) Result {
	p, closeProbe, err := f.open()
	if err != nil {
		return Result{State: "invalid"}
	}
	defer closeProbe()
	body := baseNative(n.protocol, n.s.Target.Model, "Call the aihub_test_marker tool with an empty object. Reply only with its exact returned marker. Do not invent or shorten the marker.")
	outputLimit(n.protocol, body, 1024)
	headers := http.Header{}
	if n.protocol == "openai-responses" {
		body["tools"] = []any{nativeObject{"type": "mcp", "server_label": mcpServer, "server_url": f.origin + mcpFixturePath, "authorization": p.token, "allowed_tools": []any{mcpTool}, "require_approval": "always"}}
	} else {
		headers.Set("anthropic-beta", "mcp-client-2025-11-20")
		body["mcp_servers"] = []any{nativeObject{"type": "url", "name": mcpServer, "url": f.origin + mcpFixturePath, "authorization_token": p.token}}
		body["tools"] = []any{nativeObject{"type": "mcp_toolset", "mcp_server_name": mcpServer, "default_config": nativeObject{"enabled": false}, "configs": nativeObject{mcpTool: nativeObject{"enabled": true}}}}
	}
	var out nativeOutcome
	if n.protocol == "native-messages" {
		out = n.messageToolLoop(ctx, body, headers)
	} else {
		out = n.inferHeaders(ctx, body, false, false, headers)
	}
	first := out
	approved := n.protocol != "openai-responses"
	if n.protocol == "openai-responses" && out.State == "pass" {
		approvals := responseItems(out, "mcp_approval_request")
		if len(approvals) == 1 {
			a := approvals[0]
			args, e := decodeNative([]byte(str(a["arguments"])))
			if e == nil && len(args) == 0 && str(a["id"]) != "" && str(a["name"]) == mcpTool && str(a["server_label"]) == mcpServer {
				// Carry the whole native output rather than registering opaque
				// item IDs or requiring a saved previous_response_id.
				input := []any{nativeObject{"role": "user", "content": body["input"]}}
				input = append(input, list(out.Reply.Object["output"])...)
				input = append(input, nativeObject{"type": "mcp_approval_response", "approval_request_id": a["id"], "approve": true})
				body["input"] = input
				out = n.inferHeaders(ctx, body, false, false, headers)
				approved = true
			}
		}
	}
	observed, valid := mcpResult(out, n.protocol, p.marker)
	valid = valid && approved && p.calls.Load() > 0 && contentMatches(out, p.marker) && nativeEndedNormally(out.Reply)
	for _, label := range n.requirement.Assertions {
		if out.State == "pass" && !observed {
			n.uncertain(label, out)
		} else {
			n.linkedCheck(label, first, out, valid)
		}
		if len(out.ProofAttempts) > 0 {
			a := n.checks[label]
			a.AttemptIDs = append([]string{}, out.ProofAttempts...)
			n.checks[label] = a
		}
	}
	if valid {
		n.toolMeter(out, 0, int64(p.calls.Load()))
	}
	return n.finish()
}
func mcpResult(out nativeOutcome, protocol, marker string) (bool, bool) {
	if protocol == "openai-responses" {
		calls := responseItems(out, "mcp_call")
		for _, c := range calls {
			args, err := decodeNative([]byte(str(c["arguments"])))
			if err == nil && len(args) == 0 && c["error"] == nil && str(c["id"]) != "" && str(c["server_label"]) == mcpServer && str(c["name"]) == mcpTool && strings.Contains(str(c["output"]), marker) {
				return true, true
			}
		}
		return len(calls) > 0, false
	}
	uses := map[string]bool{}
	observed := false
	for _, raw := range nativeParts(out) {
		b := object(raw)
		if str(b["type"]) == "mcp_tool_use" {
			observed = true
			if str(b["name"]) == mcpTool && str(b["server_name"]) == mcpServer && str(b["id"]) != "" && object(b["input"]) != nil && len(object(b["input"])) == 0 {
				uses[str(b["id"])] = true
			}
		}
		if str(b["type"]) == "mcp_tool_result" && b["is_error"] == false && uses[str(b["tool_use_id"])] {
			for _, c := range list(b["content"]) {
				if str(object(c)["type"]) == "text" && str(object(c)["text"]) == marker {
					return true, true
				}
			}
		}
	}
	return observed, false
}
