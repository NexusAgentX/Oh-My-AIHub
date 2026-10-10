package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// isolatedDatabase migrates the baseline into a fresh schema of
// TEST_DATABASE_URL and returns a pool bound to it. Tests skip without a
// database, unless AIHUB_REQUIRE_TEST_DATABASE is set (CI), where a missing
// database fails the test instead of silently skipping it.
func isolatedDatabase(t *testing.T) (*pgxpool.Pool, *storepg.Store) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("AIHUB_REQUIRE_TEST_DATABASE") != "" {
			t.Fatal("TEST_DATABASE_URL is not set but AIHUB_REQUIRE_TEST_DATABASE demands a database")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(basePool.Close)
	schema := "test_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, storepg.New(pool)
}

func withSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(value)
}

func mustAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return amount
}

// accounts creates a bootstrap administrator and ready members with the given credit limits.
func accounts(t *testing.T, store *storepg.Store, credits ...string) (*identity.Service, identity.Account, []identity.AdminAccount) {
	t.Helper()
	ctx := context.Background()
	service, err := identity.NewService(store.Identity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := service.CreateBootstrapAdmin(ctx, "founder", "创始人", "Founder-password-2026")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	members := make([]identity.AdminAccount, 0, len(credits))
	for index, credit := range credits {
		limit := mustAmount(t, credit)
		created, err := service.CreateInvitedAccount(ctx, admin, fmt.Sprintf("member%d", index), fmt.Sprintf("成员%d", index), &limit, false)
		if err != nil {
			t.Fatalf("create member %d: %v", index, err)
		}
		members = append(members, created.Account)
	}
	return service, admin, members
}
