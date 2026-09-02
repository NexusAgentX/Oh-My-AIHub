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
	a.writeLedgerEntries(w, r, ledger.UserAccount(accountFromContext(r.Context()).ID), false)
}

func (a *app) adminLedgerAccountWallet(w http.ResponseWriter, r *http.Request) {
	account, ok := ledgerUserAccountFromPath(w, r)
	if !ok {
		return
	}
	a.writeAdminLedgerWallet(w, r, account)
}

func (a *app) adminLedgerSystemWallet(w http.ResponseWriter, r *http.Request) {
	a.writeAdminLedgerWallet(w, r, ledger.SystemAccount(ledger.AccountKind(strings.TrimSpace(r.PathValue("systemKind")))))
}

func (a *app) writeAdminLedgerWallet(w http.ResponseWriter, r *http.Request, account ledger.AccountRef) {
	wallet, err := a.ledger.AdminWallet(r.Context(), accountFromContext(r.Context()), account)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallet": walletResponse(wallet)})
}

func (a *app) adminLedgerAccountEntries(w http.ResponseWriter, r *http.Request) {
	account, ok := ledgerUserAccountFromPath(w, r)
	if !ok {
		return
	}
	a.writeLedgerEntries(w, r, account, true)
}

func ledgerUserAccountFromPath(w http.ResponseWriter, r *http.Request) (ledger.AccountRef, bool) {
	accountID := strings.TrimSpace(r.PathValue("accountID"))
	if !uuidPattern.MatchString(accountID) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return ledger.AccountRef{}, false
	}
	return ledger.UserAccount(accountID), true
}

func (a *app) adminLedgerSystemEntries(w http.ResponseWriter, r *http.Request) {
	a.writeLedgerEntries(w, r, ledger.SystemAccount(ledger.AccountKind(strings.TrimSpace(r.PathValue("systemKind")))), true)
}

func (a *app) writeLedgerEntries(w http.ResponseWriter, r *http.Request, account ledger.AccountRef, administrator bool) {
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
	var entries []ledger.Entry
	var err error
	if administrator {
		entries, err = a.ledger.AdminEntries(r.Context(), accountFromContext(r.Context()), account, before, limit)
	} else {
		entries, err = a.ledger.Entries(r.Context(), account.IdentityAccountID, before, limit)
	}
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
			"total_posted_balance":                metrics.TotalPostedBalance,
			"positive_posted_balance":             metrics.PositivePostedBalance,
			"negative_posted_balance":             metrics.NegativePostedBalance,
			"posted_projection_difference":        metrics.PostedProjectionDifference,
			"posted_projection_mismatch_accounts": metrics.PostedProjectionMismatchAccounts,
			"asset_reservation_difference":        metrics.AssetReservationDifference,
			"spend_authorization_difference":      metrics.SpendAuthorizationDifference,
			"hold_projection_mismatch_accounts":   metrics.HoldProjectionMismatchAccounts,
			"zero_sum":                            metrics.TotalPostedBalance == "0",
			"ledger_consistent":                   metrics.TotalPostedBalance == "0" && metrics.PostedProjectionDifference == "0" && metrics.AssetReservationDifference == "0" && metrics.SpendAuthorizationDifference == "0",
			"total_credit_limit":                  metrics.TotalCreditLimit,
			"credit_capacity_used":                metrics.UsedCredit,
			"asset_reserved":                      metrics.AssetReserved,
			"spend_authorized":                    metrics.SpendAuthorized,
			"incentive_posted_balance":            metrics.IncentivePostedBalance,
			"loss_posted_balance":                 metrics.LossPostedBalance,
			"over_limit_accounts":                 metrics.OverLimitAccounts,
			"credit_frozen_accounts":              metrics.CreditFrozenAccounts,
			"ledger_account_count":                metrics.AccountCount,
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
		if !uuidPattern.MatchString(request.AccountID) {
			return ledger.AccountRef{}, ledger.ErrInvalidInput
		}
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
	request.AccountID = strings.TrimSpace(request.AccountID)
	if !uuidPattern.MatchString(request.AccountID) {
		writeDomainError(w, ledger.ErrInvalidInput)
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
	return map[string]any{
		"posted_balance":         wallet.PostedBalance.String(),
		"asset_reserved":         wallet.AssetReserved.String(),
		"spend_authorized":       wallet.SpendAuthorized.String(),
		"credit_limit":           wallet.CreditLimit.String(),
		"effective_credit_limit": wallet.EffectiveCredit.String(),
		"credit_used":            ledger.CreditUsed(wallet.PostedBalance).String(),
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
	counterparties := make([]map[string]any, 0, len(entry.Counterparties))
	for _, counterparty := range entry.Counterparties {
		counterparties = append(counterparties, map[string]any{
			"account_kind":  counterparty.AccountKind,
			"account_id":    counterparty.IdentityAccountID,
			"business_role": counterparty.BusinessRole,
			"amount":        counterparty.Amount.String(),
		})
	}
	return map[string]any{
		"id":                         strconv.FormatInt(entry.ID, 10),
		"transaction_id":             entry.TransactionID,
		"entry_ordinal":              entry.Ordinal,
		"business_role":              entry.BusinessRole,
		"amount":                     entry.Amount.String(),
		"posted_balance_before":      entry.PostedBalanceBefore.String(),
		"posted_balance_after":       entry.PostedBalanceAfter.String(),
		"created_at":                 entry.CreatedAt,
		"transaction_kind":           entry.TransactionKind,
		"reason":                     entry.Reason,
		"reference_type":             entry.ReferenceType,
		"reference_id":               entry.ReferenceID,
		"actor_account_id":           entry.ActorAccountID,
		"reversal_of_transaction_id": entry.ReversalOfTransactionID,
		"hold_id":                    entry.HoldID,
		"counterparties":             counterparties,
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
		"hold_id":                    transaction.HoldID,
		"entries":                    entries,
		"created_at":                 transaction.CreatedAt,
	}
}
