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

// pointsTrendPointJSON is one element of the OpenAPI Points schema's trend.
type pointsTrendPointJSON struct {
	Date    string `json:"date"`
	Balance string `json:"balance"`
	Income  string `json:"income"`
	Spend   string `json:"spend"`
}

// pointsPeriodJSON is the OpenAPI PointsPeriod schema.
type pointsPeriodJSON struct {
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	OpeningBalance string    `json:"opening_balance"`
	ClosingBalance string    `json:"closing_balance"`
	Income         string    `json:"income"`
	Spend          string    `json:"spend"`
	CallSpend      string    `json:"call_spend"`
	ChannelIncome  string    `json:"channel_income"`
	C2CBuy         string    `json:"c2c_buy"`
	C2CSell        string    `json:"c2c_sell"`
	C2CReturn      string    `json:"c2c_return"`
	Adjustments    string    `json:"adjustments"`
	WriteOffs      string    `json:"write_offs"`
	Difference     string    `json:"difference"`
}

// pointsSummaryJSON is the OpenAPI PointsSummary schema.
type pointsSummaryJSON struct {
	Balance     string `json:"balance"`
	CreditLimit string `json:"credit_limit"`
	Available   string `json:"available"`
}

// pointsJSON is the OpenAPI Points schema.
type pointsJSON struct {
	Balance     string                 `json:"balance"`
	CreditLimit string                 `json:"credit_limit"`
	Available   string                 `json:"available"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Trend       []pointsTrendPointJSON `json:"trend"`
	Period      pointsPeriodJSON       `json:"period"`
}

// ledgerRelatedJSON is the OpenAPI LedgerRelated schema.
type ledgerRelatedJSON struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// pointsEntryJSON is the OpenAPI PointsEntry schema.
type pointsEntryJSON struct {
	ID            string                 `json:"id"`
	TransactionID string                 `json:"transaction_id"`
	CreatedAt     time.Time              `json:"created_at"`
	Type          ledger.TransactionType `json:"type"`
	Reason        string                 `json:"reason"`
	Related       *ledgerRelatedJSON     `json:"related"`
	Amount        string                 `json:"amount"`
	BalanceAfter  string                 `json:"balance_after"`
	APIKey        *apiKeyRefJSON         `json:"api_key"`
}

// pointsDayFlowJSON is one by_day element of the OpenAPI PointsEntrySummary schema.
type pointsDayFlowJSON struct {
	Date   string `json:"date"`
	Income string `json:"income"`
	Spend  string `json:"spend"`
	Net    string `json:"net"`
}

// pointsKeySpendJSON is one by_key element of the OpenAPI PointsEntrySummary schema.
type pointsKeySpendJSON struct {
	APIKey  *apiKeyRefJSON `json:"api_key"`
	Spend   string         `json:"spend"`
	Entries int64          `json:"entries"`
}

// pointsEntrySummaryJSON is the OpenAPI PointsEntrySummary schema.
type pointsEntrySummaryJSON struct {
	ByDay []pointsDayFlowJSON  `json:"by_day"`
	ByKey []pointsKeySpendJSON `json:"by_key"`
}

// pointsEntryPageJSON is the OpenAPI PointsEntryPage schema; summary is only present when grouped.
type pointsEntryPageJSON struct {
	Items      []pointsEntryJSON       `json:"items"`
	NextCursor *string                 `json:"next_cursor"`
	Summary    *pointsEntrySummaryJSON `json:"summary,omitempty"`
}

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
	trend := make([]pointsTrendPointJSON, 0, len(report.Trend))
	for _, point := range report.Trend {
		trend = append(trend, pointsTrendPointJSON{
			Date: point.Day, Balance: point.Balance.String(), Income: point.Income.String(), Spend: point.Spend.String(),
		})
	}
	period := report.Period
	writeJSON(w, http.StatusOK, pointsJSON{
		Balance:     points.Balance.String(),
		CreditLimit: points.CreditLimit.String(),
		Available:   points.Available().String(),
		UpdatedAt:   points.UpdatedAt,
		Trend:       trend,
		Period: pointsPeriodJSON{
			From: period.From, To: period.To, OpeningBalance: period.Opening.String(), ClosingBalance: period.Closing.String(),
			Income: period.Income.String(), Spend: period.Spend.String(), CallSpend: period.CallSpend.String(),
			ChannelIncome: period.ChannelIncome.String(), C2CBuy: period.C2CBuy.String(), C2CSell: period.C2CSell.String(),
			C2CReturn: period.C2CReturn.String(), Adjustments: period.Adjustments.String(), WriteOffs: period.WriteOffs.String(),
			Difference: period.Difference.String(),
		},
	})
}

func newPointsEntryJSON(entry ledger.EntryView) pointsEntryJSON {
	var related *ledgerRelatedJSON
	if entry.RelatedType != "" {
		related = &ledgerRelatedJSON{Type: entry.RelatedType, ID: entry.RelatedID}
	}
	return pointsEntryJSON{
		ID:            strconv.FormatInt(entry.ID, 10),
		TransactionID: entry.TransactionID,
		CreatedAt:     entry.CreatedAt,
		Type:          entry.Type,
		Reason:        entry.Reason,
		Related:       related,
		Amount:        entry.Amount.String(),
		BalanceAfter:  entry.BalanceAfter.String(),
		APIKey:        newApiKeyRefJSON(entry.APIKeyID, entry.APIKeyName),
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
	items := make([]pointsEntryJSON, 0, len(entries))
	cursor := ""
	for _, entry := range entries {
		items = append(items, newPointsEntryJSON(entry))
		cursor = strconv.FormatInt(entry.ID, 10)
	}
	response := pointsEntryPageJSON{Items: items, NextCursor: nextCursor(hasMore, cursor)}
	if group != "" {
		summary, err := a.observe.EntriesSummary(r.Context(), observe.EntryFilter{
			AccountID: accountFromContext(r.Context()).ID, Type: query.Get("type"), APIKeyID: keyID, From: from, To: to,
		}, group)
		if err != nil {
			writeObserveError(w, err)
			return
		}
		converted := newPointsEntrySummaryJSON(summary)
		response.Summary = &converted
	}
	writeJSON(w, http.StatusOK, response)
}

func newPointsEntrySummaryJSON(summary observe.EntrySummary) pointsEntrySummaryJSON {
	byDay := make([]pointsDayFlowJSON, 0, len(summary.ByDay))
	for _, day := range summary.ByDay {
		byDay = append(byDay, pointsDayFlowJSON{Date: day.Day, Income: day.Income.String(), Spend: day.Spend.String(), Net: day.Net.String()})
	}
	byKey := make([]pointsKeySpendJSON, 0, len(summary.ByKey))
	for _, key := range summary.ByKey {
		var apiKey *apiKeyRefJSON
		if key.Key != nil {
			apiKey = &apiKeyRefJSON{ID: key.Key.ID, Name: key.Key.Name}
		}
		byKey = append(byKey, pointsKeySpendJSON{APIKey: apiKey, Spend: key.Spend.String(), Entries: key.Entries})
	}
	return pointsEntrySummaryJSON{ByDay: byDay, ByKey: byKey}
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
