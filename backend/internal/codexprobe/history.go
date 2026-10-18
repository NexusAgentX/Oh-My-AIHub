package codexprobe

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

const historyMissing = "AIHUB-SYNTHETIC-HISTORY-MISSING"

func runHistoryLifecycle(ctx context.Context, c Config, d *dialogue, first string, params map[string]any, r *Result) error {
	r.Stage = "history_metadata"
	var seed struct {
		Window    string `json:"window"`
		Alternate string `json:"alternate"`
	}
	if probeControl(ctx, c, "/probe/history-window", &seed) != nil {
		return ErrClient
	}
	r.Stage = "history_metadata_id"
	if !historyWindowID(seed.Window) || seed.Alternate != "" && !historyWindowID(seed.Alternate) {
		return ErrClient
	}
	d.expected = "AIHUB-HISTORY-" + first
	r.Stage = "history_primary"
	start := time.Now().UnixNano()
	allowed := seed.Window
	if seed.Alternate != "" {
		allowed += " or " + seed.Alternate
	}
	prompt := "Use only the declared history tools to complete the pending user task in this synthetic conversation: list_windows(limit=8), then choose only an actual returned window matching the ingested identifiers " + allowed + "; list_items in that exact window with role=user, limit=8 and max_chars_per_item=4096. Select only the actual pending user task, read_item using its unchanged item ID and the same window (offset_chars=0, limit_chars=4096), and search_contents within that exact window for AIHUB-HISTORY- with limit=8. Check actual results. Return only the pending task's answer, without quoting or describing private history/tool details. Never read another window or invent IDs; if the task is not yet present, return exactly " + historyMissing + ". Do not wait for indexing."
	if d.runTurn(ctx, first, prompt) != nil {
		return ErrClient
	}
	r.ContextVerified = d.completed && d.matched
	r.HistoryUnobserved = d.missingHistory
	r.Phases = append(r.Phases, Phase{Kind: "history_primary", Start: start, End: time.Now().UnixNano()})
	// Use the actual identifier selected in the primary operations; the fixed
	// client distinguishes logical thread:number from context-window UUID.
	if probeControl(ctx, c, "/probe/history-window", &seed) != nil || !historyWindowID(seed.Window) {
		return ErrClient
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	r.Stage = "thread"
	if d.call(ctx, "thread/start", params, &thread) != nil || thread.Thread.ID == "" || thread.Thread.ID == first {
		return ErrClient
	}
	d.expected = historyMissing
	r.Stage = "history_isolation"
	start = time.Now().UnixNano()
	if d.runTurn(ctx, thread.Thread.ID, "Use history.list_items with only window "+seed.Window+", role=user, limit=8 and max_chars_per_item=4096. If an actual pending user task is accessible, read only that actual item in the same window and complete its task, returning only its answer. If no task is accessible, reply exactly "+historyMissing+". Do not copy, guess IDs, read other windows or disclose private history/tool details.") != nil {
		return ErrClient
	}
	r.IsolationVerified = d.completed && d.matched
	r.Phases = append(r.Phases, Phase{Kind: "history_isolation", Start: start, End: time.Now().UnixNano()})
	d.thread, d.turn, d.expected = first, "pending", "AIHUB-HISTORY-"+first
	r.Stage = "thread"
	if d.call(ctx, "thread/resume", map[string]any{"threadId": first, "cwd": params["cwd"], "approvalPolicy": "never", "sandbox": "read-only", "model": c.Model}, &thread) != nil || thread.Thread.ID != first {
		return ErrClient
	}
	r.Stage = "history_reopen"
	start = time.Now().UnixNano()
	if d.runTurn(ctx, first, "Use history.list_items and then history.read_item again for only the actual pending user task in window "+seed.Window+" (limit=8, max_chars_per_item=4096, offset_chars=0, limit_chars=4096). Use unchanged actual IDs and complete the pending task, returning only its answer. Do not copy a past answer, guess IDs or disclose private history/tool details. If the task is not present, reply exactly "+historyMissing+". Do not wait for indexing.") != nil {
		return ErrClient
	}
	r.ReopenVerified = d.completed && d.matched
	r.HistoryUnobserved = r.HistoryUnobserved || d.missingHistory
	r.Phases = append(r.Phases, Phase{Kind: "history_reopen", Start: start, End: time.Now().UnixNano()})
	d.matched = r.ContextVerified && r.IsolationVerified && r.ReopenVerified
	return nil
}

func historyWindowID(value string) bool {
	return len(value) <= 128 && gatewaycontract.ValidModelID(value)
}
