package postgres_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// TestGatewayIntegrationTieredPricingSnapshotAndSettlement proves the three
// load-bearing tier behaviors end to end: BeginCall snapshots the conditional
// tiers, a mid-flight catalog price change never rewrites an authorized call,
// and settlement picks the first matching tier by prompt-side tokens and
// records which tier won.
func TestGatewayIntegrationTieredPricingSnapshotAndSettlement(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(basePool.Close)
	schema := "tiers_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop tiers schema: %v", err)
		}
	})
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := storepg.New(pool)
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin := createExactlyOneBootstrapAdmin(t, ctx, store)
	admin = changeGatewayPassword(t, ctx, identityService, admin, "Bootstrap-password-2026", "Tiers-admin-password-2026")
	consumer := inviteGatewayAccount(t, ctx, identityService, admin, "tiers.consumer", "档位消费者", money.Amount(100*money.Scale))
	provider := inviteGatewayAccount(t, ctx, identityService, admin, "tiers.provider", "档位共享者", money.Amount(10*money.Scale))

	catalogService := catalog.NewService(store)
	longContextMinimum := int64(100)
	windowStart, windowEnd := int16(0), int16(1440)
	model, err := catalogService.Create(ctx, admin, catalog.Model{
		ID: "test/tiered-model", Name: "Tiered model", Provider: "Test", ContextWindow: 1000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: money.FromNano(1 * money.Scale), OutputPrice: money.FromNano(2 * money.Scale),
		CacheWritePrice: money.FromNano(money.Scale / 2), CacheReadPrice: money.FromNano(money.Scale / 4),
		PriceTiers: []ledger.PriceTier{
			{
				Name: "long-context", MinPromptTokens: &longContextMinimum, Timezone: "UTC",
				InputPrice: money.FromNano(3 * money.Scale), OutputPrice: money.FromNano(6 * money.Scale),
				CacheWritePrice: money.FromNano(3 * money.Scale / 2), CacheReadPrice: money.FromNano(3 * money.Scale / 4),
			},
			{
				Name: "always-window", Timezone: "UTC", Weekdays: []int{1, 2, 3, 4, 5, 6, 7},
				StartMinute: &windowStart, EndMinute: &windowEnd,
				InputPrice: money.FromNano(5 * money.Scale), OutputPrice: money.FromNano(10 * money.Scale),
				CacheWritePrice: money.FromNano(money.Scale), CacheReadPrice: money.FromNano(money.Scale),
			},
		},
		Status: catalog.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := channel.NewOutboundPolicyWithResolver(nil, nil, integrationPublicResolver{})
	if err != nil {
		t.Fatal(err)
	}
	channelService, err := channel.NewService(store, keyring, policy)
	if err != nil {
		t.Fatal(err)
	}
	offer := createGatewayOffer(t, ctx, store, channelService, provider, "Tier relay", "https://tiers.example", "upstream-secret-tiers", model.ID, "vendor-tiers")
	gatewayService, err := gateway.NewService(store, channelService)
	if err != nil {
		t.Fatal(err)
	}
	created, err := gatewayService.CreateAPIKey(ctx, consumer, gateway.KeyConfigInput{
		DisplayName: "档位 Key",
		Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{offer.ID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := gatewayService.Authenticate(ctx, created.Secret)
	if err != nil {
		t.Fatal(err)
	}

	plan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
	if len(plan.Candidates) != 1 || len(plan.Candidates[0].Lease.PriceTiers) != 2 {
		t.Fatalf("call plan did not carry the model tiers: %+v", plan.Candidates)
	}
	var formulaVersion string
	var snapshotTiers int
	if err := pool.QueryRow(ctx, `SELECT formula_version, (SELECT count(*) FROM api_call_price_tiers WHERE call_id = api_calls.id) FROM api_calls WHERE id = $1`, plan.Call.ID).Scan(&formulaVersion, &snapshotTiers); err != nil {
		t.Fatal(err)
	}
	if formulaVersion != "formula-v2" || snapshotTiers != 2 {
		t.Fatalf("call snapshot formula %q with %d tiers", formulaVersion, snapshotTiers)
	}

	// Replace the catalog tiers after authorization: the in-flight call must
	// settle with its own snapshot, not the new prices.
	renamedMinimum := int64(100)
	if _, err := catalogService.Update(ctx, admin, model.ID, model.Version, catalog.Model{
		ID: model.ID, Name: model.Name, Provider: model.Provider, ContextWindow: model.ContextWindow,
		InputModalities: model.InputModalities, OutputModalities: model.OutputModalities,
		InputPrice: model.InputPrice, OutputPrice: model.OutputPrice,
		CacheWritePrice: model.CacheWritePrice, CacheReadPrice: model.CacheReadPrice,
		PriceTiers: []ledger.PriceTier{{
			Name: "rewritten", MinPromptTokens: &renamedMinimum, Timezone: "UTC",
			InputPrice: money.FromNano(999 * money.Scale), OutputPrice: money.FromNano(999 * money.Scale),
			CacheWritePrice: money.FromNano(999 * money.Scale), CacheReadPrice: money.FromNano(999 * money.Scale),
		}},
		Status: catalog.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}

	attempt, err := gatewayService.StartAttempt(ctx, plan.Call.ID, plan.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	usage := &ledger.UsageV1{InputTokens: 100, OutputTokens: 50, CacheWriteTokens: 10, CacheReadTokens: 20}
	successResult := gateway.AttemptResult{
		LeaseGeneration: attempt.LeaseGeneration, Status: gateway.AttemptSucceeded,
		HTTPStatus: http.StatusOK, SemanticCommitted: true, Usage: usage, Duration: time.Millisecond,
	}
	pending, err := gatewayService.Finalize(ctx, plan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration, Status: gateway.CallSucceeded,
		CompletionReason: "completed", FinalOfferID: offer.ID, HTTPStatus: http.StatusOK, Usage: usage,
		SuccessAttemptID: attempt.ID, SuccessAttempt: &successResult,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Prompt side is 130 tokens, so the first snapshot tier wins:
	// (100*3 + 50*6 + 10*1.5 + 20*0.75) / 1e6 = 0.00063 credits.
	if pending.SettledPriceTierSeq != 1 {
		t.Fatalf("settled tier seq = %d, want 1", pending.SettledPriceTierSeq)
	}
	if pending.ProviderCharge != money.FromNano(630_000) {
		t.Fatalf("provider charge = %s, want 0.00063", pending.ProviderCharge.String())
	}
	if pending.PlatformFee != money.FromNano(630) {
		t.Fatalf("platform fee = %s, want 0.00000063", pending.PlatformFee.String())
	}
	if _, err := gatewayService.ConfirmDelivery(ctx, plan.Call.ID, plan.Call.LeaseGeneration); err != nil {
		t.Fatal(err)
	}

	// A fresh call snapshots the rewritten catalog: a short prompt now misses
	// the only conditional tier and settles at the default prices.
	shortPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
	shortAttempt, err := gatewayService.StartAttempt(ctx, shortPlan.Call.ID, shortPlan.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	shortUsage := &ledger.UsageV1{InputTokens: 10, OutputTokens: 5}
	shortResult := gateway.AttemptResult{
		LeaseGeneration: shortAttempt.LeaseGeneration, Status: gateway.AttemptSucceeded,
		HTTPStatus: http.StatusOK, SemanticCommitted: true, Usage: shortUsage, Duration: time.Millisecond,
	}
	shortPending, err := gatewayService.Finalize(ctx, shortPlan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: shortPlan.Call.LeaseGeneration, Status: gateway.CallSucceeded,
		CompletionReason: "completed", FinalOfferID: offer.ID, HTTPStatus: http.StatusOK, Usage: shortUsage,
		SuccessAttemptID: shortAttempt.ID, SuccessAttempt: &shortResult,
	})
	if err != nil {
		t.Fatal(err)
	}
	if shortPending.SettledPriceTierSeq != 0 {
		t.Fatalf("short call settled tier seq = %d, want default", shortPending.SettledPriceTierSeq)
	}
	// (10*1 + 5*2) / 1e6 = 0.00002 credits.
	if shortPending.ProviderCharge != money.FromNano(20_000) {
		t.Fatalf("short call provider charge = %s, want 0.00002", shortPending.ProviderCharge.String())
	}
}
