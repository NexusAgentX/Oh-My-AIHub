package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/clientip"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

// 中间件链（由外到内）：
//
//	responseWriteDeadline -> securityHeaders -> requireSameOrigin -> mux
//
// 路由级门禁由 router 按路由声明的 access 级别包裹，由外到内：
//
//	requireSession -> requireReadyAccount（首次改密门禁）-> requireAdmin（管理员门禁）
//
// 限流依赖请求体内容与口令槽位，保留在对应 handler 内，状态集中在 rateLimits。

func responseWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(defaultWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			writeError(w, http.StatusServiceUnavailable, "write_deadline_unavailable", "响应暂不可用")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (a *app) requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The fixed MCP callback has its own task-scoped random Bearer token;
		// it accepts neither browser sessions nor platform API Keys.
		if isExternalGatewayPath(r.URL.Path) || r.Method == http.MethodPost && r.URL.Path == "/api/detection/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			origin := r.Header.Get("Origin")
			parsed, err := url.Parse(origin)
			if origin == "" || err != nil || parsed.Scheme == "" || !strings.EqualFold(parsed.Scheme, a.requestScheme(r)) || !strings.EqualFold(parsed.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross_origin_request", "跨站请求已被拒绝")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isExternalGatewayPath(path string) bool {
	return strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v1beta/") || strings.HasPrefix(path, "/backend-api/codex/") || path == "/backend-api/wham/accounts/check"
}

func (a *app) requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if a.trustsProxy(r) {
		forwarded := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
		if forwarded == "http" || forwarded == "https" {
			return forwarded
		}
	}
	return "http"
}

func (a *app) trustsProxy(r *http.Request) bool {
	return clientip.Trusted(r, a.trustedProxyCIDRs)
}

func (a *app) loginLimitKey(r *http.Request, username string) string {
	return a.loginClientIP(r) + "\x00" + identity.NormalizeUsername(username)
}

func (a *app) loginClientIP(r *http.Request) string {
	if ip := clientip.Address(r, a.trustedProxyCIDRs); ip != "" {
		return ip
	}
	return "unknown"
}

type contextKey string

const accountContextKey contextKey = "authenticated-account"

func accountFromContext(ctx context.Context) identity.Account {
	account, _ := ctx.Value(accountContextKey).(identity.Account)
	return account
}

func (a *app) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
			return
		}
		account, err := a.identity.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, identity.ErrInvalidCredentials) {
				a.clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
				return
			}
			writeDomainError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountContextKey, account)))
	})
}

func (a *app) requireReadyAccount(next http.Handler) http.Handler {
	return a.requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accountFromContext(r.Context()).MustChangePassword {
			writeError(w, http.StatusForbidden, "password_change_required", "首次登录必须修改密码")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *app) requireAdmin(next http.Handler) http.Handler {
	return a.requireReadyAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accountFromContext(r.Context()).IsAdmin {
			writeError(w, http.StatusForbidden, "administrator_required", "没有管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *app) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *app) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
