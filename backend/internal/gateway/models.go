package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// ListModels answers GET /v1/models (OpenAI shape) and GET /v1beta/models
// (Gemini shape): the models the calling key may use that have at least one
// listed channel. Aliases of such models are listed too, since that is the
// name the client sends.
func (e *Engine) ListModels(w http.ResponseWriter, r *http.Request, gemini bool) {
	ctx := r.Context()
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive {
		writeGatewayError(w, http.StatusForbidden, "account_disabled", "账号已停用", "")
		return
	}
	if !keyUsable(key, e.Now()) {
		writeGatewayError(w, http.StatusUnauthorized, "invalid_api_key", "API Key 已停用或过期", "")
		return
	}
	names, err := e.availableModels(ctx, key)
	if err != nil {
		e.Logger.Error("gateway: listing models failed", "error", err)
		writeGatewayError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if gemini {
		models := make([]map[string]any, 0, len(names))
		for _, name := range names {
			models = append(models, map[string]any{
				"name": "models/" + name.id, "displayName": name.display,
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			})
		}
		writeJSONBody(w, map[string]any{"models": models})
		return
	}
	data := make([]map[string]any, 0, len(names))
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, name := range names {
		data = append(data, map[string]any{"id": name.id, "object": "model", "created": created, "owned_by": "oh-my-aihub"})
	}
	writeJSONBody(w, map[string]any{"object": "list", "data": data})
}

type listedModel struct{ id, display string }

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
	display := map[string]string{}
	var result []listedModel
	for _, model := range catalogModels {
		display[model.ID] = model.DisplayName
		if !onlineSet[model.ID] || (len(key.AllowedModels) > 0 && !containsString(key.AllowedModels, model.ID)) {
			continue
		}
		result = append(result, listedModel{id: model.ID, display: model.DisplayName})
	}
	for alias, target := range key.Aliases {
		if shown, ok := display[target]; ok && onlineSet[target] && (len(key.AllowedModels) == 0 || containsString(key.AllowedModels, target)) {
			result = append(result, listedModel{id: alias, display: shown})
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
