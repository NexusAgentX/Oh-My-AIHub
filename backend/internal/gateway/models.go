package gateway

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// unknownRelease stands in for a model's release time, which the catalog does
// not keep; Anthropic documents the epoch for an unknown release date.
var unknownRelease = time.Unix(0, 0).UTC()

// ListModels returns the OpenAI catalog shape for the two supported protocols.
func (e *Engine) ListModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dialect := DialectOpenAI
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
	names, err := e.availableModels(ctx, key, channel.Formats)
	if err != nil {
		e.Logger.Error("gateway: listing models failed", "error", err)
		WriteError(w, dialect, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return
	}
	body := openAIModelList(names)
	w.Header().Set("Content-Type", "application/json")
	writeJSONBody(w, body)
}

// GetModel uses exactly the same visibility rules and representation as the
// OpenAI model list; it never contacts an upstream.
func (e *Engine) GetModel(w http.ResponseWriter, r *http.Request) {
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		WriteError(w, DialectOpenAI, http.StatusUnauthorized, "invalid_api_key", "API Key 或账号已停用或过期", "")
		return
	}
	names, err := e.availableModels(r.Context(), key, channel.Formats)
	if err != nil {
		WriteError(w, DialectOpenAI, 500, "internal_error", "读取模型失败", "")
		return
	}
	at := indexOf(names, r.PathValue("model"))
	if at < 0 {
		WriteError(w, DialectOpenAI, 404, "model_not_found", "模型不存在或不可用", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSONBody(w, openAIModelList(names[at : at+1])["data"].([]map[string]any)[0])
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

func indexOf(names []listedModel, id string) int {
	return slices.IndexFunc(names, func(name listedModel) bool { return name.id == id })
}

// listedModel is a name a client may send (a catalog ID or a key alias) with
// the catalog model it resolves to.
type listedModel struct {
	id    string
	model catalog.Model
}

// availableModels are the names the key may send that a channel serves in one
// of formats. An alias follows the model it points to.
func (e *Engine) availableModels(ctx context.Context, key KeyAuth, formats []channel.Format) ([]listedModel, error) {
	catalogModels, err := e.Catalog.List(ctx, false)
	if err != nil {
		return nil, err
	}
	online, err := e.Store.OnlineModels(ctx)
	if err != nil {
		return nil, err
	}
	onlineSet := map[string]bool{}
	byID := map[string]catalog.Model{}
	var result []listedModel
	allowed := func(id string) bool { return len(key.AllowedModels) == 0 || containsString(key.AllowedModels, id) }
	for _, model := range online {
		if !slices.ContainsFunc(model.Formats, func(format channel.Format) bool { return slices.Contains(formats, format) }) {
			continue
		}
		onlineSet[model.ID] = true
		if model.Custom {
			// Custom models (ADR-0034) are listed under their own name only.
			custom := catalog.Model{ID: model.ID, DisplayName: model.ID}
			byID[model.ID] = custom
			if allowed(model.ID) {
				result = append(result, listedModel{id: model.ID, model: custom})
			}
		}
	}
	for _, model := range catalogModels {
		byID[model.ID] = model
		if !onlineSet[model.ID] || !allowed(model.ID) {
			continue
		}
		result = append(result, listedModel{id: model.ID, model: model})
	}
	for alias, target := range key.Aliases {
		if model, ok := byID[target]; ok && onlineSet[target] && allowed(target) {
			result = append(result, listedModel{id: alias, model: model})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

// callTimeLimit is the longest one call may run, from its start to its end and
// every attempt and fallback included (ADR-0035 rule 5). RunReaper's horizon
// is derived from it: change one and the other follows.
const callTimeLimit = 60 * time.Minute

// reaperMargin is the slack RunReaper allows beyond callTimeLimit before it
// treats a call that is still in progress as lost.
const reaperMargin = time.Minute

// RunReaper marks calls that have been in progress longer than any call can
// legitimately run as interrupted, without charging them (a crash in the
// middle of a stream loses that charge by design). A live call is cut off by
// the call time limit, so only a call whose process died outlives
// callTimeLimit + reaperMargin. It returns when ctx ends.
func (e *Engine) RunReaper(ctx context.Context, interval time.Duration) {
	horizon := e.CallTimeLimit + reaperMargin
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
