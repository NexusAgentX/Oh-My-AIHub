package api

import (
	"errors"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	if len(request.Username) > 128 || len(request.Password) > 128 || request.Username == "" || request.Password == "" {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	limitKey := a.loginLimitKey(r, request.Username)
	ipLimitKey := a.loginClientIP(r)
	if !a.allowLoginAttempt(ipLimitKey, limitKey) {
		writeError(w, http.StatusTooManyRequests, "login_rate_limited", "登录尝试过多，请稍后再试")
		return
	}
	if !a.loginIPLimiter.allowed(ipLimitKey) || !a.loginLimiter.allowed(limitKey) {
		writeError(w, http.StatusTooManyRequests, "login_rate_limited", "登录尝试过多，请稍后再试")
		return
	}
	if !acquirePasswordSlot(a.loginPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "login_busy", "登录服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.loginPasswordSlots }()
	result, err := a.identity.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			a.loginLimiter.failure(limitKey)
			a.loginIPLimiter.failure(ipLimitKey)
		}
		writeDomainError(w, err)
		return
	}
	a.loginLimiter.success(limitKey)
	a.loginIPLimiter.success(ipLimitKey)
	a.setSessionCookie(w, result.SessionToken)
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(result.Account)})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.cookieName); err == nil {
		if err := a.identity.Logout(r.Context(), cookie.Value); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(accountFromContext(r.Context()))})
}

func (a *app) currentAccount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(accountFromContext(r.Context()))})
}

func (a *app) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	if len(request.CurrentPassword) > 128 || len(request.NewPassword) > 128 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
		return
	}
	account := accountFromContext(r.Context())
	if !a.allowPasswordChangeAttempt(a.loginClientIP(r), account.ID) {
		writeError(w, http.StatusTooManyRequests, "password_rate_limited", "密码修改尝试过多，请稍后再试")
		return
	}
	if !acquirePasswordSlot(a.accountPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "password_service_busy", "密码服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.accountPasswordSlots }()
	result, err := a.identity.ChangePassword(r.Context(), account.ID, request.CurrentPassword, request.NewPassword)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	a.setSessionCookie(w, result.SessionToken)
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(result.Account)})
}

func (a *app) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.identity.ListAccounts(r.Context(), accountFromContext(r.Context()), r.URL.Query().Get("q"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, accountResponse(account))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": items})
}

func (a *app) createAccount(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string          `json:"username"`
		DisplayName string          `json:"display_name"`
		CreditLimit string          `json:"credit_limit"`
		IsAdmin     bool            `json:"is_admin"`
		Status      identity.Status `json:"status"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	creditLimit, err := money.Parse(request.CreditLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if !acquirePasswordSlot(a.accountPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "password_service_busy", "密码服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.accountPasswordSlots }()
	created, err := a.identity.CreateInvitedAccount(r.Context(), accountFromContext(r.Context()), request.Username, request.DisplayName, creditLimit, request.IsAdmin, request.Status)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"account":          accountResponse(created.Account),
		"initial_password": created.InitialPassword,
	})
}

func (a *app) updateAccount(w http.ResponseWriter, r *http.Request) {
	if !uuidPattern.MatchString(r.PathValue("accountID")) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	var request struct {
		ExpectedVersion int64            `json:"expected_version"`
		Status          *identity.Status `json:"status"`
		CreditLimit     *string          `json:"credit_limit"`
		CreditFrozen    *bool            `json:"credit_frozen"`
		IsAdmin         *bool            `json:"is_admin"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	update := identity.AccountUpdate{
		ExpectedVersion: request.ExpectedVersion,
		Status:          request.Status,
		CreditFrozen:    request.CreditFrozen,
		IsAdmin:         request.IsAdmin,
	}
	if request.CreditLimit != nil {
		creditLimit, err := money.Parse(*request.CreditLimit)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		update.CreditLimit = &creditLimit
	}
	account, err := a.identity.UpdateAccount(r.Context(), accountFromContext(r.Context()), r.PathValue("accountID"), update)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(account)})
}

func (a *app) resetAccountPassword(w http.ResponseWriter, r *http.Request) {
	if !uuidPattern.MatchString(r.PathValue("accountID")) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	var request struct{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	account := accountFromContext(r.Context())
	if r.PathValue("accountID") == account.ID {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "不能重置自己的密码，请在账户设置中修改")
		return
	}
	if !a.allowPasswordChangeAttempt(a.loginClientIP(r), account.ID) {
		writeError(w, http.StatusTooManyRequests, "password_rate_limited", "密码修改尝试过多，请稍后再试")
		return
	}
	if !acquirePasswordSlot(a.accountPasswordSlots) {
		writeError(w, http.StatusTooManyRequests, "password_service_busy", "密码服务繁忙，请稍后再试")
		return
	}
	defer func() { <-a.accountPasswordSlots }()
	reset, err := a.identity.AdminResetPassword(r.Context(), account, r.PathValue("accountID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account":          accountResponse(reset.Account),
		"initial_password": reset.InitialPassword,
	})
}
