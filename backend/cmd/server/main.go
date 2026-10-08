package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
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
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/metrics"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	cookieSecure := true
	if value := os.Getenv("COOKIE_SECURE"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			log.Fatal("COOKIE_SECURE must be true or false")
		}
		cookieSecure = parsed
	}
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		log.Fatal(err)
	}
	credentialKeyring, err := channel.ParseKeyring(
		os.Getenv("UPSTREAM_CREDENTIAL_KEYRING"),
		os.Getenv("UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID"),
	)
	if err != nil {
		log.Fatal(err)
	}
	outboundPolicy, err := channel.NewOutboundPolicy(
		parseCommaSeparated(os.Getenv("UPSTREAM_ALLOWED_PORTS")),
		parseCommaSeparated(os.Getenv("UPSTREAM_BLOCKED_HOSTS")),
	)
	if err != nil {
		log.Fatal(err)
	}

	c2cKeyring, err := c2c.ParseKeyring(
		os.Getenv("C2C_PRIVATE_DATA_KEYRING"),
		os.Getenv("C2C_PRIVATE_DATA_ACTIVE_KEY_ID"),
	)
	if err != nil {
		log.Fatal(err)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	pool, err := database.Open(startupContext, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	store := postgres.New(pool)
	identityService, err := identity.NewService(store.Identity, 24*time.Hour)
	if err != nil {
		log.Fatal(err)
	}

	catalogService := catalog.NewService(store.Catalog)
	settingsService := settings.NewService(store.Settings)
	modelIDs := func(ctx context.Context) ([]string, error) {
		models, err := catalogService.List(ctx, true)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids, nil
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	bus := gateway.NewBus()
	runtime := gateway.NewRuntime(func(channelID, kind, reason string) {
		bus.Publish(gateway.Event{Kind: channelEventKind(kind), At: time.Now(), ChannelID: channelID, Detail: reason})
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.Gateway.RecordChannelEvent(ctx, channelID, kind, reason); err != nil {
				logger.Error("recording channel event failed", "channel_id", channelID, "kind", kind, "error", err)
			}
		}()
	})
	engine := gateway.NewEngine(gateway.Dependencies{
		Store: store.Gateway, Catalog: catalogService, Settings: settingsService, Routing: store.Routes,
		Keyring: credentialKeyring, Outbound: outboundPolicy, Events: bus, Logger: logger, Runtime: runtime,
	})
	reaperContext, stopReaper := context.WithCancel(context.Background())
	defer stopReaper()
	go engine.RunReaper(reaperContext, time.Minute)

	c2cService := c2c.NewService(store.C2C, c2cKeyring)
	backgroundContext, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	go runC2CExpiry(backgroundContext, c2cService)

	observeService := observe.NewService(store.Observe)
	feed := observe.NewFeed(observeService, bus, logger)
	platformMetrics := metrics.New(func(channelID string, now time.Time) bool { return !runtime.CooldownUntil(channelID, now).IsZero() })
	feed.AddObserver(platformMetrics)
	go feed.Run(backgroundContext)
	go platformMetrics.Run(backgroundContext, observeService, time.Minute, logger)
	go refreshMetricModels(backgroundContext, platformMetrics, modelIDs, logger)
	go runErrorScrub(backgroundContext, observeService, logger)
	metricsServer := newMetricsServer(os.Getenv("METRICS_ADDR"), platformMetrics.Handler())
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics listener stopped: %v", err)
		}
	}()

	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(api.Dependencies{
			Identity: identityService,
			Catalog:  catalogService,
			Ledger:   ledger.NewService(store.Ledger),
			Settings: settingsService,
			Audit:    audit.NewService(store.Audit),
			Keys:     apikey.NewService(store.Keys, credentialKeyring, modelIDs),
			Channels: channel.NewService(channel.Dependencies{
				Store: store.Channels, Keyring: credentialKeyring, Outbound: outboundPolicy, KnownModels: modelIDs,
				BlockedHosts: func(ctx context.Context) ([]string, error) {
					value, err := settingsService.Get(ctx)
					return value.ExtraBlockedHosts, err
				},
			}),
			Routing:           store.Routes,
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

	log.Printf("backend listening on %s", server.Addr)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-signals:
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
		_ = metricsServer.Shutdown(shutdownContext)
	}
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
	}
	return gateway.EventChannelLimit
}

// runC2CExpiry cancels C2C trades whose payment deadline has passed, once a
// minute, and returns their points to the order (or the seller).
func runC2CExpiry(ctx context.Context, service *c2c.Service) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		expireOnce(ctx, service)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func expireOnce(ctx context.Context, service *c2c.Service) {
	runContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cancelled, err := service.ExpireDue(runContext, 100)
	if err != nil && ctx.Err() == nil {
		log.Printf("c2c payment expiry failed: %v", err)
	}
	if cancelled > 0 {
		log.Printf("c2c payment expiry cancelled %d trades", cancelled)
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
