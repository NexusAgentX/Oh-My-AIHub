package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
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

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	pool, err := database.Open(startupContext, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	store := postgres.New(pool)
	identityService, err := identity.NewService(store, 24*time.Hour)
	if err != nil {
		log.Fatal(err)
	}
	channelService, err := channel.NewService(store, credentialKeyring, outboundPolicy)
	if err != nil {
		log.Fatal(err)
	}
	if err := channelService.ValidateCredentialInventory(startupContext); err != nil {
		log.Fatal(err)
	}
	if _, err := channelService.RecoverAbandonedValidations(startupContext); err != nil {
		log.Fatal(err)
	}
	gatewayService, err := gateway.NewService(store, channelService)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := gatewayService.RecoverOrphans(startupContext, time.Now().Add(-2*time.Minute), 100); err != nil {
		log.Fatal(err)
	}
	maintenanceContext, cancelMaintenance := context.WithCancel(context.Background())
	defer cancelMaintenance()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-maintenanceContext.Done():
				return
			case <-ticker.C:
				result := runRecoveryCycle(
					maintenanceContext, 5*time.Second,
					channelService.RecoverAbandonedValidations,
					func(ctx context.Context) (int64, error) {
						recovered, err := gatewayService.RecoverOrphans(ctx, time.Now().Add(-2*time.Minute), 100)
						return int64(recovered), err
					},
				)
				if result.channelErr != nil {
					log.Printf("recover abandoned channel validations: %v", result.channelErr)
				} else if result.channels > 0 {
					log.Printf("recovered %d abandoned channel validation attempts", result.channels)
				}
				if result.gatewayErr != nil {
					log.Printf("recover orphan gateway calls: %v", result.gatewayErr)
				} else if result.gateways > 0 {
					log.Printf("recovered %d orphan gateway calls", result.gateways)
				}
			}
		}
	}()

	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(api.Dependencies{
			Identity:          identityService,
			Catalog:           catalog.NewService(store),
			Channels:          channelService,
			Gateway:           gatewayService,
			Ledger:            ledger.NewService(store),
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
	}
}

type recoveryCycleResult struct {
	channels   int64
	channelErr error
	gateways   int64
	gatewayErr error
}

func runRecoveryCycle(parent context.Context, timeout time.Duration, recoverChannels, recoverGateways func(context.Context) (int64, error)) recoveryCycleResult {
	channelContext, cancelChannels := context.WithTimeout(parent, timeout)
	channels, channelErr := recoverChannels(channelContext)
	cancelChannels()

	gatewayContext, cancelGateways := context.WithTimeout(parent, timeout)
	gateways, gatewayErr := recoverGateways(gatewayContext)
	cancelGateways()
	return recoveryCycleResult{channels: channels, channelErr: channelErr, gateways: gateways, gatewayErr: gatewayErr}
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
