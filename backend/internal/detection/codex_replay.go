package detection

import (
	"context"
	"encoding/json"
	"net/http"
)

func (n *nativeScene) codexInstructions(ctx context.Context) {
	body := codexBase(n.s.Target.Model)
	body["instructions"] = "Follow the instruction precedence of message roles."
	body["input"] = []any{nativeObject{"role": "developer", "content": "Reply with exactly " + nativeMarker + "."}, nativeObject{"role": "user", "content": "Reply with exactly CONTRADICTORY-USER-REQUEST."}}
	developer := n.codexInfer(ctx, body, "")
	n.check("developer_instruction", developer, codexMarker(developer))
	body["instructions"] = nativeBasicPrompt
	body["input"] = []any{nativeObject{"role": "user", "content": "Reply with exactly CONTRADICTORY-USER-REQUEST."}}
	instructions := n.codexInfer(ctx, body, "")
	n.check("instructions_preserved", instructions, codexMarker(instructions))
}

func (n *nativeScene) codexReasoningContext(ctx context.Context) {
	body := codexBase(n.s.Target.Model)
	body["reasoning"] = nativeObject{"context": "all_turns"}
	first := n.codexInfer(ctx, body, "")
	n.check("reasoning_context_parameter", first, codexMarker(first))
	next := n.plainReplayBody(body, first.Reply, "Repeat exactly the previous assistant reply.")
	if next == nil {
		n.uncertain("multi_turn_continuation", first)
		return
	}
	out := n.codexInfer(ctx, next, "")
	n.linkedCheck("multi_turn_continuation", first, out, codexMarker(out))
}

func (n *nativeScene) codexReasoningReplay(ctx context.Context) {
	body := codexBase(n.s.Target.Model)
	body["input"] = []any{nativeObject{"role": "user", "content": "Remember the synthetic code " + replayCode + ". Calculate 37 times 43 and reply only with the product."}}
	body["include"] = []any{"reasoning.encrypted_content"}
	body["reasoning"] = nativeObject{"effort": "low"}
	first := n.codexInfer(ctx, body, "")
	produced := first.Reply.Structure && first.Reply.Opaque && nativeEndedNormally(first.Reply)
	if !produced {
		n.uncertain("encrypted_reasoning_produced", first)
		n.uncertain("encrypted_reasoning_continued", first)
		return
	}
	n.check("encrypted_reasoning_produced", first, true)
	next := n.plainReplayBody(body, first.Reply, "Reply only with the synthetic code I asked you to remember.")
	if next == nil {
		n.uncertain("encrypted_reasoning_continued", first)
		return
	}
	out := n.codexInfer(ctx, next, "")
	n.linkedCheck("encrypted_reasoning_continued", first, out, contentMatches(out, replayCode) && nativeEndedNormally(out.Reply))
}

func (n *nativeScene) codexCompact(ctx context.Context, v2 bool) {
	body := codexBase(n.s.Target.Model)
	body["input"] = []any{nativeObject{"role": "user", "content": "Remember the exact synthetic code " + replayCode + " for later."}}
	var compact nativeOutcome
	if v2 {
		body["input"] = append(list(body["input"]), nativeObject{"type": "compaction_trigger"})
		compact = n.codexInfer(ctx, body, "")
	} else {
		delete(body, "stream")
		delete(body, "store")
		encoded, _ := json.Marshal(body)
		compact = n.call(ctx, Request{EndpointID: "codex.compact", Path: "/backend-api/codex/responses/compact", Headers: http.Header{"Content-Type": {"application/json"}}, Body: encoded, Category: "generate"}, true)
	}
	window := list(compact.Reply.Object["output"])
	valid := len(window) > 0
	found := false
	for _, raw := range window {
		item := object(raw)
		valid = valid && item != nil && str(item["type"]) != ""
		kind := str(item["type"])
		found = found || (kind == "compaction" || v2 && kind == "context_compaction") && str(item["encrypted_content"]) != ""
	}
	if v2 {
		// A compacted window can include retained input messages. Its root
		// completion is distinct from ordinary assistant-only inference output.
		n.check("compaction_trigger", compact, str(compact.Reply.Object["object"]) == "response" && str(compact.Reply.Object["status"]) == "completed")
		n.check("compaction_output", compact, valid && found)
	} else {
		n.check("native_compact", compact, str(compact.Reply.Object["object"]) == "response.compaction" && valid && found)
		n.check("complete_output_window", compact, valid && found)
	}
	if !valid || !found || compact.State != "pass" {
		n.uncertain("compaction_continuation", compact)
		return
	}
	// Preserve the entire ordered window, including messages and unknown
	// opaque metadata. Never extract just the ciphertext item or register it
	// as a remotely stored response.
	next := codexBase(n.s.Target.Model)
	next["input"] = append(window, nativeObject{"role": "user", "content": "Reply only with the synthetic code from the compacted conversation."})
	out := n.codexInfer(ctx, next, "")
	n.linkedCheck("compaction_continuation", compact, out, contentMatches(out, replayCode) && nativeEndedNormally(out.Reply))
}
