package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const replayCode = "PALLADIUM-641"

func (n *nativeScene) plainReplayBody(body nativeObject, reply nativeReply, prompt string) nativeObject {
	if len(reply.Candidates) != 1 || len(reply.Candidates[0].Parts) == 0 {
		return nil
	}
	next := cloneNative(body)
	parts := reply.Candidates[0].Parts
	switch n.protocol {
	case "openai-responses":
		delete(next, "previous_response_id")
		delete(next, "conversation")
		next["store"] = false
		next["input"] = append(append(list(next["input"]), parts...), nativeObject{"role": "user", "content": prompt})
	case "native-messages":
		next["messages"] = append(list(next["messages"]), nativeObject{"role": "assistant", "content": parts}, nativeObject{"role": "user", "content": prompt})
	case "native-generate-content":
		next["contents"] = append(list(next["contents"]), nativeObject{"role": "model", "parts": parts}, nativeObject{"role": "user", "parts": []any{nativeObject{"text": prompt}}})
	default:
		return nil
	}
	return next
}

func (n *nativeScene) reasoning(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, "Remember the synthetic code "+replayCode+". Calculate 37 times 43, verify the arithmetic, then reply only with the product.")
	nativeReasoningConfig(n.protocol, body)
	first := n.infer(ctx, body, false, true)
	n.check("native_current_configuration", first, first.Reply.Structure && strings.Contains(first.Reply.Text, "1591") && nativeEndedNormally(first.Reply))
	if first.Reply.Structure && (first.Reply.Reasoning || first.Reply.Usage.Reasoning > 0) {
		n.check("actual_reasoning_when_observable", first, true)
	} else {
		n.unobserved("actual_reasoning_when_observable", first)
	}
	for _, label := range []string{"complete_opaque_signature_or_encrypted_replay", "self_contained_replay_without_resource_reference"} {
		if !first.Reply.Structure || !first.Reply.Opaque {
			n.uncertain(label, first)
			continue
		}
		next := n.plainReplayBody(body, first.Reply, "Reply only with the synthetic code I asked you to remember.")
		if next == nil {
			n.uncertain(label, first)
			continue
		}
		out := n.infer(ctx, next, false, true)
		n.linkedCheck(label, first, out, contentMatches(out, replayCode) && nativeEndedNormally(out.Reply))
	}
}

const compactionBeta = "compact-2026-09-04"

func (n *nativeScene) countCompact(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, "Remember this synthetic code for later: "+replayCode+". Reply with OK.")
	count, out := n.tokenCount(ctx, body)
	n.check("native_token_count", out, count != nil && *count > 0)
	if ctx.Err() != nil {
		return
	}
	var compact nativeOutcome
	var next nativeObject
	if n.protocol == "openai-responses" {
		request := nativeObject{"model": n.s.Target.Model, "input": body["input"], "instructions": "Keep the exact synthetic code so it can be recalled later."}
		encoded, _ := json.Marshal(request)
		compact = n.call(ctx, Request{EndpointID: "responses.compact", Path: "/v1/responses/compact", Body: encoded, Headers: http.Header{"Content-Type": {"application/json"}}, Category: "generate"}, true)
		window := list(compact.Reply.Object["output"])
		hasCompaction := false
		for _, raw := range window {
			item := object(raw)
			hasCompaction = hasCompaction || str(item["type"]) == "compaction" && str(item["encrypted_content"]) != ""
		}
		n.check("native_compact_when_applicable", compact, str(compact.Reply.Object["object"]) == "response.compaction" && hasCompaction)
		if hasCompaction && len(window) > 0 {
			next = baseNative(n.protocol, n.s.Target.Model, "")
			next["input"] = append(window, nativeObject{"role": "user", "content": "Reply only with the synthetic code from the compacted conversation."})
		}
	} else {
		request := cloneNative(body)
		request["max_tokens"] = 4096
		request["compaction"] = nativeObject{"type": "summarize"}
		compact = n.inferHeaders(ctx, request, false, true, http.Header{"anthropic-beta": {compactionBeta}})
		blocks := list(compact.Reply.Object["content"])
		valid := compact.Reply.Structure && str(compact.Reply.Object["stop_reason"]) == "compaction" && len(blocks) == 1
		if valid {
			block := object(blocks[0])
			valid = str(block["type"]) == "compaction" && str(block["content"]) != "" && str(block["signature"]) != ""
		}
		n.check("native_compact_when_applicable", compact, valid)
		if valid {
			next = baseNative(n.protocol, n.s.Target.Model, "")
			next["messages"] = []any{nativeObject{"role": "assistant", "content": blocks}, nativeObject{"role": "user", "content": "Reply only with the synthetic code from the compacted conversation."}}
		}
	}
	if next == nil {
		n.uncertain("complete_compacted_window_replay", compact)
		return
	}
	headers := http.Header{}
	if n.protocol == "native-messages" {
		headers.Set("anthropic-beta", compactionBeta)
	}
	resumed := n.inferHeaders(ctx, next, false, true, headers)
	n.linkedCheck("complete_compacted_window_replay", compact, resumed, contentMatches(resumed, replayCode) && nativeEndedNormally(resumed.Reply))
}
