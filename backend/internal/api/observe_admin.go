package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// ledgerCheckJSON is the OpenAPI LedgerCheck schema.
type ledgerCheckJSON struct {
	Balanced  bool      `json:"balanced"`
	Total     string    `json:"total"`
	CheckedAt time.Time `json:"checked_at"`
}

// ledgerAccountRefJSON is the OpenAPI LedgerAccountRef schema.
type ledgerAccountRefJSON struct {
	Kind       string          `json:"kind"`
	Account    *accountRefJSON `json:"account"`
	SystemCode *string         `json:"system_code"`
}

// ledgerChecksMismatchJSON is one account_balances.mismatches element of the OpenAPI LedgerChecks schema.
type ledgerChecksMismatchJSON struct {
	LedgerAccount ledgerAccountRefJSON `json:"ledger_account"`
	Balance       string               `json:"balance"`
	EntriesTotal  string               `json:"entries_total"`
}

// ledgerChecksAccountBalancesJSON is the account_balances object of the OpenAPI LedgerChecks schema.
type ledgerChecksAccountBalancesJSON struct {
	Passed     bool                       `json:"passed"`
	Mismatches []ledgerChecksMismatchJSON `json:"mismatches"`
}

// ledgerChecksEscrowJSON is the escrow object of the OpenAPI LedgerChecks schema.
type ledgerChecksEscrowJSON struct {
	Passed        bool   `json:"passed"`
	EscrowBalance string `json:"escrow_balance"`
	OrdersTotal   string `json:"orders_total"`
	Difference    string `json:"difference"`
}

// ledgerChecksMissingCallJSON is one billing_calls.missing element of the OpenAPI LedgerChecks schema.
type ledgerChecksMissingCallJSON struct {
	CallID    string         `json:"call_id"`
	CreatedAt time.Time      `json:"created_at"`
	Account   accountRefJSON `json:"account"`
	Channel   channelRefJSON `json:"channel"`
	Outcome   string         `json:"outcome"`
	Charged   string         `json:"charged"`
}

// ledgerChecksBillingCallsJSON is the billing_calls object of the OpenAPI LedgerChecks schema.
type ledgerChecksBillingCallsJSON struct {
	Passed       bool                          `json:"passed"`
	MissingCount int64                         `json:"missing_count"`
	Missing      []ledgerChecksMissingCallJSON `json:"missing"`
}

// ledgerChecksMissingTradeJSON is one released_trades.missing element of the OpenAPI LedgerChecks schema.
type ledgerChecksMissingTradeJSON struct {
	TradeID    string     `json:"trade_id"`
	Status     string     `json:"status"`
	Amount     string     `json:"amount"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

// ledgerChecksReleasedTradesJSON is the released_trades object of the OpenAPI LedgerChecks schema.
type ledgerChecksReleasedTradesJSON struct {
	Passed       bool                           `json:"passed"`
	MissingCount int64                          `json:"missing_count"`
	Missing      []ledgerChecksMissingTradeJSON `json:"missing"`
}

// ledgerChecksJSON is the OpenAPI LedgerChecks schema.
type ledgerChecksJSON struct {
	CheckedAt       time.Time                       `json:"checked_at"`
	AllPassed       bool                            `json:"all_passed"`
	AccountBalances ledgerChecksAccountBalancesJSON `json:"account_balances"`
	Escrow          ledgerChecksEscrowJSON          `json:"escrow"`
	BillingCalls    ledgerChecksBillingCallsJSON    `json:"billing_calls"`
	ReleasedTrades  ledgerChecksReleasedTradesJSON  `json:"released_trades"`
}

// adminPointsBalancesJSON is the OpenAPI AdminPointsBalances schema.
type adminPointsBalancesJSON struct {
	UserPositive           string `json:"user_positive"`
	UserNegative           string `json:"user_negative"`
	CreditIssued           string `json:"credit_issued"`
	C2CEscrow              string `json:"c2c_escrow"`
	PlatformRevenue        string `json:"platform_revenue"`
	BadDebt                string `json:"bad_debt"`
	Total                  string `json:"total"`
	TotalCreditLimit       string `json:"total_credit_limit"`
	BadDebtWriteoffs       int64  `json:"bad_debt_writeoffs"`
	EscrowOrders           int64  `json:"escrow_orders"`
	EscrowTradesInProgress int64  `json:"escrow_trades_in_progress"`
}

// adminPointsTrendJSON is one trend element of the OpenAPI AdminPoints schema.
type adminPointsTrendJSON struct {
	Date            string   `json:"date"`
	Circulation     string   `json:"circulation"`
	CreditIssued    string   `json:"credit_issued"`
	PlatformRevenue string   `json:"platform_revenue"`
	BadDebt         string   `json:"bad_debt"`
	C2CEscrow       string   `json:"c2c_escrow"`
	APIVolume       string   `json:"api_volume"`
	APIFee          string   `json:"api_fee"`
	C2CVolume       string   `json:"c2c_volume"`
	C2CAvgPriceFen  *float64 `json:"c2c_avg_price_fen"`
}

// adminPointsRiskJSON is the OpenAPI AdminPointsRisk schema.
type adminPointsRiskJSON struct {
	Account        accountRefJSON `json:"account"`
	Balance        string         `json:"balance"`
	CreditLimit    string         `json:"credit_limit"`
	Available      string         `json:"available"`
	Kind           string         `json:"kind"`
	NegativeDays   *int           `json:"negative_days"`
	LastActivityAt *time.Time     `json:"last_activity_at"`
}

// adminPointsHolderJSON is one concentration.top element of the OpenAPI AdminPoints schema.
type adminPointsHolderJSON struct {
	Account accountRefJSON `json:"account"`
	Balance string         `json:"balance"`
	Share   string         `json:"share"`
}

// adminPointsConcentrationJSON is the concentration object of the OpenAPI AdminPoints schema.
type adminPointsConcentrationJSON struct {
	Top5Share *string                 `json:"top5_share"`
	Top       []adminPointsHolderJSON `json:"top"`
}

// adminPointsJSON is the OpenAPI AdminPoints schema.
type adminPointsJSON struct {
	Balances      adminPointsBalancesJSON      `json:"balances"`
	Check         ledgerCheckJSON              `json:"check"`
	Trend         []adminPointsTrendJSON       `json:"trend"`
	Risks         []adminPointsRiskJSON        `json:"risks"`
	Concentration adminPointsConcentrationJSON `json:"concentration"`
	Checks        ledgerChecksJSON             `json:"checks"`
}

// attentionItemJSON is the OpenAPI AttentionItem schema.
type attentionItemJSON struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Count    int    `json:"count"`
	Link     string `json:"link"`
}

// adminOverviewTodayJSON is the today object of the OpenAPI AdminOverview schema.
type adminOverviewTodayJSON struct {
	Calls       int64   `json:"calls"`
	Succeeded   int64   `json:"succeeded"`
	SuccessRate *string `json:"success_rate"`
	Spend       string  `json:"spend"`
	FeeRevenue  string  `json:"fee_revenue"`
}

// adminOverviewC2CJSON is the c2c object of the OpenAPI AdminOverview schema.
type adminOverviewC2CJSON struct {
	OpenOrders      int64    `json:"open_orders"`
	AwaitingPayment int64    `json:"awaiting_payment"`
	OpenDisputes    int64    `json:"open_disputes"`
	Trades24h       int64    `json:"trades_24h"`
	Volume24h       string   `json:"volume_24h"`
	AvgPriceFen24h  *float64 `json:"avg_price_fen_24h"`
}

// adminOverviewCreditJSON is the credit object of the OpenAPI AdminOverview schema.
type adminOverviewCreditJSON struct {
	Issued string `json:"issued"`
	Limit  string `json:"limit"`
}

// adminOverviewWindowJSON is the last_24h object of the OpenAPI AdminOverview schema.
type adminOverviewWindowJSON struct {
	Calls       int64   `json:"calls"`
	Succeeded   int64   `json:"succeeded"`
	SuccessRate *string `json:"success_rate"`
}

// adminOverviewJSON is the OpenAPI AdminOverview schema.
type adminOverviewJSON struct {
	Attention []attentionItemJSON     `json:"attention"`
	Ledger    ledgerCheckJSON         `json:"ledger"`
	Today     adminOverviewTodayJSON  `json:"today"`
	C2C       adminOverviewC2CJSON    `json:"c2c"`
	Credit    adminOverviewCreditJSON `json:"credit"`
	Last24h   adminOverviewWindowJSON `json:"last_24h"`
}

// ledgerTransactionEntryJSON is the OpenAPI LedgerTransactionEntry schema.
type ledgerTransactionEntryJSON struct {
	LedgerAccount ledgerAccountRefJSON `json:"ledger_account"`
	Amount        string               `json:"amount"`
	BalanceBefore string               `json:"balance_before"`
	BalanceAfter  string               `json:"balance_after"`
}

// ledgerTransactionJSON is the OpenAPI LedgerTransaction schema as the list returns it, without
// the detail-only price_snapshot and recent_actions.
type ledgerTransactionJSON struct {
	ID             string                       `json:"id"`
	Type           string                       `json:"type"`
	IdempotencyKey string                       `json:"idempotency_key"`
	Related        *ledgerRelatedJSON           `json:"related"`
	Actor          *accountRefJSON              `json:"actor"`
	Reason         string                       `json:"reason"`
	CreatedAt      time.Time                    `json:"created_at"`
	RelatedSummary *string                      `json:"related_summary"`
	Entries        []ledgerTransactionEntryJSON `json:"entries"`
}

// ledgerTransactionDetailJSON is the OpenAPI LedgerTransaction schema as the detail endpoint
// returns it. Both extra fields are always present there, price_snapshot as null when the
// transaction has none, which omitempty on the shared type could not express.
type ledgerTransactionDetailJSON struct {
	ledgerTransactionJSON
	PriceSnapshot json.RawMessage  `json:"price_snapshot"`
	RecentActions []auditEntryJSON `json:"recent_actions"`
}

// ledgerTransactionEnvelopeJSON is the OpenAPI LedgerTransactionEnvelope schema.
type ledgerTransactionEnvelopeJSON struct {
	Transaction ledgerTransactionDetailJSON `json:"transaction"`
}

// repairCallResponseJSON is the OpenAPI RepairCallResponse schema.
type repairCallResponseJSON struct {
	Call          callDetailJSON `json:"call"`
	TransactionID *string        `json:"transaction_id"`
}

func newLedgerCheckJSON(checks observe.Checks) ledgerCheckJSON {
	return ledgerCheckJSON{Balanced: checks.ZeroSumOK, Total: checks.ZeroSumTotal.String(), CheckedAt: checks.CheckedAt}
}

func newLedgerAccountRefJSON(ref observe.LedgerAccountRef) ledgerAccountRefJSON {
	var account *accountRefJSON
	if ref.Account != nil {
		value := newAccountRefJSON(*ref.Account)
		account = &value
	}
	return ledgerAccountRefJSON{Kind: ref.Kind, Account: account, SystemCode: ref.SystemCode}
}

func newLedgerChecksJSON(checks observe.Checks) ledgerChecksJSON {
	mismatches := make([]ledgerChecksMismatchJSON, 0, len(checks.Mismatches))
	for _, mismatch := range checks.Mismatches {
		mismatches = append(mismatches, ledgerChecksMismatchJSON{
			LedgerAccount: newLedgerAccountRefJSON(mismatch.Account), Balance: mismatch.Balance.String(), EntriesTotal: mismatch.EntriesTotal.String(),
		})
	}
	calls := make([]ledgerChecksMissingCallJSON, 0, len(checks.MissingCalls))
	for _, call := range checks.MissingCalls {
		calls = append(calls, ledgerChecksMissingCallJSON{
			CallID:    call.CallID,
			CreatedAt: call.CreatedAt,
			Account:   newAccountRefJSON(call.Account),
			Channel:   channelRefJSON{ID: call.Channel.ID, Name: call.Channel.Name},
			Outcome:   call.Outcome,
			Charged:   call.Charged.String(),
		})
	}
	trades := make([]ledgerChecksMissingTradeJSON, 0, len(checks.MissingTrades))
	for _, trade := range checks.MissingTrades {
		trades = append(trades, ledgerChecksMissingTradeJSON{TradeID: trade.TradeID, Status: trade.Status, Amount: trade.Amount.String(), ResolvedAt: trade.ResolvedAt})
	}
	return ledgerChecksJSON{
		CheckedAt:       checks.CheckedAt,
		AllPassed:       checks.AllPassed(),
		AccountBalances: ledgerChecksAccountBalancesJSON{Passed: checks.AccountsOK(), Mismatches: mismatches},
		Escrow: ledgerChecksEscrowJSON{
			Passed: checks.EscrowOK(), EscrowBalance: checks.EscrowBalance.String(), OrdersTotal: checks.OrdersTotal.String(),
			Difference: checks.EscrowDifference.String(),
		},
		BillingCalls:   ledgerChecksBillingCallsJSON{Passed: checks.CallsOK(), MissingCount: checks.MissingCallCount, Missing: calls},
		ReleasedTrades: ledgerChecksReleasedTradesJSON{Passed: checks.TradesOK(), MissingCount: checks.MissingTradeCount, Missing: trades},
	}
}

func newAdminPointsBalancesJSON(balances observe.Balances) adminPointsBalancesJSON {
	return adminPointsBalancesJSON{
		UserPositive:           balances.UserPositive.String(),
		UserNegative:           balances.UserNegative.String(),
		CreditIssued:           balances.CreditIssued().String(),
		C2CEscrow:              balances.Escrow.String(),
		PlatformRevenue:        balances.PlatformRevenue.String(),
		BadDebt:                balances.BadDebt.String(),
		Total:                  balances.Total.String(),
		TotalCreditLimit:       balances.TotalCreditLimit.String(),
		BadDebtWriteoffs:       balances.WriteOffs,
		EscrowOrders:           balances.EscrowOrders,
		EscrowTradesInProgress: balances.EscrowTradesInProgress,
	}
}

func newAdminPointsJSON(report observe.AdminPointsReport) adminPointsJSON {
	trend := make([]adminPointsTrendJSON, 0, len(report.Trend))
	for _, day := range report.Trend {
		trend = append(trend, adminPointsTrendJSON{
			Date:            day.Day,
			Circulation:     day.Circulation.String(),
			CreditIssued:    day.CreditIssued.String(),
			PlatformRevenue: day.PlatformRevenue.String(),
			BadDebt:         day.BadDebt.String(),
			C2CEscrow:       day.Escrow.String(),
			APIVolume:       day.APIVolume.String(),
			APIFee:          day.APIFee.String(),
			C2CVolume:       day.C2CVolume.String(),
			C2CAvgPriceFen:  day.C2CAvgPriceFen,
		})
	}
	risks := make([]adminPointsRiskJSON, 0, len(report.Risks))
	for _, risk := range report.Risks {
		risks = append(risks, adminPointsRiskJSON{
			Account:        newAccountRefJSON(risk.Account),
			Balance:        risk.Balance.String(),
			CreditLimit:    risk.CreditLimit.String(),
			Available:      money.FromNano(risk.Balance.Nano() + risk.CreditLimit.Nano()).String(),
			Kind:           risk.Kind,
			NegativeDays:   risk.NegativeDays,
			LastActivityAt: risk.LastActivityAt,
		})
	}
	top := make([]adminPointsHolderJSON, 0, len(report.Concentration.Top))
	for _, holder := range report.Concentration.Top {
		top = append(top, adminPointsHolderJSON{Account: newAccountRefJSON(holder.Account), Balance: holder.Balance.String(), Share: ratio(holder.Share)})
	}
	return adminPointsJSON{
		Balances:      newAdminPointsBalancesJSON(report.Balances),
		Check:         newLedgerCheckJSON(report.Checks),
		Trend:         trend,
		Risks:         risks,
		Concentration: adminPointsConcentrationJSON{Top5Share: optionalRatio(report.Concentration.Top5Share), Top: top},
		Checks:        newLedgerChecksJSON(report.Checks),
	}
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
	writeJSON(w, http.StatusOK, newAdminPointsJSON(report))
}

func newAdminOverviewJSON(overview observe.Overview) adminOverviewJSON {
	attention := make([]attentionItemJSON, 0, len(overview.Attention))
	for _, item := range overview.Attention {
		attention = append(attention, attentionItemJSON{Kind: item.Kind, Severity: item.Severity, Title: item.Title, Count: item.Count, Link: item.Link})
	}
	return adminOverviewJSON{
		Attention: attention,
		Ledger:    newLedgerCheckJSON(overview.Checks),
		Today: adminOverviewTodayJSON{
			Calls:       overview.Today.Calls,
			Succeeded:   overview.Today.Succeeded,
			SuccessRate: optionalRatio(rateOf(overview.Today.Succeeded, overview.Today.Calls)),
			Spend:       overview.Today.Spend.String(),
			FeeRevenue:  overview.Today.Fee.String(),
		},
		C2C: adminOverviewC2CJSON{
			OpenOrders:      overview.C2C.OpenOrders,
			AwaitingPayment: overview.C2C.AwaitingPayment,
			OpenDisputes:    overview.C2C.OpenDisputes,
			Trades24h:       overview.C2C24h.Trades,
			Volume24h:       overview.C2C24h.Volume.String(),
			AvgPriceFen24h:  overview.C2C24h.AvgPriceFen(),
		},
		Credit: adminOverviewCreditJSON{Issued: overview.Balances.CreditIssued().String(), Limit: overview.Balances.TotalCreditLimit.String()},
		Last24h: adminOverviewWindowJSON{
			Calls:       overview.Last24h.Calls,
			Succeeded:   overview.Last24h.Succeeded,
			SuccessRate: optionalRatio(rateOf(overview.Last24h.Succeeded, overview.Last24h.Calls)),
		},
	}
}

func (a *app) getAdminOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := a.observe.Overview(r.Context())
	if err != nil {
		writeObserveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAdminOverviewJSON(overview))
}

func rateOf(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	value := float64(part) / float64(whole)
	return &value
}

func newLedgerTransactionJSON(transaction observe.Transaction) ledgerTransactionJSON {
	var related *ledgerRelatedJSON
	if transaction.RelatedType != "" {
		related = &ledgerRelatedJSON{Type: transaction.RelatedType, ID: transaction.RelatedID}
	}
	var actor *accountRefJSON
	if transaction.Actor != nil {
		value := newAccountRefJSON(*transaction.Actor)
		actor = &value
	}
	var summary *string
	if transaction.RelatedSummary != "" {
		summary = &transaction.RelatedSummary
	}
	entries := make([]ledgerTransactionEntryJSON, 0, len(transaction.Entries))
	for _, entry := range transaction.Entries {
		entries = append(entries, ledgerTransactionEntryJSON{
			LedgerAccount: newLedgerAccountRefJSON(entry.Account),
			Amount:        entry.Amount.String(),
			BalanceBefore: entry.BalanceBefore.String(),
			BalanceAfter:  entry.BalanceAfter.String(),
		})
	}
	return ledgerTransactionJSON{
		ID:             transaction.ID,
		Type:           transaction.Type,
		IdempotencyKey: transaction.IdempotencyKey,
		Related:        related,
		Actor:          actor,
		Reason:         transaction.Reason,
		CreatedAt:      transaction.CreatedAt,
		RelatedSummary: summary,
		Entries:        entries,
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
	result := make([]ledgerTransactionJSON, 0, len(items))
	for _, item := range items {
		result = append(result, newLedgerTransactionJSON(item))
	}
	writeJSON(w, http.StatusOK, pageJSON[ledgerTransactionJSON]{Items: result, NextCursor: encodeCallCursor(next)})
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
	recent := make([]auditEntryJSON, 0, len(actions))
	for _, entry := range actions {
		recent = append(recent, newAuditEntryJSON(entry))
	}
	writeJSON(w, http.StatusOK, ledgerTransactionEnvelopeJSON{Transaction: ledgerTransactionDetailJSON{
		ledgerTransactionJSON: newLedgerTransactionJSON(transaction),
		PriceSnapshot:         rawJSONOrNil(transaction.PriceSnapshot),
		RecentActions:         recent,
	}})
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
	var transactionID *string
	if result.TransactionID != "" {
		transactionID = &result.TransactionID
	}
	writeJSON(w, http.StatusOK, repairCallResponseJSON{Call: newCallDetailJSON(detail), TransactionID: transactionID})
}
