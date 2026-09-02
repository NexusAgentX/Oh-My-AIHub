package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (a *app) wallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := a.ledger.Wallet(r.Context(), accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet": walletResponse(wallet),
		"recovery_actions": []map[string]string{
			{"kind": "market", "href": "/c2c"},
			{"kind": "create_buy_order", "href": "/c2c/orders/new?side=buy"},
			{"kind": "my_orders", "href": "/c2c/me"},
		},
	})
}

func (a *app) walletEntries(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeDomainError(w, ledger.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeDomainError(w, ledger.ErrInvalidInput)
			return
		}
		before = parsed
	}
	entries, err := a.ledger.Entries(r.Context(), accountFromContext(r.Context()).ID, before, limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entryResponse(entry))
	}
	next := ""
	if len(entries) == limit {
		next = strconv.FormatInt(entries[len(entries)-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": items, "next_before": next})
}

func (a *app) ledgerMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := a.ledger.Metrics(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": map[string]any{
			"total_posted_balance":      metrics.TotalPostedBalance,
			"positive_posted_balance":   metrics.PositivePostedBalance,
			"negative_posted_balance":   metrics.NegativePostedBalance,
			"posted_balance_difference": metrics.TotalPostedBalance,
			"zero_sum":                  metrics.TotalPostedBalance == "0",
			"total_credit_limit":        metrics.TotalCreditLimit,
			"credit_capacity_used":      metrics.UsedCredit,
			"asset_reserved":            metrics.AssetReserved,
			"spend_authorized":          metrics.SpendAuthorized,
			"incentive_posted_balance":  metrics.IncentivePostedBalance,
			"loss_posted_balance":       metrics.LossPostedBalance,
			"over_limit_accounts":       metrics.OverLimitAccounts,
			"credit_frozen_accounts":    metrics.CreditFrozenAccounts,
			"ledger_account_count":      metrics.AccountCount,
		},
	})
}

type ledgerAccountReferenceRequest struct {
	AccountID  string             `json:"account_id"`
	SystemKind ledger.AccountKind `json:"system_kind"`
}

func (request ledgerAccountReferenceRequest) ref() (ledger.AccountRef, error) {
	request.AccountID = strings.TrimSpace(request.AccountID)
	if request.AccountID != "" && request.SystemKind == "" {
		return ledger.UserAccount(request.AccountID), nil
	}
	if request.AccountID == "" && (request.SystemKind == ledger.AccountIncentive || request.SystemKind == ledger.AccountLoss) {
		return ledger.SystemAccount(request.SystemKind), nil
	}
	return ledger.AccountRef{}, ledger.ErrInvalidInput
}

func (a *app) adminLedgerAdjustment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		From          ledgerAccountReferenceRequest `json:"from"`
		To            ledgerAccountReferenceRequest `json:"to"`
		Amount        string                        `json:"amount"`
		Reason        string                        `json:"reason"`
		ReferenceType string                        `json:"reference_type"`
		ReferenceID   string                        `json:"reference_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	from, err := request.From.ref()
	if err != nil {
		writeDomainError(w, err)
		return
	}
	to, err := request.To.ref()
	if err != nil || from == to {
		writeDomainError(w, ledger.ErrInvalidInput)
		return
	}
	amount, err := money.Parse(request.Amount)
	if err != nil || amount <= 0 {
		writeDomainError(w, ledger.ErrInvalidInput)
		return
	}
	transaction, err := a.ledger.AdminAdjustment(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), from, to,
		amount, request.Reason, request.ReferenceType, request.ReferenceID,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transaction": transactionResponse(transaction)})
}

func (a *app) adminBadDebtTransfer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AccountID   string `json:"account_id"`
		Amount      string `json:"amount"`
		Reason      string `json:"reason"`
		ReferenceID string `json:"reference_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	amount, err := money.Parse(request.Amount)
	if err != nil || amount <= 0 {
		writeDomainError(w, ledger.ErrInvalidInput)
		return
	}
	transaction, err := a.ledger.TransferBadDebt(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), request.AccountID,
		amount, request.Reason, request.ReferenceID,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transaction": transactionResponse(transaction)})
}

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}

func walletResponse(wallet ledger.Wallet) map[string]any {
	used := money.Amount(0)
	committed, err := ledger.Add(wallet.AssetReserved, wallet.SpendAuthorized)
	if err == nil && committed > wallet.PostedBalance {
		used, _ = ledger.Subtract(committed, wallet.PostedBalance)
	}
	return map[string]any{
		"posted_balance":         wallet.PostedBalance.String(),
		"asset_reserved":         wallet.AssetReserved.String(),
		"spend_authorized":       wallet.SpendAuthorized.String(),
		"credit_limit":           wallet.CreditLimit.String(),
		"effective_credit_limit": wallet.EffectiveCredit.String(),
		"credit_capacity_used":   used.String(),
		"credit_frozen":          wallet.CreditFrozen,
		"spendable_capacity":     wallet.SpendableCapacity.String(),
		"over_limit":             wallet.OverLimit,
		"risk_status":            walletRiskStatus(wallet),
		"updated_at":             wallet.UpdatedAt,
	}
}

func walletRiskStatus(wallet ledger.Wallet) string {
	if wallet.CreditFrozen {
		return "credit_frozen"
	}
	if wallet.OverLimit {
		return "over_limit"
	}
	if wallet.SpendableCapacity == 0 {
		return "insufficient"
	}
	return "normal"
}

func entryResponse(entry ledger.Entry) map[string]any {
	return map[string]any{
		"id":                    strconv.FormatInt(entry.ID, 10),
		"transaction_id":        entry.TransactionID,
		"entry_ordinal":         entry.Ordinal,
		"amount":                entry.Amount.String(),
		"posted_balance_before": entry.PostedBalanceBefore.String(),
		"posted_balance_after":  entry.PostedBalanceAfter.String(),
		"created_at":            entry.CreatedAt,
	}
}

func transactionResponse(transaction ledger.Transaction) map[string]any {
	entries := make([]map[string]any, 0, len(transaction.Entries))
	for _, entry := range transaction.Entries {
		item := entryResponse(entry)
		item["account_id"] = entry.IdentityAccountID
		item["account_kind"] = entry.AccountKind
		entries = append(entries, item)
	}
	return map[string]any{
		"id":                         transaction.ID,
		"idempotency_key":            transaction.IdempotencyKey,
		"kind":                       transaction.Kind,
		"reason":                     transaction.Reason,
		"reference_type":             transaction.ReferenceType,
		"reference_id":               transaction.ReferenceID,
		"actor_account_id":           transaction.ActorAccountID,
		"reversal_of_transaction_id": transaction.ReversalOfTransactionID,
		"entries":                    entries,
		"created_at":                 transaction.CreatedAt,
	}
}
