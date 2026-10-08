package api

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/dashboard"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
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
	FeeRates          *feerate.Service
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
}

type app struct {
	identity          *identity.Service
	catalog           *catalog.Service
	channels          *channel.Service
	gateway           *gateway.Service
	ledger            *ledger.Service
	c2c               *c2c.Service
	dashboard         *dashboard.Service
	ops               OpsStore
	feeRates          *feerate.Service
	databaseReady     func(context.Context) error
	cookieSecure      bool
	cookieName        string
	trustedProxyCIDRs []netip.Prefix
	rateLimits
}

// NewHandler 组装依赖、按领域注册路由，并套上全局中间件链。
func NewHandler(dependencies Dependencies) http.Handler {
	handler, _ := buildHandler(dependencies)
	return handler
}

// buildHandler 同时返回路由表，供契约测试与 OpenAPI 规范对照。
func buildHandler(dependencies Dependencies) (http.Handler, []route) {
	application := &app{
		identity:          dependencies.Identity,
		catalog:           dependencies.Catalog,
		channels:          dependencies.Channels,
		gateway:           dependencies.Gateway,
		ledger:            dependencies.Ledger,
		c2c:               dependencies.C2C,
		dashboard:         dashboard.New(dependencies.Gateway, dependencies.Channels, dependencies.C2C),
		ops:               dependencies.Ops,
		feeRates:          dependencies.FeeRates,
		databaseReady:     dependencies.DatabaseReady,
		cookieSecure:      dependencies.CookieSecure,
		cookieName:        defaultSessionCookie,
		trustedProxyCIDRs: append([]netip.Prefix(nil), dependencies.TrustedProxyCIDRs...),
		rateLimits:        newRateLimits(),
	}

	routes := newRouter(application)
	application.registerInstanceRoutes(routes)
	application.registerIdentityRoutes(routes)
	application.registerCatalogRoutes(routes)
	application.registerLedgerRoutes(routes)
	application.registerChannelRoutes(routes)
	application.registerGatewayRoutes(routes)
	application.registerDashboardRoutes(routes)
	application.registerC2CRoutes(routes)
	application.registerFeeRateRoutes(routes)
	application.registerOpsRoutes(routes)

	return chain(routes.mux, responseWriteDeadline, securityHeaders, application.requireSameOrigin), routes.table
}

// chain 按“先写者在外”的顺序包裹处理器。
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return handler
}
