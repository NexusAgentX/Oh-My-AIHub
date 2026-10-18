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
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

const (
	defaultSessionCookie = "oma_session"
	defaultWriteTimeout  = 30 * time.Second
)

type Dependencies struct {
	Typical       *typical.Service
	GroupChannels *groupchannel.Service
	DetectionMCP  *detection.MCPFixture
	Contributions *contribution.Service
	Detection     *detection.Service
	Forum         *forum.Service
	Identity      *identity.Service
	Catalog       *catalog.Service
	Ledger        *ledger.Service
	Settings      *settings.Service
	// RateSync fetches the USD/CNY rate on demand; nil disables manual sync.
	RateSync          *settings.RateSyncer
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
	typical           *typical.Service
	groupChannels     *groupchannel.Service
	detectionMCP      *detection.MCPFixture
	contributions     *contribution.Service
	detection         *detection.Service
	forum             *forum.Service
	identity          *identity.Service
	catalog           *catalog.Service
	ledger            *ledger.Service
	settings          *settings.Service
	rateSync          *settings.RateSyncer
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
	gatewayEndpoints  map[string]bool
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
		typical:           dependencies.Typical,
		groupChannels:     dependencies.GroupChannels,
		detectionMCP:      dependencies.DetectionMCP,
		contributions:     dependencies.Contributions,
		detection:         dependencies.Detection,
		forum:             dependencies.Forum,
		identity:          dependencies.Identity,
		catalog:           dependencies.Catalog,
		ledger:            dependencies.Ledger,
		settings:          dependencies.Settings,
		rateSync:          dependencies.RateSync,
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
	application.registerAdminSettingsRoutes(routes)
	application.registerKeyRoutes(routes)
	application.registerBrowseRoutes(routes)
	application.registerDirectoryAnalysisRoutes(routes)
	application.registerChannelRoutes(routes)
	application.registerGatewayRoutes(routes)
	application.registerC2CRoutes(routes)
	application.registerObserveRoutes(routes)
	application.registerForumRoutes(routes)
	application.registerDetectionRoutes(routes)
	application.registerContributionRoutes(routes)
	application.registerGroupChannelRoutes(routes)
	application.registerTypicalRoutes(routes)
	application.registerMarketBrowseRoutes(routes)

	return chain(routes.mux, responseWriteDeadline, securityHeaders, application.requireSameOrigin), routes.table
}

// chain 按“先写者在外”的顺序包裹处理器。
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return handler
}
