package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
)

const (
	defaultSessionCookie = "oma_session"
	defaultWriteTimeout  = 30 * time.Second
)

type Dependencies struct {
	Identity          *identity.Service
	Catalog           *catalog.Service
	Channels          *channel.Service
	Gateway           *gateway.Service
	Ledger            *ledger.Service
	C2C               *c2c.Service
	Ops               OpsStore
	DatabaseReady     func(context.Context) error
	CookieSecure      bool
	TrustedProxyCIDRs []netip.Prefix
}

// OpsStore is the operations data surface consumed by the admin API.
type OpsStore interface {
	OpsMetrics(ctx context.Context, window ops.Window) (ops.Metrics, error)
	OpsProviderIncome(ctx context.Context, window ops.Window) (ops.ProviderIncomeSnapshot, error)
	OpsAnomalies(ctx context.Context) (ops.Anomalies, error)
	OpsRunInspection(ctx context.Context, triggeredBy string) (ops.InspectionRecord, error)
	OpsListInspections(ctx context.Context, limit int64) ([]ops.InspectionRecord, error)
	OpsTrialSummary(ctx context.Context) (ops.TrialSummary, error)
}

type app struct {
	identity             *identity.Service
	catalog              *catalog.Service
	channels             *channel.Service
	gateway              *gateway.Service
	ledger               *ledger.Service
	c2c                  *c2c.Service
	ops                  OpsStore
	databaseReady        func(context.Context) error
	cookieSecure         bool
	cookieName           string
	loginLimiter         *loginLimiter
	loginIPLimiter       *loginLimiter
	loginAttempts        *loginLimiter
	loginIPAttempts      *loginLimiter
	passwordChanges      *loginLimiter
	passwordChangeIPs    *loginLimiter
	loginPasswordSlots   chan struct{}
	accountPasswordSlots chan struct{}
	trustedProxyCIDRs    []netip.Prefix
}

func NewHandler(dependencies Dependencies) http.Handler {
	application := &app{
		identity:             dependencies.Identity,
		catalog:              dependencies.Catalog,
		channels:             dependencies.Channels,
		gateway:              dependencies.Gateway,
		ledger:               dependencies.Ledger,
		c2c:                  dependencies.C2C,
		ops:                  dependencies.Ops,
		databaseReady:        dependencies.DatabaseReady,
		cookieSecure:         dependencies.CookieSecure,
		cookieName:           defaultSessionCookie,
		loginLimiter:         newLoginLimiter(8, 15*time.Minute, 10_000),
		loginIPLimiter:       newLoginLimiter(32, 15*time.Minute, 10_000),
		loginAttempts:        newLoginLimiter(20, 15*time.Minute, 10_000),
		loginIPAttempts:      newLoginLimiter(60, 15*time.Minute, 10_000),
		passwordChanges:      newLoginLimiter(8, 15*time.Minute, 10_000),
		passwordChangeIPs:    newLoginLimiter(32, 15*time.Minute, 10_000),
		loginPasswordSlots:   make(chan struct{}, 2),
		accountPasswordSlots: make(chan struct{}, 2),
		trustedProxyCIDRs:    append([]netip.Prefix(nil), dependencies.TrustedProxyCIDRs...),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", application.health)
	mux.HandleFunc("GET /api/instance", application.instanceState)
	mux.HandleFunc("POST /api/instance/initialize", application.instanceInitialize)
	mux.HandleFunc("POST /api/auth/login", application.login)
	mux.HandleFunc("POST /api/auth/logout", application.logout)
	mux.Handle("GET /api/auth/session", application.requireSession(http.HandlerFunc(application.session)))
	mux.Handle("PUT /api/account/password", application.requireSession(http.HandlerFunc(application.changePassword)))
	mux.Handle("GET /api/account", application.requireReadyAccount(http.HandlerFunc(application.currentAccount)))
	mux.Handle("GET /api/models", application.requireReadyAccount(http.HandlerFunc(application.listPublicModels)))
	mux.Handle("GET /api/models/{modelID...}", application.requireReadyAccount(http.HandlerFunc(application.getPublicModel)))
	mux.Handle("GET /api/wallet", application.requireReadyAccount(http.HandlerFunc(application.wallet)))
	mux.Handle("GET /api/wallet/entries", application.requireReadyAccount(http.HandlerFunc(application.walletEntries)))
	mux.Handle("GET /api/channels", application.requireReadyAccount(http.HandlerFunc(application.listChannels)))
	mux.Handle("POST /api/channels", application.requireReadyAccount(http.HandlerFunc(application.createChannel)))
	mux.Handle("GET /api/channels/{channelID}", application.requireReadyAccount(http.HandlerFunc(application.getChannel)))
	mux.Handle("PATCH /api/channels/{channelID}", application.requireReadyAccount(http.HandlerFunc(application.updateChannel)))
	mux.Handle("POST /api/channels/{channelID}/publish", application.requireReadyAccount(http.HandlerFunc(application.publishChannel)))
	mux.Handle("POST /api/channels/{channelID}/pause", application.requireReadyAccount(http.HandlerFunc(application.pauseChannel)))
	mux.Handle("DELETE /api/channels/{channelID}", application.requireReadyAccount(http.HandlerFunc(application.deleteChannel)))
	mux.Handle("POST /api/channels/{channelID}/credential-revoke", application.requireReadyAccount(http.HandlerFunc(application.revokeChannelCredential)))
	mux.Handle("POST /api/channels/{channelID}/offers", application.requireReadyAccount(http.HandlerFunc(application.addChannelOffer)))
	mux.Handle("PATCH /api/channel-offers/{offerID}", application.requireReadyAccount(http.HandlerFunc(application.updateChannelOffer)))
	mux.Handle("POST /api/channel-offers/{offerID}/disable", application.requireReadyAccount(http.HandlerFunc(application.disableChannelOffer)))
	mux.Handle("POST /api/channel-offers/{offerID}/resume", application.requireReadyAccount(http.HandlerFunc(application.resumeChannelOffer)))
	mux.Handle("DELETE /api/channel-offers/{offerID}", application.requireReadyAccount(http.HandlerFunc(application.deleteChannelOffer)))
	mux.Handle("POST /api/channel-offers/{offerID}/validation-attempts", application.requireReadyAccount(http.HandlerFunc(application.validateChannelOffer)))
	mux.Handle("GET /api/channel-offers/{offerID}/validation-attempts", application.requireReadyAccount(http.HandlerFunc(application.listOfferValidationAttempts)))
	mux.Handle("GET /api/market/offers", application.requireReadyAccount(http.HandlerFunc(application.listMarketOffers)))
	mux.Handle("GET /api/market/channels/{channelID}", application.requireReadyAccount(http.HandlerFunc(application.getMarketChannel)))
	mux.Handle("PUT /api/market/channels/{channelID}/rating", application.requireReadyAccount(http.HandlerFunc(application.rateMarketChannel)))
	mux.Handle("GET /api/keys", application.requireReadyAccount(http.HandlerFunc(application.listAPIKeys)))
	mux.Handle("POST /api/keys", application.requireReadyAccount(http.HandlerFunc(application.createAPIKey)))
	mux.Handle("GET /api/keys/{keyID}", application.requireReadyAccount(http.HandlerFunc(application.getAPIKey)))
	mux.Handle("PATCH /api/keys/{keyID}", application.requireReadyAccount(http.HandlerFunc(application.updateAPIKey)))
	mux.Handle("DELETE /api/keys/{keyID}", application.requireReadyAccount(http.HandlerFunc(application.deleteAPIKey)))
	mux.Handle("POST /api/keys/{keyID}/rotate", application.requireReadyAccount(http.HandlerFunc(application.rotateAPIKey)))
	mux.Handle("POST /api/keys/{keyID}/disable", application.requireReadyAccount(http.HandlerFunc(application.disableAPIKey)))
	mux.Handle("POST /api/keys/{keyID}/enable", application.requireReadyAccount(http.HandlerFunc(application.enableAPIKey)))
	mux.Handle("POST /api/keys/{keyID}/pool-members", application.requireReadyAccount(http.HandlerFunc(application.addAPIKeyPoolMember)))
	mux.Handle("GET /api/calls", application.requireReadyAccount(http.HandlerFunc(application.listGatewayCalls)))
	mux.Handle("GET /api/calls/{callID}", application.requireReadyAccount(http.HandlerFunc(application.getGatewayCall)))
	mux.Handle("GET /api/dashboard", application.requireReadyAccount(http.HandlerFunc(application.gatewayDashboard)))
	mux.Handle("GET /api/c2c/market", application.requireReadyAccount(http.HandlerFunc(application.c2cMarket)))
	mux.Handle("POST /api/c2c/orders", application.requireReadyAccount(http.HandlerFunc(application.c2cCreateOrder)))
	mux.Handle("GET /api/c2c/orders/{orderID}", application.requireReadyAccount(http.HandlerFunc(application.c2cOrder)))
	mux.Handle("GET /api/c2c/orders/{orderID}/payment-methods/{methodID}/qr", application.requireReadyAccount(http.HandlerFunc(application.c2cPaymentQR)))
	mux.Handle("POST /api/c2c/orders/{orderID}/take", application.requireReadyAccount(http.HandlerFunc(application.c2cTakeOrder)))
	mux.Handle("POST /api/c2c/orders/{orderID}/cancel", application.requireReadyAccount(http.HandlerFunc(application.c2cCancelOrder)))
	mux.Handle("GET /api/c2c/me", application.requireReadyAccount(http.HandlerFunc(application.c2cMyActivity)))
	mux.Handle("GET /api/c2c/trades/{tradeID}", application.requireReadyAccount(http.HandlerFunc(application.c2cTrade)))
	mux.Handle("POST /api/c2c/trades/{tradeID}/paid", application.requireReadyAccount(http.HandlerFunc(application.c2cMarkPaid)))
	mux.Handle("POST /api/c2c/trades/{tradeID}/cancel", application.requireReadyAccount(http.HandlerFunc(application.c2cCancelTrade)))
	mux.Handle("POST /api/c2c/trades/{tradeID}/release", application.requireReadyAccount(http.HandlerFunc(application.c2cConfirmReceipt)))
	mux.Handle("POST /api/c2c/trades/{tradeID}/dispute", application.requireReadyAccount(http.HandlerFunc(application.c2cOpenDispute)))
	mux.Handle("POST /api/c2c/trades/{tradeID}/evidence", application.requireReadyAccount(http.HandlerFunc(application.c2cAddEvidence)))
	mux.Handle("GET /api/c2c/evidence/{evidenceID}", application.requireReadyAccount(http.HandlerFunc(application.c2cEvidence)))
	mux.Handle("GET /api/admin/accounts", application.requireAdmin(http.HandlerFunc(application.listAccounts)))
	mux.Handle("POST /api/admin/accounts", application.requireAdmin(http.HandlerFunc(application.createAccount)))
	mux.Handle("PATCH /api/admin/accounts/{accountID}", application.requireAdmin(http.HandlerFunc(application.updateAccount)))
	mux.Handle("POST /api/admin/accounts/{accountID}/password-reset", application.requireAdmin(http.HandlerFunc(application.resetAccountPassword)))
	mux.Handle("GET /api/admin/models", application.requireAdmin(http.HandlerFunc(application.listAdminModels)))
	mux.Handle("POST /api/admin/models", application.requireAdmin(http.HandlerFunc(application.createModel)))
	mux.Handle("GET /api/admin/models/{modelID...}", application.requireAdmin(http.HandlerFunc(application.getAdminModel)))
	mux.Handle("PUT /api/admin/models/{modelID...}", application.requireAdmin(http.HandlerFunc(application.updateModel)))
	mux.Handle("GET /api/admin/ledger/metrics", application.requireAdmin(http.HandlerFunc(application.ledgerMetrics)))
	mux.Handle("GET /api/admin/ops/metrics", application.requireAdmin(http.HandlerFunc(application.opsMetrics)))
	mux.Handle("GET /api/admin/ops/providers", application.requireAdmin(http.HandlerFunc(application.opsProviderIncome)))
	mux.Handle("GET /api/admin/ops/anomalies", application.requireAdmin(http.HandlerFunc(application.opsAnomalies)))
	mux.Handle("GET /api/admin/ops/inspections", application.requireAdmin(http.HandlerFunc(application.opsListInspections)))
	mux.Handle("POST /api/admin/ops/inspections", application.requireAdmin(http.HandlerFunc(application.opsRunInspection)))
	mux.Handle("GET /api/admin/ops/trial-summary", application.requireAdmin(http.HandlerFunc(application.opsTrialSummary)))
	mux.Handle("GET /api/admin/ledger/accounts/{accountID}/wallet", application.requireAdmin(http.HandlerFunc(application.adminLedgerAccountWallet)))
	mux.Handle("GET /api/admin/ledger/accounts/{accountID}/entries", application.requireAdmin(http.HandlerFunc(application.adminLedgerAccountEntries)))
	mux.Handle("GET /api/admin/ledger/system-accounts/{systemKind}/wallet", application.requireAdmin(http.HandlerFunc(application.adminLedgerSystemWallet)))
	mux.Handle("GET /api/admin/ledger/system-accounts/{systemKind}/entries", application.requireAdmin(http.HandlerFunc(application.adminLedgerSystemEntries)))
	mux.Handle("POST /api/admin/ledger/adjustments", application.requireAdmin(http.HandlerFunc(application.adminLedgerAdjustment)))
	mux.Handle("POST /api/admin/ledger/bad-debts", application.requireAdmin(http.HandlerFunc(application.adminBadDebtTransfer)))
	mux.Handle("GET /api/admin/channels", application.requireAdmin(http.HandlerFunc(application.listAdminChannels)))
	mux.Handle("GET /api/admin/channels/{channelID}", application.requireAdmin(http.HandlerFunc(application.getAdminChannel)))
	mux.Handle("POST /api/admin/channels/{channelID}/pause", application.requireAdmin(http.HandlerFunc(application.adminPauseChannel)))
	mux.Handle("DELETE /api/admin/channels/{channelID}", application.requireAdmin(http.HandlerFunc(application.adminDeleteChannel)))
	mux.Handle("POST /api/admin/channel-offers/{offerID}/validation-attempts", application.requireAdmin(http.HandlerFunc(application.validateChannelOffer)))
	mux.Handle("GET /api/admin/channel-offers/{offerID}/validation-attempts", application.requireAdmin(http.HandlerFunc(application.listOfferValidationAttempts)))
	mux.Handle("POST /api/admin/channel-credentials/reencrypt", application.requireAdmin(http.HandlerFunc(application.reencryptChannelCredentials)))
	mux.HandleFunc("/v1/chat/completions", application.proxyChatCompletions)
	mux.HandleFunc("/v1/responses", application.proxyResponses)
	mux.HandleFunc("/v1/messages", application.proxyAnthropicMessages)
	mux.HandleFunc("/v1beta/models/{model...}", application.proxyGemini)
	mux.Handle("GET /api/admin/c2c/disputes", application.requireAdmin(http.HandlerFunc(application.adminC2CDisputes)))
	mux.Handle("POST /api/admin/c2c/orders/{orderID}/cancel", application.requireAdmin(http.HandlerFunc(application.adminC2CCancelOrder)))
	mux.Handle("POST /api/admin/c2c/trades/{tradeID}/resolve", application.requireAdmin(http.HandlerFunc(application.adminC2CResolve)))

	return responseWriteDeadline(securityHeaders(application.requireSameOrigin(mux)))
}

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
		if isExternalGatewayPath(r.URL.Path) {
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
	return path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages" || strings.HasPrefix(path, "/v1beta/models/")
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
	remoteIP, ok := requestRemoteIP(r)
	if !ok {
		return false
	}
	for _, prefix := range a.trustedProxyCIDRs {
		if prefix.Contains(remoteIP) {
			return true
		}
	}
	return false
}

func requestRemoteIP(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return remoteIP.Unmap(), true
}

func (a *app) loginLimitKey(r *http.Request, username string) string {
	return a.loginClientIP(r) + "\x00" + identity.NormalizeUsername(username)
}

func (a *app) loginClientIP(r *http.Request) string {
	if a.trustsProxy(r) {
		if forwardedIP, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return forwardedIP.Unmap().String()
		}
	}
	if remoteIP, ok := requestRemoteIP(r); ok {
		return remoteIP.String()
	}
	return "unknown"
}

func (a *app) allowLoginAttempt(ipKey, pairKey string) bool {
	return a.loginIPAttempts.take(ipKey) && a.loginAttempts.take(pairKey)
}

func (a *app) allowPasswordChangeAttempt(ipKey, accountID string) bool {
	return a.passwordChangeIPs.take(ipKey) && a.passwordChanges.take(accountID)
}

type loginAttempt struct {
	failures []time.Time
}

type loginLimiter struct {
	mu         sync.Mutex
	attempts   map[string]loginAttempt
	limit      int
	window     time.Duration
	maxEntries int
	now        func() time.Time
}

func newLoginLimiter(limit int, window time.Duration, maxEntries int) *loginLimiter {
	return &loginLimiter{
		attempts:   make(map[string]loginAttempt),
		limit:      limit,
		window:     window,
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.attempts) >= l.maxEntries {
		for existingKey, attempt := range l.attempts {
			if len(recentFailures(attempt.failures, now.Add(-l.window))) == 0 {
				delete(l.attempts, existingKey)
			}
		}
	}
	attempt, exists := l.attempts[key]
	if !exists && len(l.attempts) >= l.maxEntries {
		return false
	}
	attempt.failures = recentFailures(attempt.failures, now.Add(-l.window))
	if len(attempt.failures) == 0 {
		delete(l.attempts, key)
	} else {
		l.attempts[key] = attempt
	}
	return len(attempt.failures) < l.limit
}

func (l *loginLimiter) failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	attempt.failures = append(recentFailures(attempt.failures, l.now().Add(-l.window)), l.now())
	l.attempts[key] = attempt
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *loginLimiter) take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.attempts) >= l.maxEntries {
		for existingKey, attempt := range l.attempts {
			if len(recentFailures(attempt.failures, now.Add(-l.window))) == 0 {
				delete(l.attempts, existingKey)
			}
		}
	}
	attempt, exists := l.attempts[key]
	if !exists && len(l.attempts) >= l.maxEntries {
		return false
	}
	attempt.failures = recentFailures(attempt.failures, now.Add(-l.window))
	if len(attempt.failures) >= l.limit {
		l.attempts[key] = attempt
		return false
	}
	attempt.failures = append(attempt.failures, now)
	l.attempts[key] = attempt
	return true
}

func recentFailures(failures []time.Time, cutoff time.Time) []time.Time {
	firstRecent := 0
	for firstRecent < len(failures) && failures[firstRecent].Before(cutoff) {
		firstRecent++
	}
	return failures[firstRecent:]
}

func acquirePasswordSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if a.databaseReady != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.databaseReady(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "oh-my-aihub-backend",
		"status":  "ok",
	})
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

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, c2c.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, identity.ErrNotFound), errors.Is(err, catalog.ErrNotFound), errors.Is(err, ledger.ErrNotFound), errors.Is(err, channel.ErrNotFound), errors.Is(err, gateway.ErrNotFound), errors.Is(err, c2c.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, identity.ErrConflict), errors.Is(err, catalog.ErrConflict), errors.Is(err, channel.ErrConflict), errors.Is(err, gateway.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "资源状态冲突或标识已被使用")
	case errors.Is(err, ledger.ErrConflict), errors.Is(err, ledger.ErrHoldClosed), errors.Is(err, ledger.ErrHoldAmountExceeded):
		writeError(w, http.StatusConflict, "ledger_conflict", "账本操作与当前状态冲突")
	case errors.Is(err, c2c.ErrConflict):
		writeError(w, http.StatusConflict, "c2c_conflict", "订单或交易状态已变化，请刷新后重试")
	case errors.Is(err, c2c.ErrExpired):
		writeError(w, http.StatusConflict, "payment_deadline_expired", "付款期限已结束")
	case errors.Is(err, ledger.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_spendable_capacity", "可消费额度不足")
	case errors.Is(err, ledger.ErrCreditFrozen):
		writeError(w, http.StatusForbidden, "credit_frozen", "账户信用已冻结")
	case errors.Is(err, channel.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, gateway.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, gateway.ErrSnapshotRetry):
		writeError(w, http.StatusConflict, "snapshot_conflict", "资源正在更新，请重试")
	case errors.Is(err, channel.ErrUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "channel_unavailable", "至少需要一个通过当前验证的可用报价")
	case errors.Is(err, channel.ErrUnsafeUpstream):
		writeError(w, http.StatusUnprocessableEntity, "unsafe_upstream", "Base URL 无法通过安全解析")
	case errors.Is(err, identity.ErrInvalidInput), errors.Is(err, catalog.ErrInvalidInput), errors.Is(err, channel.ErrInvalidInput), errors.Is(err, gateway.ErrInvalidInput), errors.Is(err, ledger.ErrInvalidInput), errors.Is(err, ledger.ErrUnbalanced), errors.Is(err, ledger.ErrAmountOverflow), errors.Is(err, money.ErrInvalidAmount), errors.Is(err, c2c.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作")
	}
}

func accountResponse(account identity.Account) map[string]any {
	effectiveCredit := account.CreditLimit
	if account.CreditFrozen {
		effectiveCredit = 0
	}
	capacity := ledger.SpendableCapacity(account.PostedBalance, effectiveCredit, account.AssetReserved, account.SpendAuthorized)
	if account.CreditFrozen {
		capacity = 0
	}
	overLimit := ledger.IsOverLimit(account.PostedBalance, effectiveCredit)
	return map[string]any{
		"id":                     account.ID,
		"username":               account.Username,
		"display_name":           account.DisplayName,
		"is_admin":               account.IsAdmin,
		"status":                 account.Status,
		"must_change_password":   account.MustChangePassword,
		"version":                account.Version,
		"credit_limit":           account.CreditLimit.String(),
		"credit_frozen":          account.CreditFrozen,
		"posted_balance":         account.PostedBalance.String(),
		"asset_reserved":         account.AssetReserved.String(),
		"spend_authorized":       account.SpendAuthorized.String(),
		"effective_credit_limit": effectiveCredit.String(),
		"credit_used":            ledger.CreditUsed(account.PostedBalance).String(),
		"spendable_capacity":     capacity.String(),
		"over_limit":             overLimit,
		"created_at":             account.CreatedAt,
		"updated_at":             account.UpdatedAt,
		"password_changed_at":    account.PasswordChangedAt,
	}
}

func modelResponse(model catalog.Model) map[string]any {
	return map[string]any{
		"id":                         model.ID,
		"name":                       model.Name,
		"provider":                   model.Provider,
		"context_window":             model.ContextWindow,
		"parameter_info":             model.ParameterInfo,
		"input_modalities":           model.InputModalities,
		"output_modalities":          model.OutputModalities,
		"supports_tools":             model.SupportsTools,
		"supports_structured_output": model.SupportsStructuredOutput,
		"supports_vision":            model.SupportsVision,
		"input_price":                model.InputPrice.String(),
		"output_price":               model.OutputPrice.String(),
		"cache_write_price":          model.CacheWritePrice.String(),
		"cache_read_price":           model.CacheReadPrice.String(),
		"price_tiers":                benchmarkPriceTierResponses(model.PriceTiers),
		"price_unit":                 "points_per_million_tokens",
		"status":                     model.Status,
		"version":                    model.Version,
		"created_at":                 model.CreatedAt,
		"updated_at":                 model.UpdatedAt,
		"price_updated_at":           model.PriceUpdatedAt,
	}
}

var modelPricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func parseModelPrice(value string) (money.Amount, error) {
	value = strings.TrimSpace(value)
	if !modelPricePattern.MatchString(value) {
		return 0, money.ErrInvalidAmount
	}
	amount, err := money.Parse(value)
	if err != nil || amount > catalog.MaxPriceNanoPerMillion {
		return 0, money.ErrInvalidAmount
	}
	return amount, nil
}

func parseModelRequest(request modelRequest) (catalog.Model, error) {
	inputPrice, err := parseModelPrice(request.InputPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	outputPrice, err := parseModelPrice(request.OutputPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	cacheWritePrice, err := parseModelPrice(request.CacheWritePrice)
	if err != nil {
		return catalog.Model{}, err
	}
	cacheReadPrice, err := parseModelPrice(request.CacheReadPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	priceTiers, err := parsePriceTierRequests(request.PriceTiers)
	if err != nil {
		return catalog.Model{}, err
	}
	return catalog.Model{
		ID:                       strings.TrimSpace(request.ID),
		Name:                     request.Name,
		Provider:                 request.Provider,
		ContextWindow:            request.ContextWindow,
		ParameterInfo:            request.ParameterInfo,
		InputModalities:          request.InputModalities,
		OutputModalities:         request.OutputModalities,
		SupportsTools:            request.SupportsTools,
		SupportsStructuredOutput: request.SupportsStructuredOutput,
		SupportsVision:           request.SupportsVision,
		InputPrice:               inputPrice,
		OutputPrice:              outputPrice,
		CacheWritePrice:          cacheWritePrice,
		CacheReadPrice:           cacheReadPrice,
		PriceTiers:               priceTiers,
		Status:                   catalog.Status(request.Status),
	}, nil
}

func parsePriceTierRequests(requests []priceTierRequest) ([]ledger.PriceTier, error) {
	if requests == nil {
		return nil, nil
	}
	tiers := make([]ledger.PriceTier, 0, len(requests))
	for _, request := range requests {
		inputPrice, err := parseModelPrice(request.InputPrice)
		if err != nil {
			return nil, err
		}
		outputPrice, err := parseModelPrice(request.OutputPrice)
		if err != nil {
			return nil, err
		}
		cacheWritePrice, err := parseModelPrice(request.CacheWritePrice)
		if err != nil {
			return nil, err
		}
		cacheReadPrice, err := parseModelPrice(request.CacheReadPrice)
		if err != nil {
			return nil, err
		}
		tiers = append(tiers, ledger.PriceTier{
			Name: request.Name, MinPromptTokens: request.MinPromptTokens, MaxPromptTokens: request.MaxPromptTokens,
			Timezone: request.Timezone, Weekdays: request.Weekdays,
			StartMinute: request.StartMinute, EndMinute: request.EndMinute,
			InputPrice: inputPrice, OutputPrice: outputPrice,
			CacheWritePrice: cacheWritePrice, CacheReadPrice: cacheReadPrice,
		})
	}
	return tiers, nil
}
