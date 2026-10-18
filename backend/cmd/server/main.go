package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/geolocation"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/keyringcheck"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/metrics"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/contributionpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/detectionpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/groupchannelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/marketpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/resourcepg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/settingspg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/typicalpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

// keyringCheckTimeout bounds the startup decryption of every stored secret.
const keyringCheckTimeout = 2 * time.Minute

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fatal(logger, "DATABASE_URL is required", nil)
	}
	cookieSecure := true
	if value := os.Getenv("COOKIE_SECURE"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			fatal(logger, "COOKIE_SECURE must be true or false", err)
		}
		cookieSecure = parsed
	}
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		fatal(logger, "invalid trusted proxy configuration", err)
	}
	credentialKeyring, err := channel.ParseKeyring(
		os.Getenv("UPSTREAM_CREDENTIAL_KEYRING"),
		os.Getenv("UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID"),
	)
	if err != nil {
		fatal(logger, "invalid upstream credential keyring", err)
	}
	outboundPolicy, err := channel.NewOutboundPolicy(
		parseCommaSeparated(os.Getenv("UPSTREAM_ALLOWED_PORTS")),
		parseCommaSeparated(os.Getenv("UPSTREAM_BLOCKED_HOSTS")),
	)
	if err != nil {
		fatal(logger, "invalid upstream outbound policy", err)
	}

	if value := os.Getenv("UPSTREAM_ALLOW_LOCALHOST"); value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			fatal(logger, "UPSTREAM_ALLOW_LOCALHOST must be true or false", err)
		}
		if allow {
			outboundPolicy = outboundPolicy.WithLocalhostForDevelopment()
			logger.Warn("localhost upstreams enabled for local development; HTTP to localhost is allowed")
		}
	}

	c2cKeyring, err := c2c.ParseKeyring(
		os.Getenv("C2C_PRIVATE_DATA_KEYRING"),
		os.Getenv("C2C_PRIVATE_DATA_ACTIVE_KEY_ID"),
	)
	if err != nil {
		fatal(logger, "invalid C2C private data keyring", err)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	pool, err := database.Open(startupContext, databaseURL)
	if err != nil {
		fatal(logger, "opening the database failed", err)
	}
	defer pool.Close()
	store := postgres.New(pool)
	// Prove every stored secret still opens with the configured keyrings before
	// serving anything: a missing historical key ID must stop the start, not
	// surface when someone uses the channel, API key or order.
	checkContext, cancelCheck := context.WithTimeout(context.Background(), keyringCheckTimeout)
	checked, err := keyringcheck.Verify(checkContext,
		keyringcheck.Sources{Channels: store.ChannelHistory, APIKeys: store.Keys, C2COrders: store.C2C},
		keyringcheck.Keyrings{Upstream: credentialKeyring, C2C: c2cKeyring})
	cancelCheck()
	if err != nil {
		fatal(logger, "stored secrets cannot be decrypted with the configured keyrings", err)
	}
	logger.Info("stored secrets decrypted with the configured keyrings",
		"channels", checked.Channels, "api_keys", checked.APIKeys, "c2c_orders", checked.C2COrders)
	// The gateway reads keys, the catalog, channels and routing preferences from
	// memory (ADR-0040). Every service that writes them is built on the cached
	// stores, so each change rebuilds the affected snapshot before it returns.
	groupStartup, cancelGroupStartup := context.WithTimeout(context.Background(), 15*time.Second)
	groupPool, err := database.OpenLimited(groupStartup, databaseURL, 2)
	cancelGroupStartup()
	if err != nil {
		fatal(logger, "opening group channel database pool failed", err)
	}
	defer groupPool.Close()
	groupStore := groupchannelpg.NewStore(groupPool)
	groupRuntime := &groupchannel.Runtime{Repository: groupStore, Removed: credentialKeyring.ForgetCredential}
	cached := store.Cached(logger, func(config *snapshot.Config) { config.Loader = groupRuntime.Wrap(config.Loader) })
	groupRuntime.Rebuild = func(ctx context.Context) error {
		return cached.Cache.InvalidateChecked(ctx, snapshot.DomainFleet|snapshot.DomainModels)
	}
	loadContext, cancelLoad := context.WithTimeout(context.Background(), keyringCheckTimeout)
	err = cached.Cache.Load(loadContext)
	cancelLoad()
	if err != nil {
		fatal(logger, "loading the gateway snapshots failed", err)
	}
	identityService, err := identity.NewService(cached.Identity, 24*time.Hour)
	if err != nil {
		fatal(logger, "creating the identity service failed", err)
	}

	catalogService := catalog.NewService(cached.Catalog)
	settingsService := settings.NewService(store.Settings)
	modelIDs := func(ctx context.Context) ([]string, error) {
		models, err := cached.Models.List(ctx, true)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids, nil
	}
	keyModelIDs := modelIDs
	bus := gateway.NewBus()
	var platformMetrics *metrics.Metrics
	runtime := gateway.NewRuntime(func(channelID, kind, reason string) {
		event := gateway.Event{Kind: channelEventKind(kind), At: time.Now(), ChannelID: channelID, Detail: reason}
		bus.Publish(event)
		if platformMetrics != nil {
			platformMetrics.Publish(event)
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.Gateway.RecordChannelEvent(ctx, channelID, kind, reason); err != nil {
				logger.Error("recording channel event failed", "channel_id", channelID, "kind", kind, "error", err)
			}
		}()
	})
	platformMetrics = metrics.New(func(channelID string, now time.Time) bool { return !runtime.CooldownUntil(channelID, now).IsZero() })
	gatewayLocation, err := geolocation.ParseLocation(os.Getenv("GATEWAY_LOCATION_LABEL"), os.Getenv("GATEWAY_LATITUDE"), os.Getenv("GATEWAY_LONGITUDE"))
	if err != nil {
		fatal(logger, "invalid gateway location", err)
	}
	var geo *geolocation.Service
	geoEnabled := true
	if value := os.Getenv("GEOIP_ENABLED"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			fatal(logger, "GEOIP_ENABLED must be true or false", err)
		}
		geoEnabled = parsed
	}
	if geoEnabled {
		geo = geolocation.New(store.Gateway)
		warmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := geo.Warm(warmCtx); err != nil {
			logger.Warn("geolocation cache unavailable; background lookups will retry")
		}
		cancel()
	}

	resourceStartup, stopResourceStartup := context.WithTimeout(context.Background(), keyringCheckTimeout)
	resourcePool, err := database.OpenLimited(resourceStartup, databaseURL, 2)
	if err != nil {
		fatal(logger, "opening resource metadata pool failed", err)
	}
	defer resourcePool.Close()
	cursorRoot, err := store.Market.ProtectedRoot(resourceStartup, credentialKeyring, "cursor-root-v1")
	if err != nil {
		fatal(logger, "opening resource cursor root failed", err)
	}
	resourceCursors, err := gatewaycontract.NewCursorCodec(cursorRoot)
	clear(cursorRoot)
	if err != nil {
		fatal(logger, "initializing resource cursor codec failed", err)
	}
	contextRoot, err := store.Market.ProtectedRoot(resourceStartup, credentialKeyring, "codex-context-root-v1")
	if err != nil {
		fatal(logger, "opening Codex context root failed", err)
	}
	contextNamespace, err := gatewaycontract.NewContextNamespace(contextRoot)
	clear(contextRoot)
	stopResourceStartup()
	if err != nil {
		fatal(logger, "initializing Codex context namespace failed", err)
	}
	engine := gateway.NewEngine(gateway.Dependencies{
		ContextNamespace: contextNamespace,
		Resources:        resourcepg.NewStore(resourcePool), ResourceCursors: resourceCursors,
		TrustedProxyCIDRs: trustedProxyCIDRs, Geo: geo, GatewayLocation: gatewayLocation, LocationSource: geoSource(geoEnabled),
		Ready: groupRuntime.Check, Jobs: store.Gateway, Store: cached.Gateway, Catalog: cached.Models, Settings: settingsService, Routing: cached.Routing,
		Keyring: credentialKeyring, Outbound: outboundPolicy, Events: gateway.Publishers{bus, platformMetrics}, Telemetry: platformMetrics, Logger: logger, Runtime: runtime,
	})
	backgroundCheckCtx, cancelBackgroundCheck := context.WithTimeout(context.Background(), keyringCheckTimeout)
	if err := store.Gateway.VerifyBackgroundCredentials(backgroundCheckCtx, credentialKeyring); err != nil {
		fatal(logger, "checking background credentials failed", err)
	}
	if err := engine.RestoreBackground(backgroundCheckCtx); err != nil {
		fatal(logger, "restoring background tasks failed", err)
	}
	cancelBackgroundCheck()
	// An edit of a channel (settings, models, formats, suspension) clears what the
	// 4xx anomaly rule counted against it before, and a channel ranked last for
	// those answers is ranked on its merits again before the edit returns.
	cached.Cache.OnChannelEdited(engine.ChannelEdited)
	groupRuntime.Edited = engine.ChannelEdited
	engineContext, stopEngine := context.WithCancel(context.Background())
	defer stopEngine()
	if geo != nil {
		go geo.Run(engineContext)
	}
	// The channel statistics are counted in memory from here on; the database is
	// asked once for the window they start from. Without it the gateway starts
	// with every channel unrated, which ranks by price alone until it has seen
	// traffic.
	statsContext, cancelStats := context.WithTimeout(context.Background(), keyringCheckTimeout)
	if err := engine.LoadStats(statsContext); err != nil {
		logger.Error("loading the channel statistics failed, starting without them", "error", err)
	}
	cancelStats()
	go engine.RunReaper(engineContext, time.Minute)
	go engine.RunRefresh(engineContext)
	go engine.RunBackground(engineContext)
	go cached.Cache.Run(engineContext)

	c2cService := c2c.NewService(store.C2C, c2cKeyring)
	backgroundContext, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	go runC2CExpiry(backgroundContext, c2cService, logger)
	forumService := forum.NewService(store.Forum)
	go runForumCleanup(backgroundContext, forumService, logger)
	rateSyncer := settings.NewRateSyncer(store.Settings, settings.NewFrankfurterSource())
	go runExchangeRateSync(backgroundContext, rateSyncer, logger)

	observeService := observe.NewService(store.Observe)
	feed := observe.NewFeed(observeService, bus, logger)
	platformMetrics.SetFeedHealth(bus.Dropped, feed.Dropped, feed.ReadFailures)
	platformMetrics.SetEgress(outboundPolicy.EgressStats)
	go feed.Run(backgroundContext)
	platformMetrics.SetDiagnosticHealth(store.Gateway.DiagnosticDropped, store.Gateway.DiagnosticFailed)
	diagnosticsDone := make(chan struct{})
	go func() { defer close(diagnosticsDone); store.Gateway.RunDiagnostics(backgroundContext) }()
	defer func() { stopBackground(); <-diagnosticsDone }()
	go platformMetrics.Run(backgroundContext, observeService, time.Minute, logger)
	go refreshMetricModels(backgroundContext, platformMetrics, modelIDs, logger)
	go runErrorScrub(backgroundContext, observeService, logger)
	metricsServer := newMetricsServer(os.Getenv("METRICS_ADDR"), platformMetrics.Handler())
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics listener stopped", "error", err)
		}
	}()

	// Detector state/queries use two private connections; saturation cannot
	// consume gateway/settlement pool slots. Missing protocol suites remain in
	// the full plan; implemented scenarios can execute without authorizing an
	// incomplete creation report.
	detectorStartup, cancelDetectorStartup := context.WithTimeout(context.Background(), keyringCheckTimeout)
	defer cancelDetectorStartup()
	detectorPool, err := database.OpenLimited(detectorStartup, databaseURL, 2)
	if err != nil {
		fatal(logger, "opening detection database pool failed", err)
	}
	defer detectorPool.Close()
	detectorMarket := marketpg.NewStore(detectorPool)
	if err = detectorMarket.VerifyProtectedRoots(detectorStartup, credentialKeyring); err != nil {
		fatal(logger, "protected market roots cannot be opened", err)
	}
	credentialRoot, err := detectorMarket.ProtectedRoot(detectorStartup, credentialKeyring, "credential-scope-root-v1")
	cancelDetectorStartup()
	if err != nil {
		fatal(logger, "opening detection credential root failed", err)
	}
	manifest, err := gatewaycontract.Load()
	if err != nil {
		fatal(logger, "loading detection manifest failed", err)
	}
	registry, err := detection.NewRegistry(manifest)
	if err != nil {
		fatal(logger, "loading detection registry failed", err)
	}
	if err = registry.RegisterNative(); err != nil {
		fatal(logger, "registering native detection suites failed", err)
	}
	if err = registry.RegisterCodexNative(); err != nil {
		fatal(logger, "registering Codex native detection suites failed", err)
	}
	if err = registry.RegisterCodexGateway(); err != nil {
		fatal(logger, "registering Codex gateway detection suites failed", err)
	}
	var mcpFixture *detection.MCPFixture
	if origin := strings.TrimSpace(os.Getenv("AIHUB_DETECTION_PUBLIC_ORIGIN")); origin != "" {
		mcpFixture, err = detection.NewMCPFixture(origin)
		if err != nil {
			fatal(logger, "invalid detection callback origin", err)
		}
	}
	if err = registry.RegisterMCP(mcpFixture); err != nil {
		fatal(logger, "registering MCP detection failed", err)
	}
	sdkBinary := strings.TrimSpace(os.Getenv("AIHUB_DETECTION_SDK_BINARY"))
	if sdkBinary == "" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			fatal(logger, "locating SDK detection artifact failed", executableErr)
		}
		sdkBinary = filepath.Join(filepath.Dir(executable), "aihub-detection-sdk")
	}
	sdkStartup, cancelSDKStartup := context.WithTimeout(context.Background(), 15*time.Second)
	err = registry.RegisterSDK(sdkStartup, sdkBinary)
	cancelSDKStartup()
	if err != nil {
		fatal(logger, "validating SDK detection artifact failed", err)
	}
	probePath := strings.TrimSpace(os.Getenv("AIHUB_DETECTION_CODEX_PROBE"))
	codexPath := strings.TrimSpace(os.Getenv("AIHUB_CODEX_BINARY"))
	if probePath == "" || codexPath == "" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			fatal(logger, "locating Codex detection artifacts failed", executableErr)
		}
		if probePath == "" {
			probePath = filepath.Join(filepath.Dir(executable), "aihub-detection-codex")
		}
		if codexPath == "" {
			codexPath = filepath.Join(filepath.Dir(executable), "codex")
		}
	}
	codexStartup, cancelCodexStartup := context.WithTimeout(context.Background(), 25*time.Second)
	err = registry.RegisterCodexCLI(codexStartup, probePath, codexPath)
	cancelCodexStartup()
	if err != nil {
		fatal(logger, "validating Codex detection artifacts failed", err)
	}
	// Review transactions and directory rebuilds use two separate connections;
	// no long review transaction holds a gateway or detector pool slot.
	reviewStartup, cancelReviewStartup := context.WithTimeout(context.Background(), keyringCheckTimeout)
	reviewPool, err := database.OpenLimited(reviewStartup, databaseURL, 2)
	if err != nil {
		fatal(logger, "opening contribution database pool failed", err)
	}
	defer reviewPool.Close()
	reviewMarket := marketpg.NewStore(reviewPool)
	reviewStore := contributionpg.NewStore(reviewPool)
	if err = reviewStore.SyncPublications(reviewStartup); err != nil {
		fatal(logger, "waiting for market publication recovery fence failed", err)
	}
	groupCredentialRoot := append([]byte{}, credentialRoot...)
	defer clear(groupCredentialRoot)
	groupRuntime.CredentialScope = func(r groupchannel.Record) (string, error) {
		secret, err := credentialKeyring.OpenCredential(r.ID, r.Credential)
		if err != nil {
			return "", err
		}
		return market.CredentialScope(groupCredentialRoot, r.GroupID, []byte(secret))
	}
	groupRuntime.Planner = registry
	marketRuntime := contribution.NewRuntime(reviewMarket, groupRuntime.Install)
	groupRuntime.Ready = func() error { _, err := marketRuntime.Current(); return err }
	if err = marketRuntime.Refresh(reviewStartup); err != nil {
		fatal(logger, "loading approved market failed", err)
	}
	contributions := &contribution.Service{Store: reviewStore, Live: reviewMarket, Planner: registry, Publisher: marketRuntime, Outbound: outboundPolicy}
	if err = contributions.Recover(reviewStartup); err != nil {
		fatal(logger, "recovering committed market publications failed", err)
	}
	cancelReviewStartup()
	detectorStore := detectionpg.NewStore(detectorPool)
	detectorSettings := settings.NewService(settingspg.NewStore(detectorPool))
	detector, err := detection.New(backgroundContext, detection.Config{Registry: registry, Store: detectorStore, Settings: detectorStore, Resolver: contribution.DraftResolver{Service: contributions, Fallback: detectionpg.DirectoryResolver{Market: detectorMarket}}, Outbound: outboundPolicy, PrepareEgress: func(ctx context.Context) (channel.Outbound, error) {
		value, err := detectorSettings.Get(ctx)
		if err != nil {
			return nil, err
		}
		return outboundPolicy.WithExtraBlockedHosts(value.ExtraBlockedHosts)
	}, Limits: detection.DefaultLimits(), CredentialRoot: credentialRoot})
	clear(credentialRoot)
	if err != nil {
		fatal(logger, "starting detection service failed", err)
	}
	defer detector.Close()
	groupService := &groupchannel.Service{Repository: groupStore, Runtime: groupRuntime, Keyring: credentialKeyring}
	groupProber := groupchannel.NewProber(backgroundContext, groupchannel.ProbeConfig{Runtime: groupRuntime, Repository: groupStore, Keyring: credentialKeyring, Logger: logger, Edited: engine.ChannelEdited, Outbound: func(ctx context.Context) (channel.Outbound, error) {
		value, err := settingsService.Get(ctx)
		if err != nil {
			return nil, err
		}
		return outboundPolicy.WithExtraBlockedHosts(value.ExtraBlockedHosts)
	}})
	defer groupProber.Close()
	groupProber.Notify()
	// Derived estimates have two private connections and one bounded worker;
	// inference/settlement never waits for them.
	typicalStartup, cancelTypicalStartup := context.WithTimeout(context.Background(), 15*time.Second)
	typicalPool, err := database.OpenLimited(typicalStartup, databaseURL, 2)
	if err != nil {
		fatal(logger, "opening typical cost pool failed", err)
	}
	defer typicalPool.Close()
	typicalRates := settings.NewService(settingspg.NewStore(typicalPool))
	typicalCosts, err := typical.New(typicalStartup, typicalpg.NewStore(typicalPool), typicalSource{runtime: groupRuntime, values: func(ctx context.Context) (int64, int64, error) {
		v, e := typicalRates.Get(ctx)
		return v.USDCNYRateNano, v.FeeRateNano, e
	}})
	cancelTypicalStartup()
	if err != nil {
		fatal(logger, "starting typical cost estimates failed", err)
	}
	engine.TypicalPrices = func() gateway.TypicalPriceLookup { return typicalCosts.Prices() }
	groupRuntime.Notify = func() { groupProber.Notify(); typicalCosts.Notify() }
	typicalContext, stopTypical := context.WithCancel(backgroundContext)
	typicalDone := make(chan struct{})
	go func() { defer close(typicalDone); typicalCosts.Run(typicalContext) }()
	defer func() { stopTypical(); <-typicalDone }()

	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(api.Dependencies{
			Typical: typicalCosts, GroupChannels: groupService, DetectionMCP: mcpFixture,
			Contributions: contributions,
			Detection:     detector,
			Identity:      identityService,
			Forum:         forumService,
			Catalog:       catalogService,
			Ledger:        ledger.NewService(store.Ledger),
			Settings:      settingsService,
			RateSync:      rateSyncer,
			Audit:         audit.NewService(store.Audit),
			Keys:          apikey.NewService(cached.Keys, credentialKeyring, keyModelIDs),
			Channels: channel.NewService(channel.Dependencies{
				Store: cached.Channels, Keyring: credentialKeyring, Outbound: outboundPolicy, KnownModels: modelIDs,
				BlockedHosts: func(ctx context.Context) ([]string, error) {
					value, err := settingsService.Get(ctx)
					return value.ExtraBlockedHosts, err
				},
			}),
			Routing:           cached.Routing,
			Gateway:           engine,
			Browse:            store.Gateway,
			C2C:               c2cService,
			Observe:           observeService,
			Feed:              feed,
			DatabaseReady:     pool.Ping,
			CookieSecure:      cookieSecure,
			TrustedProxyCIDRs: trustedProxyCIDRs,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	logger.Info("backend listening", "addr", server.Addr)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			fatal(logger, "server stopped unexpectedly", err)
		}
	case <-signals:
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
		_ = metricsServer.Shutdown(shutdownContext)
	}
}

// fatal logs a startup or server failure and exits with status 1. Like log.Fatal
// it skips deferred cleanups. A nil err logs the message alone.
func fatal(logger *slog.Logger, message string, err error) {
	if err != nil {
		logger.Error(message, "error", err)
	} else {
		logger.Error(message)
	}
	os.Exit(1)
}

// newMetricsServer builds the internal Prometheus listener. METRICS_ADDR
// defaults to :9090; Compose never publishes or proxies it.
func newMetricsServer(addr string, handler http.Handler) *http.Server {
	if strings.TrimSpace(addr) == "" {
		addr = ":9090"
	}
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
}

// refreshMetricModels keeps the model label of the metrics bounded to the
// catalog, so arbitrary requested model names never become label values.
func refreshMetricModels(ctx context.Context, target *metrics.Metrics, modelIDs func(context.Context) ([]string, error), logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		refreshContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		ids, err := modelIDs(refreshContext)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Error("metrics: reading the model catalog failed", "error", err)
		} else if err == nil {
			target.SetModels(ids)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runErrorScrub clears raw upstream error text older than 30 days, once a day.
func runErrorScrub(ctx context.Context, service *observe.Service, logger *slog.Logger) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		runContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
		cleared, err := service.ScrubRawErrors(runContext)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Error("raw error cleanup failed", "error", err)
		case cleared > 0:
			logger.Info("raw error cleanup", "calls", cleared)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func channelEventKind(kind string) gateway.EventKind {
	switch kind {
	case channel.EventCooldownStarted:
		return gateway.EventChannelCooldown
	case channel.EventCooldownEnded:
		return gateway.EventChannelRecover
	case channel.EventAnomalousClientErrors:
		return gateway.EventChannelAnomaly
	}
	return gateway.EventChannelLimit
}

// runExchangeRateSync checks once a minute whether the daily automatic
// USD/CNY sync is due and runs it; failures are kept on the settings row.
func runExchangeRateSync(ctx context.Context, syncer *settings.RateSyncer, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		runContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		ran, err := syncer.SyncIfDue(runContext)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Error("exchange rate sync failed", "error", err)
		} else if ran {
			logger.Info("exchange rate synced")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runC2CExpiry cancels C2C trades whose payment deadline has passed, once a
// minute, and returns their points to the order (or the seller).
func runC2CExpiry(ctx context.Context, service *c2c.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		expireOnce(ctx, service, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func expireOnce(ctx context.Context, service *c2c.Service, logger *slog.Logger) {
	runContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cancelled, err := service.ExpireDue(runContext, 100)
	if err != nil && ctx.Err() == nil {
		logger.Error("c2c payment expiry failed", "error", err)
	}
	if cancelled > 0 {
		logger.Info("c2c payment expiry cancelled trades", "trades", cancelled)
	}
}

func parseCommaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseTrustedProxyCIDRs(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid CIDR %q: %w", part, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func runForumCleanup(ctx context.Context, service *forum.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		cleanupContext, cancel := context.WithTimeout(ctx, time.Minute)
		if _, err := service.Cleanup(cleanupContext); err != nil && ctx.Err() == nil {
			logger.Error("forum attachment cleanup failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func geoSource(enabled bool) string {
	if enabled {
		return "ipwho.is"
	}
	return ""
}
