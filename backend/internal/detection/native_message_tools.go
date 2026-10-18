package detection

import (
	"context"
	"net/http"
	"strings"
)

func messageServerResult(out nativeOutcome, kind string) (bool, bool) {
	uses := map[string]nativeObject{}
	observed, valid := false, false
	for _, raw := range nativeParts(out) {
		block := object(raw)
		if str(block["type"]) == "server_tool_use" {
			name := str(block["name"])
			if kind == "search" && name == "web_search" || kind == "code" && name == "bash_code_execution" {
				observed = true
				if str(block["id"]) != "" && object(block["input"]) != nil {
					uses[str(block["id"])] = block
				}
			}
		}
		if uses[str(block["tool_use_id"])] == nil {
			continue
		}
		if kind == "search" && str(block["type"]) == "web_search_tool_result" {
			for _, rawResult := range list(block["content"]) {
				result := object(rawResult)
				valid = valid || str(result["type"]) == "web_search_result" && publicToolURL(str(result["url"])) && str(result["title"]) != "" && str(result["encrypted_content"]) != ""
			}
		}
		if kind == "code" && str(block["type"]) == "bash_code_execution_tool_result" {
			result := object(block["content"])
			code, ok := fieldInt(result, "return_code")
			valid = valid || str(result["type"]) == "bash_code_execution_result" && ok && code == 0 && strings.TrimSpace(str(result["stdout"])) == codeAnswer
		}
	}
	expected := toolPageTitle
	if kind == "code" {
		expected = codeAnswer
	}
	return observed, valid && contentMatches(out, expected) && nativeEndedNormally(out.Reply)
}
func (n *nativeScene) messageServerTools(ctx context.Context) {
	for _, kind := range []string{"search", "code"} {
		body := builtinBody(n.protocol, n.s.Target.Model, kind)
		out := n.messageToolLoop(ctx, body, nil)
		observed, valid := messageServerResult(out, kind)
		label := "web_search_20250305"
		if kind == "code" {
			label = "code_execution_20250825"
		}
		n.builtinCheck(label, out, observed, valid)
		n.builtinCheck("actual_tool_use_result_and_reliable_usage", out, observed, valid && out.Reply.Usage.Reliable)
		if valid {
			n.toolMeter(out, 0, 1)
		}
	}
}

// pause_turn is a bounded native continuation, not a client tool call. Replay
// complete assistant content with the same tool declarations; never fabricate
// a tool_result or strip encrypted search/signature blocks from the history.
func (n *nativeScene) messageToolLoop(ctx context.Context, body nativeObject, headers http.Header) nativeOutcome {
	var parts []any
	var proofs []string
	reliable := true
	for hop := 0; hop < 4; hop++ {
		out := n.inferHeaders(ctx, body, false, hop == 0, headers)
		if out.State != "pass" {
			out.ProofAttempts = proofs
			return out
		}
		proofs = append(proofs, out.Response.AttemptID)
		parts = append(parts, nativeParts(out)...)
		reliable = reliable && out.Reply.Usage.Reliable
		if str(out.Reply.Object["stop_reason"]) != "pause_turn" {
			if len(out.Reply.Candidates) == 1 {
				out.Reply.Candidates[0].Parts = parts
			}
			out.Reply.Usage.Reliable = reliable
			out.ProofAttempts = proofs
			return out
		}
		if hop == 3 {
			out.State, out.Reason = "inconclusive", "prerequisite_failed"
			out.ProofAttempts = proofs
			return out
		}
		next := cloneNative(body)
		next["messages"] = append(list(next["messages"]), nativeObject{"role": "assistant", "content": out.Reply.Object["content"]})
		body = next
	}
	return nativeOutcome{State: "inconclusive", Reason: "detector_error"}
}
