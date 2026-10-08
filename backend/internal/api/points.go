package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

func (a *app) getPoints(w http.ResponseWriter, r *http.Request) {
	from, fromOK := parseTimeParam(r, "from")
	to, toOK := parseTimeParam(r, "to")
	if !fromOK || !toOK {
		writeInvalidQuery(w)
		return
	}
	accountID := accountFromContext(r.Context()).ID
	points, err := a.ledger.Points(r.Context(), accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	report, err := a.observe.Points(r.Context(), accountID, from, to)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	trend := make([]map[string]any, 0, len(report.Trend))
	for _, point := range report.Trend {
		trend = append(trend, map[string]any{"date": point.Day, "balance": point.Balance.String(), "income": point.Income.String(), "spend": point.Spend.String()})
	}
	period := report.Period
	writeJSON(w, http.StatusOK, map[string]any{
		"balance":      points.Balance.String(),
		"credit_limit": points.CreditLimit.String(),
		"available":    points.Available().String(),
		"updated_at":   points.UpdatedAt,
		"trend":        trend,
		"period": map[string]any{
			"from": period.From, "to": period.To, "opening_balance": period.Opening.String(), "closing_balance": period.Closing.String(),
			"income": period.Income.String(), "spend": period.Spend.String(), "call_spend": period.CallSpend.String(),
			"channel_income": period.ChannelIncome.String(), "c2c_buy": period.C2CBuy.String(), "c2c_sell": period.C2CSell.String(),
			"c2c_return": period.C2CReturn.String(), "adjustments": period.Adjustments.String(), "write_offs": period.WriteOffs.String(),
			"difference": period.Difference.String(),
		},
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
	exportCSV := false
	switch query.Get("format") {
	case "", "json":
	case "csv":
		exportCSV = true
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "format 无效")
		return
	}
	group := query.Get("group")
	if group != "" && group != "day" && group != "key" {
		writeInvalidQuery(w)
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
	if ledgerType := query.Get("type"); ledgerType != "" && !ledger.TransactionType(ledgerType).Valid() {
		writeInvalidQuery(w)
		return
	}
	if exportCSV {
		a.exportPointsEntries(w, r, observe.EntryFilter{
			AccountID: accountFromContext(r.Context()).ID, Type: query.Get("type"), APIKeyID: keyID, From: from, To: to,
		})
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
	response := map[string]any{"items": items, "next_cursor": nextCursor(hasMore, cursor)}
	if group != "" {
		summary, err := a.observe.EntriesSummary(r.Context(), observe.EntryFilter{
			AccountID: accountFromContext(r.Context()).ID, Type: query.Get("type"), APIKeyID: keyID, From: from, To: to,
		}, group)
		if err != nil {
			writeObserveError(w, err)
			return
		}
		response["summary"] = entrySummaryJSON(summary)
	}
	writeJSON(w, http.StatusOK, response)
}

func entrySummaryJSON(summary observe.EntrySummary) map[string]any {
	byDay := make([]map[string]any, 0, len(summary.ByDay))
	for _, day := range summary.ByDay {
		byDay = append(byDay, map[string]any{"date": day.Day, "income": day.Income.String(), "spend": day.Spend.String(), "net": day.Net.String()})
	}
	byKey := make([]map[string]any, 0, len(summary.ByKey))
	for _, key := range summary.ByKey {
		byKey = append(byKey, map[string]any{"api_key": keyRefJSON(key.Key), "spend": key.Spend.String(), "entries": key.Entries})
	}
	return map[string]any{"by_day": byDay, "by_key": byKey}
}

// csvTypeLabel names an entry the way the user's statement shows it.
func csvTypeLabel(entry observe.ExportEntry) string {
	switch entry.Type {
	case "api_call":
		if entry.Amount < 0 {
			return "调用支出"
		}
		return "渠道收入"
	case "c2c_list":
		return "C2C 挂单转入托管"
	case "c2c_release":
		return "C2C 买入"
	case "c2c_return":
		return "C2C 退回"
	case "admin_adjust":
		return "管理员调账"
	case "bad_debt_writeoff":
		return "坏账核销"
	}
	return entry.Type
}

// csvText neutralizes spreadsheet formula injection in free text cells.
func csvText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

// exportPointsEntries writes the matching entries as a UTF-8 CSV with a byte
// order mark, so spreadsheet software opens the Chinese text correctly.
func (a *app) exportPointsEntries(w http.ResponseWriter, r *http.Request, filter observe.EntryFilter) {
	entries, err := a.observe.ExportEntries(r.Context(), filter)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="points-entries.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("\ufeff"))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"时间", "类型", "说明", "关联", "变动", "余额", "Key"})
	for _, entry := range entries {
		related := ""
		if entry.RelatedType != "" {
			related = entry.RelatedType + ":" + entry.RelatedID
		}
		_ = writer.Write([]string{
			entry.CreatedAt.In(localtime.Zone).Format("2006-01-02 15:04:05"), csvTypeLabel(entry), csvText(entry.Reason), related,
			entry.Amount.String(), entry.BalanceAfter.String(), csvText(entry.KeyName),
		})
	}
	writer.Flush()
}

// registerPointsRoutes 注册用户积分路由。
func (a *app) registerPointsRoutes(r *router) {
	r.ready("GET /api/points", a.getPoints)
	r.ready("GET /api/points/entries", a.listPointsEntries)
}
