package gateway

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
)

// unknownRelease stands in for a model's release time, which the catalog does
// not keep; Anthropic documents the epoch for an unknown release date.
var unknownRelease = time.Unix(0, 0).UTC()

// ListModels answers GET /v1/models (Anthropic shape when the request carries
// anthropic-version, as Anthropic clients always do; OpenAI shape otherwise)
// and GET /v1beta/models (Gemini shape): the models the calling key may use
// that have at least one listed channel. Aliases of such models are listed
// too, since that is the name the client sends.
func (e *Engine) ListModels(w http.ResponseWriter, r *http.Request, gemini bool) {
	ctx := r.Context()
	dialect := DialectOf(r)
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive {
		WriteError(w, dialect, http.StatusForbidden, "account_disabled", "账号已停用", "")
		return
	}
	if !keyUsable(key, e.Now()) {
		WriteError(w, dialect, http.StatusUnauthorized, "invalid_api_key", "API Key 已停用或过期", "")
		return
	}
	names, err := e.availableModels(ctx, key)
	if err != nil {
		e.Logger.Error("gateway: listing models failed", "error", err)
		WriteError(w, dialect, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return
	}
	var body map[string]any
	switch {
	case gemini:
		page, ok := geminiModelPage(names, r.URL.Query())
		if !ok {
			WriteError(w, dialect, http.StatusBadRequest, "invalid_page_token", "pageToken 无效", "")
			return
		}
		body = page
	case dialect == DialectAnthropic:
		body = anthropicModelPage(names, r.URL.Query())
	default:
		body = openAIModelList(names)
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSONBody(w, body)
}

// openAIModelList is OpenAI's unpaged {"object":"list","data":[Model]}.
func openAIModelList(names []listedModel) map[string]any {
	data := make([]map[string]any, 0, len(names))
	for _, name := range names {
		owner := name.model.Provider
		if owner == "" {
			owner = "oh-my-aihub"
		}
		data = append(data, map[string]any{"id": name.id, "object": "model", "created": unknownRelease.Unix(), "owned_by": owner})
	}
	return map[string]any{"object": "list", "data": data}
}

// anthropicModelPage pages the sorted list the way Anthropic does: limit
// (default 20, 1–1000) items after after_id or before before_id. An unknown
// cursor yields an empty page. Fields the catalog does not know are null, as
// the ModelInfo schema allows.
func anthropicModelPage(names []listedModel, query url.Values) map[string]any {
	limit := 20
	if value, err := strconv.Atoi(query.Get("limit")); err == nil {
		limit = min(max(value, 1), 1000)
	}
	var start, end int
	if before := query.Get("before_id"); before != "" {
		end = max(indexOf(names, before), 0)
		start = max(end-limit, 0)
	} else {
		if after := query.Get("after_id"); after != "" {
			if found := indexOf(names, after); found >= 0 {
				start = found + 1
			} else {
				start = len(names)
			}
		}
		end = min(start+limit, len(names))
	}
	page := names[start:end]
	data := make([]map[string]any, 0, len(page))
	for _, name := range page {
		data = append(data, map[string]any{
			"type": "model", "id": name.id, "display_name": name.model.DisplayName, "created_at": unknownRelease.Format(time.RFC3339),
			"lifecycle": "active", "deprecated_at": nil, "retires_at": nil, "line": nil, "capabilities": nil,
			"max_input_tokens": name.model.ContextWindow, "max_tokens": name.model.MaxOutputTokens,
		})
	}
	var firstID, lastID any
	if len(page) > 0 {
		firstID, lastID = page[0].id, page[len(page)-1].id
	}
	hasMore := end < len(names)
	if query.Get("before_id") != "" {
		hasMore = start > 0
	}
	return map[string]any{"data": data, "has_more": hasMore, "first_id": firstID, "last_id": lastID}
}

// geminiModelPage pages the sorted list the way models.list does: pageSize
// (default 50, at most 1000) items from pageToken, an opaque cursor naming
// the first model of the page; nextPageToken is omitted on the last page. A
// token that names no listed model is invalid.
func geminiModelPage(names []listedModel, query url.Values) (map[string]any, bool) {
	size := 50
	if value, err := strconv.Atoi(query.Get("pageSize")); err == nil && value > 0 {
		size = min(value, 1000)
	}
	start := 0
	if token := query.Get("pageToken"); token != "" {
		id, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, false
		}
		if start = indexOf(names, string(id)); start < 0 {
			return nil, false
		}
	}
	end := min(start+size, len(names))
	models := make([]map[string]any, 0, end-start)
	for _, name := range names[start:end] {
		item := map[string]any{
			"name": "models/" + name.id, "baseModelId": name.id, "version": name.id, "displayName": name.model.DisplayName,
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		}
		if name.model.ContextWindow != nil {
			item["inputTokenLimit"] = *name.model.ContextWindow
		}
		if name.model.MaxOutputTokens != nil {
			item["outputTokenLimit"] = *name.model.MaxOutputTokens
		}
		models = append(models, item)
	}
	body := map[string]any{"models": models}
	if end < len(names) {
		body["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(names[end].id))
	}
	return body, true
}

func indexOf(names []listedModel, id string) int {
	return slices.IndexFunc(names, func(name listedModel) bool { return name.id == id })
}

// listedModel is a name a client may send (a catalog ID or a key alias) with
// the catalog model it resolves to.
type listedModel struct {
	id    string
	model catalog.Model
}

func (e *Engine) availableModels(ctx context.Context, key KeyAuth) ([]listedModel, error) {
	catalogModels, err := e.Catalog.List(ctx, false)
	if err != nil {
		return nil, err
	}
	online, err := e.Store.OnlineModels(ctx)
	if err != nil {
		return nil, err
	}
	onlineSet := map[string]bool{}
	for _, id := range online {
		onlineSet[id] = true
	}
	byID := map[string]catalog.Model{}
	var result []listedModel
	for _, model := range catalogModels {
		byID[model.ID] = model
		if !onlineSet[model.ID] || (len(key.AllowedModels) > 0 && !containsString(key.AllowedModels, model.ID)) {
			continue
		}
		result = append(result, listedModel{id: model.ID, model: model})
	}
	for alias, target := range key.Aliases {
		if model, ok := byID[target]; ok && onlineSet[target] && (len(key.AllowedModels) == 0 || containsString(key.AllowedModels, target)) {
			result = append(result, listedModel{id: alias, model: model})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

// RunReaper marks calls that have been in progress longer than any call can
// legitimately run as interrupted, without charging them (a crash in the
// middle of a stream loses that charge by design). It returns when ctx ends.
func (e *Engine) RunReaper(ctx context.Context, interval time.Duration) {
	// The longest per-channel total timeout is one hour; allow a minute more.
	const horizon = time.Hour + time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		changed, err := e.Store.SweepStaleCalls(ctx, e.Now().Add(-horizon))
		if err != nil {
			e.Logger.Error("gateway: sweeping stale calls failed", "error", err)
		} else if changed > 0 {
			e.Logger.Warn("gateway: marked stale calls as interrupted", "calls", changed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
