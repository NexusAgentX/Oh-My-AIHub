package detection

import (
	"context"
	"regexp"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

var codexNoteMarker = regexp.MustCompile(`AIHUB-NOTE-[0-9a-f-]{36}`)

// Thread hints are obtained at context creation and may be cached across a
// local resume. Seed the first private session before that actual CLI request;
// an empty hint in two fresh sessions cannot prove isolation.
func (b *codexBridge) seedCLIHint(ctx context.Context, session *Session, body []byte) []string {
	b.mu.Lock()
	if b.hintSeedStarted {
		b.mu.Unlock()
		return nil
	}
	b.hintSeedStarted = true
	id, err := ids.NewUUID()
	if err != nil {
		b.mu.Unlock()
		return nil
	}
	b.hintMarker = "AIHUB-HINT-" + id
	marker := b.hintMarker
	b.mu.Unlock()
	root, err := decodeNative(body)
	contextID := str(object(root["context"])["session_id"])
	if err != nil || contextID == "" {
		return nil
	}
	session.allowed["codex.responses"], session.allowed["codex.notes.write_file"] = true, true
	n := nativeScene{s: session, protocol: "openai-responses", checks: map[string]Assertion{}}
	args, _ := n.codexContextArguments(ctx, b.store.row.Candidate.UpstreamModel, contextID, "write_file", nativeObject{"path": "aihub-thread-hint.txt", "text": marker})
	if args != nil {
		written := n.codexContextCall(ctx, contextID, "notes", "write_file", args)
		b.mu.Lock()
		b.wire.HintSeeded = written.State == "pass"
		b.mu.Unlock()
	}
	return n.witnesses
}

type codexHintFacts struct{ Observed, Shaped, Known, Matched int }

func codexHintPhases(phases []codexprobe.Phase, executions []codexContextExecution) map[string]codexHintFacts {
	out := map[string]codexHintFacts{}
	for _, phase := range phases {
		facts := codexHintFacts{}
		for _, execution := range executions {
			if execution.Endpoint != "codex.notes.thread_hint" || execution.Start < phase.Start || execution.End > phase.End {
				continue
			}
			facts.Observed++
			if execution.HintShape {
				facts.Shaped++
			}
			if execution.HintKnown {
				facts.Known++
			}
			if execution.HintMatched {
				facts.Matched++
			}
		}
		out[phase.Kind] = facts
	}
	return out
}
