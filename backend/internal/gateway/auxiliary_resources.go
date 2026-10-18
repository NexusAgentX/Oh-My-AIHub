package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func (e *Engine) observeAuxiliary(ctx context.Context, ep gatewaycontract.Endpoint, c Candidate, key KeyAuth, model, operation, parent string, body []byte, owned *resourcestate.Record) error {
	var root map[string]json.RawMessage
	if len(body) == 0 {
		if strings.HasSuffix(ep.ID, ".delete") && owned != nil {
			return e.Resources.Delete(ctx, principal(key), gatewaycontract.Reference{Kind: owned.Kind, ID: owned.ID, ParentID: owned.ParentID}, owned.Binding)
		}
		return nil
	}
	if json.Unmarshal(body, &root) != nil {
		return errors.New("invalid native resource response")
	}
	if raw := root["error"]; len(raw) > 0 && string(raw) != "null" {
		return nil
	}
	text := func(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
	if owned != nil && (strings.HasSuffix(ep.ID, ".get") || strings.HasSuffix(ep.ID, ".update") || strings.HasSuffix(ep.ID, ".delete") || ep.ID == "responses.cancel") {
		id := text(root["id"])
		if owned.Kind == "cache" {
			id = text(root["name"])
		}
		if id != owned.ID && (!strings.HasSuffix(ep.ID, ".delete") || id != "") {
			return gatewaycontract.ErrConflict
		}
	}
	if (ep.ID == "responses.get" || ep.ID == "responses.cancel") && owned != nil {
		call := &callState{id: owned.CreationID, key: key, modelID: owned.ModelID, resourceObserver: &resourceObserver{format: channel.FormatOpenAIResponses, store: true, responseID: owned.ID, expectedResponseID: owned.ID}}
		if err := e.registerResources(call, c, body); err != nil {
			return err
		}
	}
	rows := []resourcestate.Record{}
	add := func(kind, id, p string) {
		rows = append(rows, resourcestate.Record{Resource: gatewaycontract.Resource{Kind: kind, ID: id, ParentID: p, AccountID: key.OwnerID, KeyID: key.KeyID, Binding: candidateBinding(c), CredentialScope: c.CredentialScope, CreationID: operation}, ModelID: model})
	}
	switch ep.ID {
	case "conversations.create":
		if text(root["object"]) != "conversation" || text(root["id"]) == "" {
			return errors.New("conversation producer missing identity")
		}
		// A model-less conversation has no invented model context. The selected
		// candidate is an authorization witness, not the resource's pricing model.
		add("conversation", text(root["id"]), "")
	case "conversations.items.create":
		var data []map[string]json.RawMessage
		if parent == "" || json.Unmarshal(root["data"], &data) != nil || len(data) > 100 {
			return errors.New("invalid conversation items producer")
		}
		for _, v := range data {
			if text(v["id"]) == "" {
				return errors.New("item producer missing identity")
			}
			add("item", text(v["id"]), parent)
		}
	case "cache.create":
		name := text(root["name"])
		if !strings.HasPrefix(name, "cachedContents/") || strings.TrimPrefix(name, "cachedContents/") == "" {
			return errors.New("cache producer missing identity")
		}
		add("cache", name, "")
	default:
		if strings.HasSuffix(ep.ID, ".delete") && owned != nil {
			if text(root["id"]) == owned.ID && string(root["deleted"]) == "true" {
				return e.Resources.Delete(ctx, principal(key), gatewaycontract.Reference{Kind: owned.Kind, ID: owned.ID, ParentID: owned.ParentID}, owned.Binding)
			}
			return nil
		}
	}
	if len(rows) > 0 {
		return e.Resources.Observe(ctx, rows)
	}
	return nil
}

func pageQueryDigest(r *http.Request, ep gatewaycontract.Endpoint, model string) string {
	q := r.URL.Query()
	for _, k := range []string{"key", "after", "pageToken"} {
		q.Del(k)
	}
	sum := sha256.Sum256([]byte(ep.ID + "\n" + q.Encode() + "\n" + model))
	return hex.EncodeToString(sum[:])
}
func (e *Engine) ownedResourcePage(ctx context.Context, r *http.Request, ep gatewaycontract.Endpoint, c Candidate, key KeyAuth, location authLocation, policy channel.Outbound, defaults settings.Settings, pref routing.Pref, parent string, prior *gatewaycontract.CursorScope, token string, producer *resourcestate.Record, budget *int) ([]byte, *http.Response, error) {
	kind := resourceListKind(ep)
	parent = resourceListParent(ep, r, parent)
	scope := gatewaycontract.CursorScope{AccountID: key.OwnerID, KeyID: key.KeyID, Kind: kind, ParentID: parent, Binding: candidateBinding(c), QueryDigest: pageQueryDigest(r, ep, pref.ModelID)}
	if prior != nil && *prior != scope {
		return nil, nil, gatewaycontract.ErrPage
	}
	limit := 20
	limitParam := "limit"
	if ep.ID == "cache.list" {
		limitParam = "pageSize"
	}
	if raw := r.URL.Query().Get(limitParam); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > gatewaycontract.MaxPageItems {
			return nil, nil, gatewaycontract.ErrPage
		}
		limit = v
	}
	var last *http.Response
	owned, err := gatewaycontract.ReadOwnedPage(ctx, principal(key), scope, token, limit, e.ResourceCursors,
		func(ctx context.Context, native string, count int) (gatewaycontract.NativePage, error) {
			req := r.Clone(ctx)
			u := *r.URL
			req.URL = &u
			q := u.Query()
			q.Del("after")
			q.Del("pageToken")
			before := r.URL.Query().Get("before")
			q.Del("before")
			q.Set(limitParam, strconv.Itoa(count))
			if native != "" {
				if ep.ID == "cache.list" {
					q.Set("pageToken", native)
				} else if before != "" {
					q.Set("before", native)
				} else {
					q.Set("after", native)
				}
			}
			if native == "" && before != "" {
				q.Set("before", before)
			}
			u.RawQuery = q.Encode()
			// The resource-page body limit is enforced while reading, before JSON
			// allocation. Pagination does not multiply a caller's channel/RPM slot.
			payload, response, err := e.auxiliaryExchange(ctx, req, ep, c, location, policy, defaults, pref, nil, budget)
			if err != nil {
				return gatewaycontract.NativePage{}, err
			}
			last = response
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return gatewaycontract.NativePage{}, errors.New("upstream list failed")
			}
			if len(payload) > gatewaycontract.MaxPageBytes {
				return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
			}
			var root map[string]json.RawMessage
			if json.Unmarshal(payload, &root) != nil {
				return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
			}
			dataKey := "data"
			if ep.ID == "cache.list" {
				dataKey = "cachedContents"
			}
			var data []json.RawMessage
			if v := root[dataKey]; len(v) > 0 {
				if json.Unmarshal(v, &data) != nil {
					return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
				}
			} else if ep.ID != "cache.list" {
				return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
			}
			if len(data) > count {
				return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
			}
			page := gatewaycontract.NativePage{Items: []gatewaycontract.ListedResource{}}
			var lastID, firstID string
			for _, raw := range data {
				var item struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				if json.Unmarshal(raw, &item) != nil {
					return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
				}
				id := item.ID
				if ep.ID == "cache.list" {
					id = item.Name
				}
				if id == "" || len(id) > 2048 {
					return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
				}
				lastID = id
				if firstID == "" {
					firstID = id
				}
				page.Items = append(page.Items, gatewaycontract.ListedResource{Reference: gatewaycontract.Reference{Kind: kind, ID: id, ParentID: parent}, Raw: raw})
			}
			// Input/stored-chat messages receive their native IDs only when the
			// API first exposes this owned parent's children. This is an extension
			// of its verified create chain, never a claim from an account-wide list.
			if kind == "item" && producer != nil && parent != "" {
				fresh := []resourcestate.Record{}
				for _, item := range page.Items {
					prior, err := e.Resources.Lookup(ctx, item.Reference)
					if err != nil {
						return gatewaycontract.NativePage{}, err
					}
					if len(prior) > 0 {
						continue
					}
					fresh = append(fresh, resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "item", ID: item.Reference.ID, ParentID: parent, AccountID: key.OwnerID, KeyID: key.KeyID, Binding: candidateBinding(c), CredentialScope: c.CredentialScope, CreationID: producer.CreationID + "/observed-child"}, ModelID: producer.ModelID})
				}
				if err := e.Resources.Observe(ctx, fresh); err != nil {
					return gatewaycontract.NativePage{}, err
				}
			}
			if ep.ID == "cache.list" {
				_ = json.Unmarshal(root["nextPageToken"], &page.Next)
			} else {
				var more bool
				_ = json.Unmarshal(root["has_more"], &more)
				if more {
					if lastID == "" {
						return gatewaycontract.NativePage{}, gatewaycontract.ErrPage
					}
					page.Next = lastID
					if before != "" {
						page.Next = firstID
					}
				}
			}
			page.Stop = *budget <= 0
			return page, nil
		}, func(ref gatewaycontract.Reference) ([]gatewaycontract.Resource, error) {
			rows, err := e.Resources.Lookup(ctx, ref)
			if err != nil {
				return nil, err
			}
			if len(rows) > 16 {
				return nil, gatewaycontract.ErrAmbiguous
			}
			out := []gatewaycontract.Resource{}
			for _, row := range rows {
				if row.Quarantined || row.Deleted {
					continue
				}
				if row.ModelID != "" {
					model, err := e.Catalog.Get(ctx, row.ModelID)
					if err != nil || !model.Enabled {
						continue
					}
				}
				if ep.ID == "chat.list" && r.URL.Query().Get("model") != "" && row.ModelID != pref.ModelID {
					continue
				}
				if row.Binding == scope.Binding && row.CredentialScope == c.CredentialScope && (row.ModelID == "" || len(key.AllowedModels) == 0 || containsString(key.AllowedModels, row.ModelID)) {
					out = append(out, row.Resource)
				}
			}
			return out, nil
		})
	if err != nil {
		return nil, nil, err
	}
	if last == nil {
		return nil, nil, errors.New("missing upstream page")
	}
	next, err := e.ResourceCursors.WrapPageCursor(scope, owned.Next)
	if err != nil {
		return nil, nil, err
	}
	data := make([]json.RawMessage, 0, len(owned.Items))
	first, lastID := "", ""
	for i, v := range owned.Items {
		data = append(data, v.Raw)
		if i == 0 {
			first = v.Reference.ID
		}
		lastID = v.Reference.ID
	}
	var output any
	if ep.ID == "cache.list" {
		output = struct {
			CachedContents []json.RawMessage `json:"cachedContents"`
			NextPageToken  string            `json:"nextPageToken,omitempty"`
		}{data, next}
	} else {
		// first_id/last_id refer only to visible objects. next_cursor is the opaque
		// continuation, including an empty bounded page; pass it as `after`.
		output = struct {
			Object     string            `json:"object"`
			Data       []json.RawMessage `json:"data"`
			FirstID    string            `json:"first_id,omitempty"`
			LastID     string            `json:"last_id,omitempty"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor,omitempty"`
		}{"list", data, first, lastID, next != "", next}
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return nil, nil, err
	}
	// Filtered lists are a permitted isolation response: shared totals, cursors
	// and arbitrary top-level engine metadata are never copied back.
	last.Header = http.Header{"Content-Type": []string{"application/json"}}
	last.StatusCode = http.StatusOK
	return payload, last, nil
}

func resourceListParent(ep gatewaycontract.Endpoint, r *http.Request, fallback string) string {
	if ep.ID == "responses.items" {
		return r.PathValue("responseID")
	}
	if ep.ID == "chat.messages" {
		return r.PathValue("completionID")
	}
	return fallback
}
