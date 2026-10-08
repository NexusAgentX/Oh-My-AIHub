package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func (a *app) getPoints(w http.ResponseWriter, r *http.Request) {
	points, err := a.ledger.Points(r.Context(), accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"balance":      points.Balance.String(),
		"credit_limit": points.CreditLimit.String(),
		"available":    points.Available().String(),
		"updated_at":   points.UpdatedAt,
	})
}

func pointsEntryResponse(entry ledger.EntryView) map[string]any {
	var related, apiKey any
	if entry.RelatedType != "" {
		related = map[string]any{"type": entry.RelatedType, "id": entry.RelatedID}
	}
	if entry.APIKeyID != nil {
		name := ""
		if entry.APIKeyName != nil {
			name = *entry.APIKeyName
		}
		apiKey = map[string]any{"id": *entry.APIKeyID, "name": name}
	}
	return map[string]any{
		"id": strconv.FormatInt(entry.ID, 10), "transaction_id": entry.TransactionID, "created_at": entry.CreatedAt,
		"type": entry.Type, "reason": entry.Reason, "related": related,
		"amount": entry.Amount.String(), "balance_after": entry.BalanceAfter.String(), "api_key": apiKey,
	}
}

func parseTimeParam(r *http.Request, name string) (*time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, false
	}
	return &value, true
}

func (a *app) listPointsEntries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	switch query.Get("format") {
	case "", "json":
	case "csv":
		// CSV 导出由 Feature G 实现。
		notImplemented(w, r)
		return
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "format 无效")
		return
	}
	limit, limitOK := pageLimit(r)
	before, cursorOK := idCursor(r)
	from, fromOK := parseTimeParam(r, "from")
	to, toOK := parseTimeParam(r, "to")
	keyID := query.Get("api_key_id")
	if !limitOK || !cursorOK || !fromOK || !toOK || (keyID != "" && !uuidPattern.MatchString(keyID)) {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	entries, err := a.ledger.Entries(r.Context(), ledger.EntryFilter{
		AccountID: accountFromContext(r.Context()).ID, Type: ledger.TransactionType(query.Get("type")),
		APIKeyID: keyID, From: from, To: to, BeforeID: before, Limit: limit + 1,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	items := make([]map[string]any, 0, len(entries))
	cursor := ""
	for _, entry := range entries {
		items = append(items, pointsEntryResponse(entry))
		cursor = strconv.FormatInt(entry.ID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nextCursor(hasMore, cursor)})
}

// registerPointsRoutes 注册用户积分路由。
func (a *app) registerPointsRoutes(r *router) {
	r.ready("GET /api/points", a.getPoints)
	r.ready("GET /api/points/entries", a.listPointsEntries)
}
