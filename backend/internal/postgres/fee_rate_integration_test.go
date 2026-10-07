package postgres_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

func TestFeeRateIntegrationVersionsAuditAndSnapshots(t *testing.T) {
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
	schema := "fee_rate_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop fee rate schema: %v", err)
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
	admin = changeGatewayPassword(t, ctx, identityService, admin, "Bootstrap-password-2026", "Fee-rate-admin-password-2026")
	consumer := inviteGatewayAccount(t, ctx, identityService, admin, "fee.consumer", "费率消费者", money.Amount(100*money.Scale))
	provider := inviteGatewayAccount(t, ctx, identityService, admin, "fee.provider", "费率共享者", money.Amount(10*money.Scale))
	feeRates := feerate.NewService(store)

	versions, err := feeRates.List(ctx, admin, 0)
	if err != nil || len(versions) != 1 || versions[0].Rate != money.FromNano(1_000_000) || versions[0].CreatedByID != "" || versions[0].Reason != "" {
		t.Fatalf("seeded fee rate = %+v, %v", versions, err)
	}
	seed := versions[0]
	if _, err := feeRates.List(ctx, consumer, 0); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member list = %v", err)
	}
	if _, err := feeRates.Set(ctx, consumer, seed.Version, money.FromNano(2_000_000), "member"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member set = %v", err)
	}
	if _, err := feeRates.Set(ctx, admin, seed.Version, money.FromNano(1_000_000), "same"); !errors.Is(err, feerate.ErrUnchanged) {
		t.Fatalf("unchanged set = %v", err)
	}
	if _, err := feeRates.Set(ctx, admin, seed.Version, feerate.MaxRate+1, "too high"); !errors.Is(err, feerate.ErrInvalidInput) {
		t.Fatalf("out of range set = %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_fee_rates (fee_rate_nano) VALUES (1000000001)`); err == nil {
		t.Fatal("database accepted a fee rate above 100%")
	}

	model, err := catalog.NewService(store).Create(ctx, admin, catalog.Model{
		ID: "test/fee-rate-model", Name: "Fee rate model", Provider: "Test", ContextWindow: 1000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: money.FromNano(10 * money.Scale), OutputPrice: money.FromNano(20 * money.Scale), Status: catalog.StatusActive,
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
	offer := createGatewayOffer(t, ctx, store, channelService, provider, "Fee relay", "https://fee-relay.example", "fee-upstream-secret", model.ID, "vendor-fee")
	gatewayService, err := gateway.NewService(store, channelService)
	if err != nil {
		t.Fatal(err)
	}
	created, err := gatewayService.CreateAPIKey(ctx, consumer, gateway.KeyConfigInput{
		DisplayName: "费率 Key",
		Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{offer.ID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := gatewayService.Authenticate(ctx, created.Secret)
	if err != nil {
		t.Fatal(err)
	}

	before := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
	if before.Call.FeeRateVersion != seed.Version || before.Call.FeeRateNano != 1_000_000 {
		t.Fatalf("pre-change snapshot = %d/%d", before.Call.FeeRateVersion, before.Call.FeeRateNano)
	}

	half := money.FromNano(money.Scale / 2)
	updated, err := feeRates.Set(ctx, admin, seed.Version, half, "  运营调整  ")
	if err != nil || updated.Version <= seed.Version || updated.Rate != half || updated.CreatedByID != admin.ID || updated.CreatedByUsername != admin.Username || updated.Reason != "运营调整" {
		t.Fatalf("set fee rate = %+v, %v", updated, err)
	}
	if _, err := feeRates.Set(ctx, admin, seed.Version, money.FromNano(3_000_000), "stale"); !errors.Is(err, feerate.ErrConflict) {
		t.Fatalf("stale set = %v", err)
	}

	var auditActor, auditReason string
	var auditDetails []byte
	if err := pool.QueryRow(ctx, `
		SELECT actor_account_id::text, reason, details::text FROM audit_events
		WHERE action = 'api_fee_rate.updated' AND target_type = 'api_fee_rate' AND target_id = $1`, strconv.FormatInt(updated.Version, 10)).Scan(&auditActor, &auditReason, &auditDetails); err != nil {
		t.Fatal(err)
	}
	var details struct {
		Before struct {
			Version     int64 `json:"version"`
			FeeRateNano int64 `json:"fee_rate_nano"`
		} `json:"before"`
		After struct {
			Version     int64 `json:"version"`
			FeeRateNano int64 `json:"fee_rate_nano"`
		} `json:"after"`
	}
	if err := json.Unmarshal(auditDetails, &details); err != nil {
		t.Fatal(err)
	}
	if auditActor != admin.ID || auditReason != "运营调整" || details.Before.Version != seed.Version || details.Before.FeeRateNano != 1_000_000 || details.After.Version != updated.Version || details.After.FeeRateNano != half.Nano() {
		t.Fatalf("audit = %s %s %s", auditActor, auditReason, auditDetails)
	}
	var seedNano int64
	if err := pool.QueryRow(ctx, `SELECT fee_rate_nano FROM api_fee_rates WHERE version = $1`, seed.Version).Scan(&seedNano); err != nil || seedNano != 1_000_000 {
		t.Fatalf("seed version changed: %d, %v", seedNano, err)
	}

	after := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
	if after.Call.FeeRateVersion != updated.Version || after.Call.FeeRateNano != half.Nano() {
		t.Fatalf("post-change snapshot = %d/%d", after.Call.FeeRateVersion, after.Call.FeeRateNano)
	}
	usage := &ledger.UsageV1{InputTokens: 1000, OutputTokens: 500}
	beforeSettled := settleFeeRateCall(t, ctx, gatewayService, before, offer.ID, usage)
	afterSettled := settleFeeRateCall(t, ctx, gatewayService, after, offer.ID, usage)
	if beforeSettled.ProviderCharge != afterSettled.ProviderCharge || beforeSettled.ProviderCharge <= 0 {
		t.Fatalf("provider charges differ: %d vs %d", beforeSettled.ProviderCharge, afterSettled.ProviderCharge)
	}
	if beforeSettled.PlatformFee != expectedPlatformFee(beforeSettled.ProviderCharge, 1_000_000) || afterSettled.PlatformFee != expectedPlatformFee(afterSettled.ProviderCharge, half.Nano()) {
		t.Fatalf("platform fees = %d (old snapshot) / %d (new snapshot) for charge %d", beforeSettled.PlatformFee, afterSettled.PlatformFee, beforeSettled.ProviderCharge)
	}
	var storedBeforeVersion int64
	if err := pool.QueryRow(ctx, `SELECT fee_rate_version FROM api_calls WHERE id = $1`, before.Call.ID).Scan(&storedBeforeVersion); err != nil || storedBeforeVersion != seed.Version {
		t.Fatalf("historical call fee version = %d, %v", storedBeforeVersion, err)
	}

	type setResult struct {
		version feerate.Version
		err     error
	}
	gate := make(chan struct{})
	results := make(chan setResult, 4)
	var group sync.WaitGroup
	for index := range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-gate
			version, setErr := feeRates.Set(ctx, admin, updated.Version, money.FromNano(int64(10_000_000+index)), "concurrent")
			results <- setResult{version, setErr}
		}()
	}
	close(gate)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, feerate.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent set: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 3 {
		t.Fatalf("concurrent set successes/conflicts = %d/%d", successes, conflicts)
	}

	history, err := feeRates.List(ctx, admin, 10)
	if err != nil || len(history) != 3 || history[1].Version != updated.Version || history[1].Reason != "运营调整" || history[1].CreatedByUsername != admin.Username || history[2].Version != seed.Version {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if limited, err := feeRates.List(ctx, admin, 1); err != nil || len(limited) != 1 || limited[0].Version != history[0].Version {
		t.Fatalf("limited history = %+v, %v", limited, err)
	}
}

func settleFeeRateCall(t *testing.T, ctx context.Context, service *gateway.Service, plan gateway.CallPlan, offerID string, usage *ledger.UsageV1) gateway.Call {
	t.Helper()
	attempt, err := service.StartAttempt(ctx, plan.Call.ID, plan.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	success := gateway.AttemptResult{LeaseGeneration: attempt.LeaseGeneration, Status: gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true, Usage: usage}
	settled, err := service.Finalize(ctx, plan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration,
		Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerID, HTTPStatus: http.StatusOK, Usage: usage,
		SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
	})
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

func expectedPlatformFee(providerCharge money.Amount, feeRateNano int64) money.Amount {
	product := new(big.Int).Mul(big.NewInt(providerCharge.Nano()), big.NewInt(feeRateNano))
	scale := big.NewInt(money.Scale)
	quotient, remainder := new(big.Int).QuoRem(product, scale, new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return money.FromNano(quotient.Int64())
}
