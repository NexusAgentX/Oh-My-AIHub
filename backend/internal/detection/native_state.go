package detection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

func resourceToken(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (n *nativeScene) rest(ctx context.Context, endpoint, path string, body nativeObject, query url.Values, memo bool) nativeOutcome {
	var encoded []byte
	headers := http.Header{}
	if body != nil {
		encoded, _ = json.Marshal(body)
		headers.Set("Content-Type", "application/json")
	}
	return n.call(ctx, Request{EndpointID: endpoint, Path: path, Body: encoded, Headers: headers, Query: query, Category: "query"}, memo)
}
func (n *nativeScene) track(ctx context.Context, out nativeOutcome, kind, endpoint, prefix string) bool {
	id := str(out.Reply.Object["id"])
	if out.State != "pass" || !resourceToken(id) {
		return false
	}
	return n.s.TrackResource(ctx, out.Response.AttemptID, kind, id, endpoint, prefix+url.PathEscape(id)) == nil
}
func (n *nativeScene) deleteOwned(ctx context.Context, kind, id string) nativeOutcome {
	response, err := n.s.DeleteTracked(ctx, kind, id)
	if response.AttemptID != "" && !slices.Contains(n.witnesses, response.AttemptID) {
		n.witnesses = append(n.witnesses, response.AttemptID)
	}
	out := nativeOutcome{Response: response, State: "inconclusive", Reason: "external_error"}
	if err != nil {
		if ctx.Err() != nil {
			out.Reason = "cancelled"
		}
		return out
	}
	if response.Status >= 200 && response.Status < 300 {
		out.State, out.Reason = "pass", "ok"
		if !verifiedDeletion(kind, id, response) {
			out.State, out.Reason = "fail", "invalid_response"
		}
		if response.Status != http.StatusNoContent {
			o, e := decodeNative(response.Body)
			out.Reply.Object = o
			if e != nil {
				out.State, out.Reason = "fail", "invalid_response"
			}
		}
	} else if response.Status >= 400 && response.Status < 500 && response.Status != 401 && response.Status != 403 && response.Status != 429 {
		out.State, out.Reason = "fail", "assertion_failed"
	}
	return out
}

type ownedPage struct {
	Last     nativeOutcome
	Valid    bool
	Found    bool
	Attempts []string
}

func (n *nativeScene) ownedPages(ctx context.Context, endpoint, path, id string) ownedPage {
	p := ownedPage{Valid: true}
	cursor := ""
	seen := map[string]bool{}
	for range 3 {
		query := url.Values{"limit": {"2"}, "order": {"desc"}}
		if cursor != "" {
			query.Set("after", cursor)
		}
		out := n.rest(ctx, endpoint, path, nil, query, false)
		p.Last = out
		p.Attempts = append(p.Attempts, out.Response.AttemptID)
		root := out.Reply.Object
		data, ok := root["data"].([]any)
		more, hasMore := root["has_more"].(bool)
		if out.State != "pass" || !ok || !hasMore || str(root["object"]) != "list" || len(data) > 10000 {
			p.Valid = false
			break
		}
		for _, raw := range data {
			p.Found = p.Found || str(object(raw)["id"]) == id
		}
		if !more {
			break
		}
		next := str(root["last_id"])
		if !resourceToken(next) || next == cursor || seen[next] {
			p.Valid = false
			break
		}
		seen[next] = true
		cursor = next
	}
	return p
}
func (n *nativeScene) pageCheck(label string, p ownedPage, requireFound bool) {
	if p.Valid && requireFound && !p.Found {
		n.uncertain(label, p.Last)
	} else {
		n.check(label, p.Last, p.Valid)
	}
	a := n.checks[label]
	for _, id := range p.Attempts {
		if !slices.Contains(a.AttemptIDs, id) {
			a.AttemptIDs = append(a.AttemptIDs, id)
		}
	}
	if len(a.AttemptIDs) > 16 {
		a.AttemptIDs = a.AttemptIDs[len(a.AttemptIDs)-16:]
	}
	n.checks[label] = a
}

func (n *nativeScene) stored(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
	body["store"], body["metadata"] = true, nativeObject{"aihub_probe": n.s.jobID}
	first := n.infer(ctx, body, false, false)
	owned := first.Reply.Structure && n.track(ctx, first, "stored_chat", "chat.delete", "/v1/chat/completions/")
	n.check("create_store_true", first, owned && contentMatches(first, nativeMarker))
	if !owned {
		return
	}
	id := first.Reply.ID
	path := "/v1/chat/completions/" + url.PathEscape(id)
	input, output := n.s.usage.InputTokens, n.s.usage.OutputTokens
	get := n.rest(ctx, "chat.get", path, nil, nil, false)
	n.check("owned_list_get_metadata_messages_delete", get, get.Reply.Structure && get.Reply.ID == id && contentMatches(get, nativeMarker))
	update := n.rest(ctx, "chat.update", path, nativeObject{"metadata": nativeObject{"aihub_probe": n.s.jobID, "phase": "checked"}}, nil, false)
	n.check("owned_list_get_metadata_messages_delete", update, str(update.Reply.Object["id"]) == id && str(object(update.Reply.Object["metadata"])["phase"]) == "checked")
	page := n.ownedPages(ctx, "chat.list", "/v1/chat/completions", id)
	n.pageCheck("owned_list_get_metadata_messages_delete", page, true)
	n.pageCheck("bounded_pagination", page, false)
	messages := n.ownedPages(ctx, "chat.messages", path+"/messages", "")
	n.pageCheck("owned_list_get_metadata_messages_delete", messages, false)
	n.pageCheck("bounded_pagination", messages, false)
	deleted := n.deleteOwned(ctx, "stored_chat", id)
	n.check("owned_list_get_metadata_messages_delete", deleted, deleted.State == "pass")
	n.check("queries_do_not_recount_inference_usage", deleted, input == n.s.usage.InputTokens && output == n.s.usage.OutputTokens)
}

func (n *nativeScene) state(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, "Remember this synthetic code and reply only with it: "+replayCode)
	body["store"] = true
	first := n.infer(ctx, body, false, false)
	owned := first.Reply.Structure && n.track(ctx, first, "response", "responses.delete", "/v1/responses/")
	n.check("previous_response_id_three_turns", first, owned && contentMatches(first, replayCode))
	if !owned {
		return
	}
	prior := first
	for range 2 {
		next := baseNative(n.protocol, n.s.Target.Model, "Reply only with the same synthetic code from the earlier turn.")
		next["store"], next["previous_response_id"] = true, prior.Reply.ID
		out := n.infer(ctx, next, false, false)
		tracked := out.Reply.Structure && n.track(ctx, out, "response", "responses.delete", "/v1/responses/")
		n.linkedCheck("previous_response_id_three_turns", prior, out, tracked && contentMatches(out, replayCode))
		if !tracked {
			break
		}
		prior = out
	}
	plain := baseNative(n.protocol, n.s.Target.Model, "Remember the synthetic code "+replayCode+". Reply only OK.")
	start := n.infer(ctx, plain, false, true)
	replay := n.plainReplayBody(plain, start.Reply, "Reply only with the synthetic code I asked you to remember.")
	if replay != nil {
		out := n.infer(ctx, replay, false, true)
		n.linkedCheck("store_false_self_contained_replay", start, out, contentMatches(out, replayCode) && replay["store"] == false && replay["previous_response_id"] == nil)
	} else {
		n.uncertain("store_false_self_contained_replay", start)
	}
	conversation := n.rest(ctx, "conversations.create", "/v1/conversations", nativeObject{"metadata": nativeObject{"aihub_probe": n.s.jobID}}, nil, false)
	convOwned := str(conversation.Reply.Object["object"]) == "conversation" && n.track(ctx, conversation, "conversation", "conversations.delete", "/v1/conversations/")
	convID := str(conversation.Reply.Object["id"])
	if convOwned {
		n.conversation(ctx, conversation)
	} else {
		n.uncertain("conversation_flow", conversation)
		n.uncertain("item_reference", conversation)
	}
	missing, _ := ids.NewUUID()
	unknown := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
	unknown["previous_response_id"] = "resp_aihub_missing_" + strings.ReplaceAll(missing, "-", "")
	invalid := n.infer(ctx, unknown, false, false)
	n.check("unknown_reference_rejected", negativeNative(n.protocol, invalid), validNegative(n.protocol, invalid))
	if convOwned {
		conflict := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
		conflict["previous_response_id"], conflict["conversation"] = first.Reply.ID, convID
		out := n.infer(ctx, conflict, false, false)
		n.check("conflicting_references_rejected", negativeNative(n.protocol, out), validNegative(n.protocol, out))
		deleted := n.deleteOwned(ctx, "conversation", convID)
		n.check("conversation_metadata", deleted, deleted.State == "pass")
	} else {
		n.uncertain("conflicting_references_rejected", conversation)
	}
	path := "/v1/responses/" + url.PathEscape(first.Reply.ID)
	get := n.rest(ctx, "responses.get", path, nil, nil, false)
	n.check("owned_response_retrieve_input_items_delete", get, get.Reply.Structure && get.Reply.ID == first.Reply.ID)
	page := n.ownedPages(ctx, "responses.items", path+"/input_items", "")
	n.pageCheck("owned_response_retrieve_input_items_delete", page, false)
	deleted := n.deleteOwned(ctx, "response", first.Reply.ID)
	n.check("owned_response_retrieve_input_items_delete", deleted, deleted.State == "pass")
}

func validNegative(protocol string, out nativeOutcome) bool {
	o, err := decodeNative(out.Response.Body)
	return err == nil && nativeError(protocol, o) && out.Response.Status >= 400 && out.Response.Status < 500 && !slices.Contains([]int{401, 403, 429}, out.Response.Status)
}
func negativeNative(protocol string, out nativeOutcome) nativeOutcome {
	if validNegative(protocol, out) {
		out.State, out.Reason = "pass", "ok"
	}
	return out
}

func (n *nativeScene) conversation(ctx context.Context, created nativeOutcome) {
	id := str(created.Reply.Object["id"])
	path := "/v1/conversations/" + url.PathEscape(id)
	get := n.rest(ctx, "conversations.get", path, nil, nil, false)
	n.check("conversation_metadata", get, str(get.Reply.Object["id"]) == id && str(get.Reply.Object["object"]) == "conversation")
	update := n.rest(ctx, "conversations.update", path, nativeObject{"metadata": nativeObject{"aihub_probe": n.s.jobID, "phase": "checked"}}, nil, false)
	n.check("conversation_metadata", update, str(update.Reply.Object["id"]) == id && str(object(update.Reply.Object["metadata"])["phase"]) == "checked")
	item := nativeObject{"type": "message", "role": "user", "content": []any{nativeObject{"type": "input_text", "text": "Remember this synthetic code: " + replayCode}}}
	added := n.rest(ctx, "conversations.items.create", path+"/items", nativeObject{"items": []any{item}}, nil, false)
	data := list(added.Reply.Object["data"])
	if added.State != "pass" || len(data) != 1 || !resourceToken(str(object(data[0])["id"])) {
		n.uncertain("conversation_flow", added)
		n.uncertain("item_reference", added)
		return
	}
	itemID := str(object(data[0])["id"])
	body := baseNative(n.protocol, n.s.Target.Model, "Reply only with the synthetic code already in this conversation.")
	body["conversation"], body["store"] = id, true
	out := n.infer(ctx, body, false, false)
	if out.Reply.Structure {
		_ = n.track(ctx, out, "response", "responses.delete", "/v1/responses/")
	}
	n.linkedCheck("conversation_flow", added, out, contentMatches(out, replayCode))
	body = baseNative(n.protocol, n.s.Target.Model, "")
	body["input"] = []any{nativeObject{"type": "item_reference", "id": itemID}, nativeObject{"role": "user", "content": "Reply only with the synthetic code in the referenced message."}}
	out = n.infer(ctx, body, false, true)
	n.linkedCheck("item_reference", added, out, contentMatches(out, replayCode))
	page := n.ownedPages(ctx, "conversations.items.list", path+"/items", itemID)
	n.pageCheck("owned_item_list_get_delete", page, true)
	get = n.rest(ctx, "conversations.items.get", path+"/items/"+url.PathEscape(itemID), nil, nil, false)
	n.check("owned_item_list_get_delete", get, str(get.Reply.Object["id"]) == itemID && str(get.Reply.Object["type"]) == "message")
	deleted := n.rest(ctx, "conversations.items.delete", path+"/items/"+url.PathEscape(itemID), nil, nil, false)
	n.check("owned_item_list_get_delete", deleted, str(deleted.Reply.Object["id"]) == itemID && deleted.Reply.Object["deleted"] == true)
}
