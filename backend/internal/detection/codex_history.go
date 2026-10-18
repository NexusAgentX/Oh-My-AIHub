package detection

import (
	"context"
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

func (n *nativeScene) codexHistory(ctx context.Context) {
	model := n.codexContextModel(ctx)
	if model == "" {
		return
	}
	session, a := ids.NewUUID()
	other, b := ids.NewUUID()
	window, c := ids.NewUUID()
	if a != nil || b != nil || c != nil {
		n.codexContextDetectorError()
		return
	}
	marker := "AIHUB-HISTORY-" + other
	seedBody := codexBase(model)
	seedBody["instructions"] = "Reply with exactly OK."
	seedBody["input"] = []any{nativeObject{"role": "user", "content": marker}}
	// These are the pinned CLI's real ingest fields, not a bare previous-ID
	// reference. The seeded window belongs to this synthetic session only.
	turn := nativeObject{"session_id": session, "thread_id": session, "agent_name": "/root", "window_id": window, "context_window_id": window, "window_number": 0, "request_kind": "turn", "history_ingest_requested": true}
	seedBody["client_metadata"] = nativeObject{"session_id": session, "thread_id": session, "x-codex-window-id": window, "x-codex-turn-metadata": string(mustNativeJSON(turn))}
	seed := n.codexInfer(ctx, seedBody, "")
	if seed.State != "pass" || !nativeEndedNormally(seed.Reply) {
		return
	}
	windowsArgs := nativeObject{"limit": 8, "recent_first": true}
	windows := n.codexContextCall(ctx, session, "history", "list_windows", windowsArgs)
	windowsText := n.codexContextText(ctx, model, session, "history", "list_windows", "Return only the actual full window IDs, one per line, with no explanation.", windowsArgs, windows)
	observed := strings.Contains(windowsText.Reply.Text, window) && nativeEndedNormally(windowsText.Reply)
	n.contextObserved("list_windows", windows, windowsText, observed)
	// Never select an arbitrary returned window from a shared upstream account.
	// Even when listings are delayed, only this task's known seed window is read.
	itemsArgs := nativeObject{"window_id": window, "role": "user", "limit": 8, "max_chars_per_item": 4096}
	items := n.codexContextCall(ctx, session, "history", "list_items", itemsArgs)
	itemText := n.codexContextText(ctx, model, session, "history", "list_items", "Select the first actual user-message item. Return only a JSON object with item_id (the unchanged opaque ID), window_id (the unchanged full window ID), and content (the full original user text without metadata or ID markers). Never invent IDs.", itemsArgs, items)
	item, parseErr := decodeNative([]byte(strings.TrimSpace(itemText.Reply.Text)))
	itemID := str(item["item_id"])
	identified := parseErr == nil && len(itemID) > 0 && len(itemID) <= 512 && !strings.ContainsAny(itemID, "\r\n\x00") && str(item["window_id"]) == window && str(item["content"]) == marker && nativeEndedNormally(itemText.Reply)
	n.contextObserved("list_items", items, itemText, identified)
	searchArgs, generated := n.codexContextArgumentsFor(ctx, model, session, "history", "search_contents", nativeObject{"window_id": window, "query": "AIHUB-HISTORY-"})
	if searchArgs != nil {
		search := n.codexContextCall(ctx, session, "history", "search_contents", searchArgs)
		searchText := n.codexContextText(ctx, model, session, "history", "search_contents", "Return only the full actual matching user-message text without metadata or ID markers.", searchArgs, search)
		n.contextObserved("search_contents", search, searchText, contentMatches(searchText, marker) && nativeEndedNormally(searchText.Reply))
	} else {
		n.check("search_contents", generated, false)
	}
	if !identified {
		// No guessed ID is used to manufacture read coverage. Actual prerequisite
		// observations explain why read/resume/isolation could not be verified.
		return
	}
	readArgs := nativeObject{"window_id": window, "item_id": itemID, "offset_chars": 0, "limit_chars": 4096}
	read := n.codexContextCall(ctx, session, "history", "read_item", readArgs)
	// This fresh model input contains no original seed text. Reconstructing the
	// synthetic user message can only use the actual remote history result.
	text := n.codexContextText(ctx, model, session, "history", "read_item", "Return only the full actual original user-message text without metadata or ID markers.", readArgs, read)
	valid := contentMatches(text, marker) && nativeEndedNormally(text.Reply)
	n.linkedCheck("read_item", read, text, valid)
	n.linkedCheck("remote_resume", read, text, valid)
	foreign := n.codexContextCall(ctx, other, "history", "read_item", readArgs)
	foreignText := n.codexContextText(ctx, model, other, "history", "read_item", "Return only the full actual original user-message text without metadata or ID markers.", readArgs, foreign)
	denied := foreign.Response.Status == http.StatusNotFound && nativeError(n.protocol, foreign.Reply.Object)
	if denied {
		foreignText = nativeOutcome{State: "pass", Reason: "ok", Response: foreign.Response}
	}
	n.linkedCheck("context_isolation", read, foreignText, valid && (denied || contentMatches(foreignText, "{}") && nativeEndedNormally(foreignText.Reply)))
}

func (n *nativeScene) contextObserved(label string, first, interpreted nativeOutcome, observed bool) {
	if interpreted.State == "pass" && !observed {
		interpreted.State, interpreted.Reason = "inconclusive", "not_observed"
	}
	n.linkedCheck(label, first, interpreted, observed)
}
