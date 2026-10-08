package api

import (
	"errors"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
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

func (a *app) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"account": accountResponse(accountFromContext(r.Context()))})
}

func (a *app) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
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

// registerIdentityRoutes 注册认证与当前账户路由。
func (a *app) registerIdentityRoutes(r *router) {
	r.public("POST /api/auth/login", a.login)
	r.public("POST /api/auth/logout", a.logout)
	r.session("GET /api/me", a.me)
	r.session("POST /api/me/password", a.changePassword)
}

func accountResponse(account identity.Account) map[string]any {
	return map[string]any{
		"id":                   account.ID,
		"username":             account.Username,
		"display_name":         account.DisplayName,
		"is_admin":             account.IsAdmin,
		"status":               account.Status,
		"must_change_password": account.MustChangePassword,
		"created_at":           account.CreatedAt,
	}
}
