package api

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

func ledgerCheckJSON(checks observe.Checks) map[string]any {
	return map[string]any{"balanced": checks.ZeroSumOK, "total": checks.ZeroSumTotal.String(), "checked_at": checks.CheckedAt}
}

func ledgerAccountRefJSON(ref observe.LedgerAccountRef) map[string]any {
	var account any
	if ref.Account != nil {
		account = accountRefJSON(*ref.Account)
	}
	return map[string]any{"kind": ref.Kind, "account": account, "system_code": optionalString(ref.SystemCode)}
}

func checksJSON(checks observe.Checks) map[string]any {
	mismatches := make([]map[string]any, 0, len(checks.Mismatches))
	for _, mismatch := range checks.Mismatches {
		mismatches = append(mismatches, map[string]any{
			"ledger_account": ledgerAccountRefJSON(mismatch.Account), "balance": mismatch.Balance.String(), "entries_total": mismatch.EntriesTotal.String(),
		})
	}
	calls := make([]map[string]any, 0, len(checks.MissingCalls))
	for _, call := range checks.MissingCalls {
		calls = append(calls, map[string]any{
			"call_id": call.CallID, "created_at": call.CreatedAt, "account": accountRefJSON(call.Account),
			"channel": map[string]any{"id": call.Channel.ID, "name": call.Channel.Name}, "outcome": call.Outcome, "charged": call.Charged.String(),
		})
	}
	trades := make([]map[string]any, 0, len(checks.MissingTrades))
	for _, trade := range checks.MissingTrades {
		trades = append(trades, map[string]any{"trade_id": trade.TradeID, "status": trade.Status, "amount": trade.Amount.String(), "resolved_at": trade.ResolvedAt})
	}
	return map[string]any{
		"checked_at": checks.CheckedAt, "all_passed": checks.AllPassed(),
		"account_balances": map[string]any{"passed": checks.AccountsOK(), "mismatches": mismatches},
		"escrow": map[string]any{
			"passed": checks.EscrowOK(), "escrow_balance": checks.EscrowBalance.String(), "orders_total": checks.OrdersTotal.String(),
			"difference": checks.EscrowDifference.String(),
		},
		"billing_calls":   map[string]any{"passed": checks.CallsOK(), "missing_count": checks.MissingCallCount, "missing": calls},
		"released_trades": map[string]any{"passed": checks.TradesOK(), "missing_count": checks.MissingTradeCount, "missing": trades},
	}
}

func balancesJSON(balances observe.Balances) map[string]any {
	return map[string]any{
		"user_positive": balances.UserPositive.String(), "user_negative": balances.UserNegative.String(), "credit_issued": balances.CreditIssued().String(),
		"c2c_escrow": balances.Escrow.String(), "platform_revenue": balances.PlatformRevenue.String(), "bad_debt": balances.BadDebt.String(),
		"total": balances.Total.String(), "total_credit_limit": balances.TotalCreditLimit.String(), "bad_debt_writeoffs": balances.WriteOffs,
		"escrow_orders": balances.EscrowOrders, "escrow_trades_in_progress": balances.EscrowTradesInProgress,
	}
}

func risksJSON(risks []observe.Risk) []map[string]any {
	items := make([]map[string]any, 0, len(risks))
	for _, risk := range risks {
		items = append(items, map[string]any{
			"account": accountRefJSON(risk.Account), "balance": risk.Balance.String(), "credit_limit": risk.CreditLimit.String(),
			"available": money.FromNano(risk.Balance.Nano() + risk.CreditLimit.Nano()).String(), "kind": risk.Kind,
			"negative_days": risk.NegativeDays, "last_activity_at": risk.LastActivityAt,
		})
	}
	return items
}

func concentrationJSON(concentration observe.Concentration) map[string]any {
	top := make([]map[string]any, 0, len(concentration.Top))
	for _, holder := range concentration.Top {
		top = append(top, map[string]any{"account": accountRefJSON(holder.Account), "balance": holder.Balance.String(), "share": ratio(holder.Share)})
	}
	return map[string]any{"top5_share": optionalRatio(concentration.Top5Share), "top": top}
}

func (a *app) getAdminPoints(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeInvalidQuery(w)
			return
		}
		days = parsed
	}
	report, err := a.observe.AdminPoints(r.Context(), days)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	trend := make([]map[string]any, 0, len(report.Trend))
	for _, day := range report.Trend {
		trend = append(trend, map[string]any{
			"date": day.Day, "circulation": day.Circulation.String(), "credit_issued": day.CreditIssued.String(),
			"platform_revenue": day.PlatformRevenue.String(), "bad_debt": day.BadDebt.String(), "c2c_escrow": day.Escrow.String(),
			"api_volume": day.APIVolume.String(), "api_fee": day.APIFee.String(), "c2c_volume": day.C2CVolume.String(), "c2c_avg_price_fen": day.C2CAvgPriceFen,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"balances": balancesJSON(report.Balances), "check": ledgerCheckJSON(report.Checks), "trend": trend,
		"risks": risksJSON(report.Risks), "concentration": concentrationJSON(report.Concentration), "checks": checksJSON(report.Checks),
	})
}

func (a *app) getAdminOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := a.observe.Overview(r.Context())
	if err != nil {
		writeObserveError(w, err)
		return
	}
	attention := make([]map[string]any, 0, len(overview.Attention))
	for _, item := range overview.Attention {
		attention = append(attention, map[string]any{"kind": item.Kind, "severity": item.Severity, "title": item.Title, "count": item.Count, "link": item.Link})
	}
	window := func(w observe.CallWindow) map[string]any {
		return map[string]any{"calls": w.Calls, "succeeded": w.Succeeded, "success_rate": optionalRatio(rateOf(w.Succeeded, w.Calls))}
	}
	today := window(overview.Today)
	today["spend"] = overview.Today.Spend.String()
	today["fee_revenue"] = overview.Today.Fee.String()
	writeJSON(w, http.StatusOK, map[string]any{
		"attention": attention, "ledger": ledgerCheckJSON(overview.Checks), "today": today, "last_24h": window(overview.Last24h),
		"c2c": map[string]any{
			"open_orders": overview.C2C.OpenOrders, "awaiting_payment": overview.C2C.AwaitingPayment, "open_disputes": overview.C2C.OpenDisputes,
			"trades_24h": overview.C2C24h.Trades, "volume_24h": overview.C2C24h.Volume.String(), "avg_price_fen_24h": overview.C2C24h.AvgPriceFen(),
		},
		"credit": map[string]any{"issued": overview.Balances.CreditIssued().String(), "limit": overview.Balances.TotalCreditLimit.String()},
	})
}

func rateOf(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	value := float64(part) / float64(whole)
	return &value
}

func transactionJSON(transaction observe.Transaction) map[string]any {
	var related, actor any
	if transaction.RelatedType != "" {
		related = map[string]any{"type": transaction.RelatedType, "id": transaction.RelatedID}
	}
	if transaction.Actor != nil {
		actor = accountRefJSON(*transaction.Actor)
	}
	var summary any
	if transaction.RelatedSummary != "" {
		summary = transaction.RelatedSummary
	}
	entries := make([]map[string]any, 0, len(transaction.Entries))
	for _, entry := range transaction.Entries {
		entries = append(entries, map[string]any{
			"ledger_account": ledgerAccountRefJSON(entry.Account), "amount": entry.Amount.String(),
			"balance_before": entry.BalanceBefore.String(), "balance_after": entry.BalanceAfter.String(),
		})
	}
	return map[string]any{
		"id": transaction.ID, "type": transaction.Type, "idempotency_key": transaction.IdempotencyKey, "related": related, "actor": actor,
		"reason": transaction.Reason, "created_at": transaction.CreatedAt, "related_summary": summary, "entries": entries,
	}
}

func (a *app) listAdminTransactions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, limitOK := pageLimit(r)
	cursor, cursorOK := decodeCallCursor(r)
	from, fromOK := parseTimeParam(r, "from")
	to, toOK := parseTimeParam(r, "to")
	filter := observe.TxFilter{
		Type: query.Get("type"), AccountID: query.Get("account_id"), RelatedType: query.Get("related_type"), RelatedID: query.Get("related_id"), From: from, To: to,
	}
	validRelated := filter.RelatedType == "" || (filter.RelatedID != "" && uuidPattern.MatchString(filter.RelatedID) &&
		(filter.RelatedType == "call" || filter.RelatedType == "c2c_order" || filter.RelatedType == "c2c_trade" || filter.RelatedType == "account"))
	if filter.RelatedType == "" && filter.RelatedID != "" {
		validRelated = false
	}
	if !limitOK || !fromOK || !toOK || !validRelated ||
		(filter.Type != "" && !ledger.TransactionType(filter.Type).Valid()) ||
		(filter.AccountID != "" && !uuidPattern.MatchString(filter.AccountID)) {
		writeInvalidQuery(w)
		return
	}
	if !cursorOK {
		writeBadCursor(w)
		return
	}
	items, next, err := a.observe.Transactions(r.Context(), filter, cursor, limit)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, transactionJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "next_cursor": encodeCallCursor(next)})
}

func (a *app) getAdminTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("transactionID")
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	transaction, err := a.observe.Transaction(r.Context(), id)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	var actions []audit.Entry
	for _, userID := range transaction.UserIDs() {
		entries, err := a.audit.List(r.Context(), audit.Filter{TargetType: "account", TargetID: userID, Limit: 10})
		if err != nil {
			writeDomainError(w, err)
			return
		}
		actions = append(actions, entries...)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].ID > actions[j].ID })
	if len(actions) > 10 {
		actions = actions[:10]
	}
	recent := make([]map[string]any, 0, len(actions))
	for _, entry := range actions {
		recent = append(recent, auditEntryResponse(entry))
	}
	response := transactionJSON(transaction)
	response["price_snapshot"] = rawJSONOrNil(transaction.PriceSnapshot)
	response["recent_actions"] = recent
	writeJSON(w, http.StatusOK, map[string]any{"transaction": response})
}

func (a *app) repairCall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("callID")
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	var request struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	result, err := a.observe.RepairCall(r.Context(), observe.Repair{
		ActorID: accountFromContext(r.Context()).ID, CallID: id, Action: observe.RepairAction(request.Action), Reason: request.Reason,
	})
	if err != nil {
		writeObserveError(w, err)
		return
	}
	detail, err := a.observe.Call(r.Context(), id, viewerOf(r))
	if err != nil {
		writeObserveError(w, err)
		return
	}
	var transactionID any
	if result.TransactionID != "" {
		transactionID = result.TransactionID
	}
	writeJSON(w, http.StatusOK, map[string]any{"call": callDetailJSON(detail), "transaction_id": transactionID})
}
