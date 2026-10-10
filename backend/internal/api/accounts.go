package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pathAccountID returns the {accountID} path value, answering 404 for a non-UUID.
func pathAccountID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("accountID")
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return "", false
	}
	return id, true
}

// adminAccountJSON is the OpenAPI AdminAccount schema.
type adminAccountJSON struct {
	ID                 string          `json:"id"`
	Username           string          `json:"username"`
	DisplayName        string          `json:"display_name"`
	IsAdmin            bool            `json:"is_admin"`
	Status             identity.Status `json:"status"`
	MustChangePassword bool            `json:"must_change_password"`
	CreditLimit        string          `json:"credit_limit"`
	Balance            string          `json:"balance"`
	Available          string          `json:"available"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	PasswordChangedAt  *time.Time      `json:"password_changed_at"`
	LastActiveAt       *time.Time      `json:"last_active_at"`
}

// adminAccountPageJSON is the OpenAPI AdminAccountPage schema.
type adminAccountPageJSON struct {
	Items      []adminAccountJSON `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

// adminAccountEnvelopeJSON is the OpenAPI AdminAccountEnvelope schema.
type adminAccountEnvelopeJSON struct {
	Account adminAccountJSON `json:"account"`
}

// accountWithInitialPasswordJSON is the OpenAPI AccountWithInitialPassword schema.
type accountWithInitialPasswordJSON struct {
	Account         adminAccountJSON `json:"account"`
	InitialPassword string           `json:"initial_password"`
}

// ledgerPostingResponseJSON is the OpenAPI LedgerPostingResponse schema.
type ledgerPostingResponseJSON struct {
	Account       adminAccountJSON `json:"account"`
	TransactionID string           `json:"transaction_id"`
	Replayed      bool             `json:"replayed"`
}

func newAdminAccountJSON(account identity.AdminAccount) adminAccountJSON {
	points := ledger.Points{Balance: account.Balance, CreditLimit: account.CreditLimit}
	return adminAccountJSON{
		ID:                 account.ID,
		Username:           account.Username,
		DisplayName:        account.DisplayName,
		IsAdmin:            account.IsAdmin,
		Status:             account.Status,
		MustChangePassword: account.MustChangePassword,
		CreditLimit:        account.CreditLimit.String(),
		Balance:            account.Balance.String(),
		Available:          points.Available().String(),
		CreatedAt:          account.CreatedAt,
		UpdatedAt:          account.UpdatedAt,
		PasswordChangedAt:  account.PasswordChangedAt,
		LastActiveAt:       account.LastActiveAt,
	}
}

func (a *app) listAdminAccounts(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	if !ok {
		writeBadCursor(w)
		return
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if after, ok = decodeTextCursor(raw); !ok {
			writeBadCursor(w)
			return
		}
	}
	accounts, err := a.identity.ListAccounts(r.Context(), accountFromContext(r.Context()), identity.AccountFilter{
		Query: r.URL.Query().Get("q"), Status: identity.Status(r.URL.Query().Get("status")),
		AfterCursor: after, Limit: limit + 1,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hasMore := len(accounts) > limit
	if hasMore {
		accounts = accounts[:limit]
	}
	items := make([]adminAccountJSON, 0, len(accounts))
	cursor := ""
	for _, account := range accounts {
		items = append(items, newAdminAccountJSON(account))
		cursor = encodeTextCursor(account.Username)
	}
	writeJSON(w, http.StatusOK, adminAccountPageJSON{Items: items, NextCursor: nextCursor(hasMore, cursor)})
}

func (a *app) createAdminAccount(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string  `json:"username"`
		DisplayName string  `json:"display_name"`
		CreditLimit *string `json:"credit_limit"`
		IsAdmin     bool    `json:"is_admin"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	var creditLimit *money.Amount
	if request.CreditLimit != nil {
		parsed, err := money.Parse(*request.CreditLimit)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		creditLimit = &parsed
	}
	if !acquirePasswordSlot(a.accountPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "password_service_busy", "密码服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.accountPasswordSlots }()
	created, err := a.identity.CreateInvitedAccount(r.Context(), accountFromContext(r.Context()), request.Username, request.DisplayName, creditLimit, request.IsAdmin)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, accountWithInitialPasswordJSON{
		Account:         newAdminAccountJSON(created.Account),
		InitialPassword: created.InitialPassword,
	})
}

func (a *app) updateAdminAccount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	var request struct {
		DisplayName *string          `json:"display_name"`
		Status      *identity.Status `json:"status"`
		CreditLimit *string          `json:"credit_limit"`
		IsAdmin     *bool            `json:"is_admin"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	update := identity.AccountUpdate{DisplayName: request.DisplayName, Status: request.Status, IsAdmin: request.IsAdmin}
	if request.CreditLimit != nil {
		creditLimit, err := money.Parse(*request.CreditLimit)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		update.CreditLimit = &creditLimit
	}
	account, err := a.identity.UpdateAccount(r.Context(), accountFromContext(r.Context()), accountID, update)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminAccountEnvelopeJSON{Account: newAdminAccountJSON(account)})
}

func (a *app) resetAdminAccountPassword(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	actor := accountFromContext(r.Context())
	if !a.allowPasswordChangeAttempt(a.loginClientIP(r), actor.ID) {
		writeError(w, http.StatusTooManyRequests, "password_rate_limited", "密码修改尝试过多，请稍后再试")
		return
	}
	if !acquirePasswordSlot(a.accountPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "password_service_busy", "密码服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.accountPasswordSlots }()
	reset, err := a.identity.AdminResetPassword(r.Context(), actor, accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accountWithInitialPasswordJSON{
		Account:         newAdminAccountJSON(reset.Account),
		InitialPassword: reset.InitialPassword,
	})
}

func (a *app) writePosting(w http.ResponseWriter, r *http.Request, accountID string, posted ledger.Posted) {
	account, err := a.identity.GetAccount(r.Context(), accountFromContext(r.Context()), accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ledgerPostingResponseJSON{
		Account:       newAdminAccountJSON(account),
		TransactionID: posted.ID,
		Replayed:      posted.Replayed,
	})
}

func (a *app) adjustAdminAccount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	var request struct {
		Amount string `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	amount, err := money.Parse(request.Amount)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	key, ok := idempotencyKey(r, "admin_adjust:"+accountID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key 无效")
		return
	}
	posted, err := a.ledger.Adjust(r.Context(), ledger.Adjustment{
		ActorID: accountFromContext(r.Context()).ID, AccountID: accountID, Amount: amount, Reason: request.Reason, IdempotencyKey: key,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	a.writePosting(w, r, accountID, posted)
}

func (a *app) writeOffAdminAccount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	key, ok := idempotencyKey(r, "bad_debt_writeoff:"+accountID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key 无效")
		return
	}
	posted, err := a.ledger.WriteOff(r.Context(), ledger.WriteOff{
		ActorID: accountFromContext(r.Context()).ID, AccountID: accountID, Reason: request.Reason, IdempotencyKey: key,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	a.writePosting(w, r, accountID, posted)
}

// registerAdminAccountRoutes 注册管理员账户路由。
func (a *app) registerAdminAccountRoutes(r *router) {
	r.admin("GET /api/admin/accounts", a.listAdminAccounts)
	r.admin("POST /api/admin/accounts", a.createAdminAccount)
	r.admin("PATCH /api/admin/accounts/{accountID}", a.updateAdminAccount)
	r.admin("POST /api/admin/accounts/{accountID}/reset-password", a.resetAdminAccountPassword)
	r.admin("POST /api/admin/accounts/{accountID}/adjust", a.adjustAdminAccount)
	r.admin("POST /api/admin/accounts/{accountID}/write-off", a.writeOffAdminAccount)
}
