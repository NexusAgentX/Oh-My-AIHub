package detection

import (
	"context"
	"slices"
)

func (n *nativeScene) continuation(ctx context.Context) {
	// Reuse the exact controlled-length request from the JSON scenario. A short
	// capped task can expose a native MAX_TOKENS continuation token; do not force
	// a 32K/64K endurance run just to trigger the per-hop continuation ceiling.
	body := baseNative(n.protocol, n.s.Target.Model, nativeLengthPrompt)
	outputLimit(n.protocol, body, 16)
	first := n.infer(ctx, body, false, true)
	candidates := list(first.Reply.Object["candidates"])
	token := ""
	if len(candidates) == 1 {
		token = str(object(candidates[0])["continuationToken"])
	}
	signal := first.Reply.Structure && len(first.Reply.Candidates) == 1 && slices.Contains([]string{"MAX_TOKENS", "CONTINUATION"}, first.Reply.Candidates[0].End) && token != "" && len(token) <= 64<<10
	if !signal {
		for _, label := range n.requirement.Assertions {
			n.uncertain(label, first)
		}
		return
	}
	n.check("native_continuation_signal", first, true)
	limit, ok := addNative(max(int64(16), first.Reply.Usage.Output), 64)
	if !ok {
		n.uncertain("original_contents_and_opaque_token_replay", first)
		n.uncertain("per_hop_usage_no_duplication", first)
		return
	}
	next := cloneNative(body)
	next["continuationToken"] = token
	outputLimit(n.protocol, next, limit)
	before := n.s.usage
	out := n.infer(ctx, next, false, false)
	valid := out.Reply.Structure && len(out.Reply.Candidates) == 1 && slices.Contains([]string{"STOP", "MAX_TOKENS", "CONTINUATION"}, out.Reply.Candidates[0].End)
	combined := outputTaskNumbers(first.Reply.Text + out.Reply.Text)
	sequential := len(combined) > 0
	for i, v := range combined {
		sequential = sequential && v == i+1
	}
	thinkingOnly := first.Reply.Text == "" && out.Reply.Text == "" && first.Reply.Usage.Reasoning > 0 && out.Reply.Usage.Reasoning > 0
	n.linkedCheck("original_contents_and_opaque_token_replay", first, out, valid && (sequential || thinkingOnly))
	counted := first.Reply.Usage.Reliable && out.Reply.Usage.Reliable && n.s.usage.InputTokens-before.InputTokens == out.Reply.Usage.Input && n.s.usage.OutputTokens-before.OutputTokens == out.Reply.Usage.Output
	n.linkedCheck("per_hop_usage_no_duplication", first, out, valid && counted)
}
