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
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
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

	// C2C 私密数据密钥环在 Feature C 使用；启动时先校验配置，避免带着错误配置上线。
	if _, err := c2c.ParseKeyring(
		os.Getenv("C2C_PRIVATE_DATA_KEYRING"),
		os.Getenv("C2C_PRIVATE_DATA_ACTIVE_KEY_ID"),
	); err != nil {
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
	// 上游凭据密钥环与出站策略由 Feature B 的渠道与网关使用。
	_, _ = credentialKeyring, outboundPolicy

	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(api.Dependencies{
			Identity:          identityService,
			Catalog:           catalog.NewService(store.Catalog),
			Ledger:            ledger.NewService(store.Ledger),
			Settings:          settings.NewService(store.Settings),
			Audit:             audit.NewService(store.Audit),
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
