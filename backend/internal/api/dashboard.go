package api

import (
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/dashboard"
)

func (a *app) listPendingItems(w http.ResponseWriter, r *http.Request) {
	items, err := a.dashboard.PendingItems(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pendingItemListResponse(items))
}

func pendingItemListResponse(items []dashboard.PendingItem) map[string]any {
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, map[string]any{
			"id": item.ID, "kind": item.Kind, "label": item.Label, "tone": item.Tone,
			"title": item.Title, "detail": item.Detail, "to": item.To,
		})
	}
	return map[string]any{"items": responses}
}

func (a *app) registerDashboardRoutes(r *router) {
	r.ready("GET /api/dashboard/pending-items", a.listPendingItems)
}
