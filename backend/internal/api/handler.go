package api

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const (
	defaultSessionCookie = "oma_session"
	defaultWriteTimeout  = 30 * time.Second
)

type Dependencies struct {
	Identity          *identity.Service
	Catalog           *catalog.Service
	Ledger            *ledger.Service
	Settings          *settings.Service
	Audit             *audit.Service
	Keys              *apikey.Service
	Channels          *channel.Service
	Routing           routing.Store
	Gateway           *gateway.Engine
	Browse            gateway.Browse
	C2C               *c2c.Service
	Observe           *observe.Service
	Feed              *observe.Feed
	DatabaseReady     func(context.Context) error
	CookieSecure      bool
	TrustedProxyCIDRs []netip.Prefix
}

type app struct {
	identity          *identity.Service
	catalog           *catalog.Service
	ledger            *ledger.Service
	settings          *settings.Service
	audit             *audit.Service
	keys              *apikey.Service
	channels          *channel.Service
	routing           routing.Store
	gateway           *gateway.Engine
	browse            gateway.Browse
	c2c               *c2c.Service
	observe           *observe.Service
	feed              *observe.Feed
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
		ledger:            dependencies.Ledger,
		settings:          dependencies.Settings,
		audit:             dependencies.Audit,
		keys:              dependencies.Keys,
		channels:          dependencies.Channels,
		routing:           dependencies.Routing,
		gateway:           dependencies.Gateway,
		browse:            dependencies.Browse,
		c2c:               dependencies.C2C,
		observe:           dependencies.Observe,
		feed:              dependencies.Feed,
		databaseReady:     dependencies.DatabaseReady,
		cookieSecure:      dependencies.CookieSecure,
		cookieName:        defaultSessionCookie,
		trustedProxyCIDRs: append([]netip.Prefix(nil), dependencies.TrustedProxyCIDRs...),
		rateLimits:        newRateLimits(),
	}

	routes := newRouter(application)
	application.registerInstanceRoutes(routes)
	application.registerIdentityRoutes(routes)
	application.registerPointsRoutes(routes)
	application.registerAdminAccountRoutes(routes)
	application.registerAdminModelRoutes(routes)
	application.registerAdminSettingsRoutes(routes)
	application.registerKeyRoutes(routes)
	application.registerBrowseRoutes(routes)
	application.registerChannelRoutes(routes)
	application.registerGatewayRoutes(routes)
	application.registerC2CRoutes(routes)
	application.registerObserveRoutes(routes)

	return chain(routes.mux, responseWriteDeadline, securityHeaders, application.requireSameOrigin), routes.table
}

// chain 按“先写者在外”的顺序包裹处理器。
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return handler
}
