package detection

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

func responseTerminal(status string) bool {
	return slices.Contains([]string{"completed", "failed", "incomplete", "cancelled"}, status)
}

// A resumed stream can start after response.created or a partial item. Validate
// its cursor/order and authoritative final response without inventing the
// omitted prefix or claiming this fragment proves the full text lifecycle.
func resumedResponse(body []byte, id string, after int64, limit int) (nativeReply, bool) {
	events, err := nativeEvents(body, limit)
	if err != nil || len(events) == 0 {
		return nativeReply{}, false
	}
	var final nativeReply
	sequence := after
	terminal := false
	for _, event := range events {
		o, e := decodeNative(event.Data)
		kind := str(o["type"])
		n, ok := fieldInt(o, "sequence_number")
		if e != nil || terminal || !ok || n <= sequence || !strings.HasPrefix(kind, "response.") || event.Name != "" && event.Name != kind {
			return nativeReply{}, false
		}
		sequence = n
		if slices.Contains([]string{"response.completed", "response.failed", "response.incomplete", "response.cancelled"}, kind) {
			final = parseNative("openai-responses", object(o["response"]))
			terminal = final.Structure && final.ID == id && responseTerminal(final.Status)
			if !terminal {
				return nativeReply{}, false
			}
		}
	}
	return final, terminal
}

func (n *nativeScene) background(ctx context.Context) {
	body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
	body["background"], body["store"] = true, true
	first := n.infer(ctx, body, false, false)
	owned := first.Reply.Structure && n.track(ctx, first, "response", "responses.delete", "/v1/responses/")
	n.check("background_create", first, owned && (first.Reply.Status == "queued" || first.Reply.Status == "in_progress" || responseTerminal(first.Reply.Status)))
	if !owned {
		return
	}
	id, path := first.Reply.ID, "/v1/responses/"+url.PathEscape(first.Reply.ID)
	last := first
	for range 8 {
		last = n.rest(ctx, "responses.get", path, nil, nil, false)
		n.check("owned_retrieve_input_items_delete", last, last.Reply.Structure && last.Reply.ID == id)
		if last.State != "pass" || responseTerminal(last.Reply.Status) || ctx.Err() != nil {
			break
		}
	}
	resumed := n.rest(ctx, "responses.get", path, nil, url.Values{"stream": {"true"}, "starting_after": {"0"}}, false)
	reply, valid := resumedResponse(resumed.Response.Body, id, 0, n.s.limits.EventBytes)
	// The generic full-stream parser deliberately rejects an omitted prefix.
	// This operation has its own fragment validator and does not recount usage.
	if valid && resumed.Response.Status >= 200 && resumed.Response.Status < 300 {
		resumed.State, resumed.Reason = "pass", "ok"
	}
	n.check("resume_from_sequence", resumed, valid && reply.Status == "completed" && reply.Usage.Reliable && strings.Contains(resumed.Response.Headers.Get("Content-Type"), "text/event-stream"))
	final := last
	if valid && reply.Status == "completed" && reply.Usage.Reliable {
		final = resumed
		final.Reply = reply
	} else if first.Reply.Status == "completed" && first.Reply.Usage.Reliable {
		final = first
	}
	if final.Reply.Status == "completed" && final.Reply.Usage.Reliable && contentMatches(final, nativeMarker) {
		ok := true
		if !responseTerminal(first.Reply.Status) || !first.Reply.Usage.Reliable {
			ok = n.s.Meter(final.Reply.Usage.Input, final.Reply.Usage.Output, 0, 0) == nil
		}
		n.linkedCheck("terminal_usage_once", first, final, ok)
	} else {
		n.uncertain("terminal_usage_once", final)
	}
	page := n.ownedPages(ctx, "responses.items", path+"/input_items", "")
	n.pageCheck("owned_retrieve_input_items_delete", page, false)
	deleted := n.deleteOwned(ctx, "response", id)
	n.check("owned_retrieve_input_items_delete", deleted, deleted.State == "pass")
	n.check("cleanup_only_owned_resources", deleted, deleted.State == "pass")
	if ctx.Err() != nil {
		return
	}
	cancelBody := baseNative(n.protocol, n.s.Target.Model, "Write the integers from 1 to 10000 until the output token limit. No tools or explanation.")
	outputLimit(n.protocol, cancelBody, 1024)
	cancelBody["background"], cancelBody["store"] = true, true
	active := n.infer(ctx, cancelBody, false, false)
	if !active.Reply.Structure || !n.track(ctx, active, "response", "responses.delete", "/v1/responses/") {
		n.uncertain("owned_cancel", active)
		return
	}
	path = "/v1/responses/" + url.PathEscape(active.Reply.ID)
	cancelled := n.rest(ctx, "responses.cancel", path+"/cancel", nil, nil, false)
	if responseTerminal(active.Reply.Status) && cancelled.Response.Status == 400 {
		cancelled.State, cancelled.Reason = "inconclusive", "not_observed"
	}
	n.linkedCheck("owned_cancel", active, cancelled, cancelled.Reply.Structure && cancelled.Reply.ID == active.Reply.ID && cancelled.Reply.Status == "cancelled")
	if !responseTerminal(active.Reply.Status) && cancelled.Reply.ID == active.Reply.ID && responseTerminal(cancelled.Reply.Status) && cancelled.Reply.Usage.Reliable {
		_ = n.s.Meter(cancelled.Reply.Usage.Input, cancelled.Reply.Usage.Output, 0, 0)
	}
	deleted = n.deleteOwned(ctx, "response", active.Reply.ID)
	n.check("cleanup_only_owned_resources", deleted, deleted.State == "pass")
}
