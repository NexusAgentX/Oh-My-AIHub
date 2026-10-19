package detection

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

// The pinned CLI creates stored context through an actual turn, even with
// store:false. A session ID alone does not initialize notes/history. Retain
// only the synthetic window ID in bounded worker memory, never a turn body.
func (n *nativeScene) codexContextInitialize(body nativeObject, model, session string) (string, error) {
	metadata := nativeObject{"session_id": session, "thread_id": session}
	body["client_metadata"] = metadata
	key := "codex-context-window:" + session
	if window := string(n.s.Recall(key)); window != "" {
		return window, nil // Do not replace the seeded history with helper input.
	}
	window, a := ids.NewUUID()
	installation, b := ids.NewUUID()
	turnID, c := ids.NewUUID()
	if a != nil || b != nil || c != nil {
		return "", ErrInvalid
	}
	logicalWindow := session + ":0"
	turn := nativeObject{
		"installation_id": installation, "session_id": session, "thread_id": session,
		"turn_id": turnID, "root_turn_id": turnID, "agent_name": "/root",
		"window_id": logicalWindow, "context_window_id": window, "window_number": 0,
		"request_kind": "turn", "history_ingest_requested": true, "history_initialization": "new",
		"model": model, "turn_started_at_unix_ms": time.Now().UnixMilli(),
		"sandbox": "read-only", "sandbox_mode": "read-only",
		"analytics_enabled": false, "auto_review_enabled": false,
		"node_repl_disabled": false, "node_repl_auto_review_required": false,
	}
	metadata["turn_id"], metadata["root_turn_id"] = turnID, turnID
	metadata["x-codex-installation-id"] = installation
	metadata["x-codex-window-id"] = logicalWindow
	metadata["x-codex-turn-metadata"] = string(mustNativeJSON(turn))
	body["reasoning"] = nativeObject{"summary": "auto"}
	body["include"] = []any{"reasoning.encrypted_content"}
	body["prompt_cache_key"] = session
	if err := n.s.Remember(key, []byte(window)); err != nil {
		return "", err
	}
	return window, nil
}

func (n *nativeScene) codexFreshContext(ctx context.Context, model, session string) nativeOutcome {
	body := codexBase(model)
	body["input"] = []any{nativeObject{"role": "user", "content": "Reply with exactly OK."}}
	if _, err := n.codexContextInitialize(body, model, session); err != nil {
		return nativeOutcome{State: "inconclusive", Reason: "detector_error"}
	}
	return n.codexInfer(ctx, body, "")
}
