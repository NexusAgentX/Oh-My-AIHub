package postgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGatewayIntegrationStateMachineAndRecovery(t *testing.T) {
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
	schema := "gateway_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop gateway schema: %v", err)
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
	admin = changeGatewayPassword(t, ctx, identityService, admin, "Bootstrap-password-2026", "Gateway-admin-password-2026")
	consumer := inviteGatewayAccount(t, ctx, identityService, admin, "gateway.consumer", "网关消费者", money.Amount(100*money.Scale))
	providerOne := inviteGatewayAccount(t, ctx, identityService, admin, "gateway.provider.one", "共享者甲", money.Amount(10*money.Scale))
	providerTwo := inviteGatewayAccount(t, ctx, identityService, admin, "gateway.provider.two", "共享者乙", money.Amount(10*money.Scale))
	outsider := inviteGatewayAccount(t, ctx, identityService, admin, "gateway.outsider", "无关用户", money.Amount(10*money.Scale))

	model, err := catalog.NewService(store).Create(ctx, admin, catalog.Model{
		ID: "test/gateway-model", Name: "Gateway model", Provider: "Test", ContextWindow: 1000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"}, SupportsTools: true,
		InputPrice: money.FromNano(10 * money.Scale), OutputPrice: money.FromNano(20 * money.Scale),
		CacheWritePrice: money.FromNano(5 * money.Scale), CacheReadPrice: money.FromNano(2 * money.Scale), Status: catalog.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
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
	offerOne := createGatewayOffer(t, ctx, store, channelService, providerOne, "Relay one", "https://gateway-one.example", "upstream-secret-one", model.ID, "vendor-one")
	offerTwo := createGatewayOffer(t, ctx, store, channelService, providerTwo, "Relay two", "https://gateway-two.example", "upstream-secret-two", model.ID, "vendor-two")
	gatewayService, err := gateway.NewService(store, channelService)
	if err != nil {
		t.Fatal(err)
	}
	created, err := gatewayService.CreateAPIKey(ctx, consumer, gateway.KeyConfigInput{
		DisplayName: "生产聚合 Key",
		Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{offerOne.ID, offerTwo.ID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, "oma_live_") || strings.Contains(fmt.Sprintf("%+v", created.APIKey), created.Secret) {
		t.Fatalf("platform key was not one-time: %+v", created)
	}
	var storedHash []byte
	var auditContainsSecret bool
	if err := pool.QueryRow(ctx, `SELECT key_hash FROM api_keys WHERE id = $1`, created.APIKey.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if len(storedHash) != 32 || bytes.Equal(storedHash, []byte(created.Secret)) {
		t.Fatalf("stored key material is not a digest: %x", storedHash)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_events WHERE details::text LIKE '%' || $1 || '%')`, created.Secret).Scan(&auditContainsSecret); err != nil {
		t.Fatal(err)
	}
	if auditContainsSecret {
		t.Fatal("platform key plaintext appeared in audit metadata")
	}
	authenticated, err := gatewayService.Authenticate(ctx, created.Secret)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := gatewayService.BeginCall(ctx, authenticated, channel.ProtocolOpenAIChat, model.ID)
	if err != nil || len(plan.Candidates) != 2 || plan.Call.HoldID == "" || plan.Call.Preauthorized <= 0 {
		t.Fatalf("begin call = %+v, %v", plan, err)
	}
	if plan.Candidates[0].Lease.Credential != "upstream-secret-one" || plan.Candidates[1].Lease.Credential != "upstream-secret-two" {
		t.Fatal("routing leases did not carry the intended in-memory credentials")
	}
	var persistedCredentials bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM api_call_candidates
		WHERE call_id = $1 AND row_to_json(api_call_candidates)::text LIKE '%upstream-secret%'
	)`, plan.Call.ID).Scan(&persistedCredentials); err != nil {
		t.Fatal(err)
	}
	if persistedCredentials {
		t.Fatal("upstream credential appeared in the call snapshot")
	}

	type startResult struct {
		attempt gateway.Attempt
		err     error
	}
	startGate := make(chan struct{})
	starts := make(chan startResult, 2)
	var startWG sync.WaitGroup
	for range 2 {
		startWG.Add(1)
		go func() {
			defer startWG.Done()
			<-startGate
			attempt, startErr := gatewayService.StartAttempt(ctx, plan.Call.ID, plan.Candidates[0])
			starts <- startResult{attempt, startErr}
		}()
	}
	close(startGate)
	startWG.Wait()
	close(starts)
	var firstAttempt gateway.Attempt
	successes, conflicts := 0, 0
	for result := range starts {
		if result.err == nil {
			successes++
			firstAttempt = result.attempt
		} else if errors.Is(result.err, gateway.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent StartAttempt successes/conflicts = %d/%d", successes, conflicts)
	}
	if _, err := gatewayService.StartAttempt(ctx, plan.Call.ID, plan.Candidates[1]); !errors.Is(err, gateway.ErrConflict) {
		t.Fatalf("second candidate started before first terminal: %v", err)
	}
	if _, err := gatewayService.CompleteAttempt(ctx, firstAttempt.ID, gateway.AttemptResult{
		LeaseGeneration: firstAttempt.LeaseGeneration,
		Status:          gateway.AttemptFailed, HTTPStatus: http.StatusBadGateway, ErrorCode: "first_failed", RawError: "first provider failed", Duration: 10 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	secondAttempt, err := gatewayService.StartAttempt(ctx, plan.Call.ID, plan.Candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	usage := &ledger.UsageV1{InputTokens: 100, OutputTokens: 50, CacheWriteTokens: 10, CacheReadTokens: 20}
	successResult := gateway.AttemptResult{
		LeaseGeneration: secondAttempt.LeaseGeneration,
		Status:          gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true, MeasureTPS: true, TTFTObserved: true, TTFT: 5 * time.Millisecond, Duration: 25 * time.Millisecond, Usage: usage,
	}
	pending, err := gatewayService.Finalize(ctx, plan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration,
		Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerTwo.ID, HTTPStatus: http.StatusOK, Usage: usage,
		SuccessAttemptID: secondAttempt.ID, SuccessAttempt: &successResult,
	})
	if err != nil || pending.Status != gateway.CallPendingDelivery || pending.FinalOfferID != offerTwo.ID || pending.ProviderCharge <= 0 || pending.PlatformFee <= 0 || pending.CompletedAt != nil {
		t.Fatalf("pending finalization = %+v, %v", pending, err)
	}
	if _, err := gatewayService.Finalize(ctx, plan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration,
		Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerTwo.ID, HTTPStatus: http.StatusOK, Usage: usage,
	}); err != nil {
		t.Fatalf("same finalizer replay: %v", err)
	}
	if _, err := gatewayService.Finalize(ctx, plan.Call.ID, gateway.FinalizeOutcome{
		LeaseGeneration: plan.Call.LeaseGeneration,
		Status:          gateway.CallSucceeded, CompletionReason: "different", FinalOfferID: offerTwo.ID, HTTPStatus: http.StatusOK, Usage: usage,
	}); !errors.Is(err, gateway.ErrConflict) {
		t.Fatalf("different finalizer replay = %v, want conflict", err)
	}
	assertSingleGatewaySettlement(t, ctx, pool, plan.Call.ID)
	pendingMarket, _, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "success_rate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range pendingMarket {
		if item.OfferID == offerTwo.ID && item.CallCount != nil {
			t.Fatalf("pending delivery entered public metrics: %+v", item)
		}
	}
	pendingProvider, err := gatewayService.GetCall(ctx, providerTwo, plan.Call.ID)
	if err != nil || pendingProvider.Status != gateway.CallPendingDelivery || pendingProvider.FinalOfferID != "" || pendingProvider.ProviderCharge != 0 || pendingProvider.Usage != nil || pendingProvider.FinalHTTPStatus != 0 {
		t.Fatalf("pending provider projection = %+v, %v", pendingProvider, err)
	}
	completed, err := gatewayService.ConfirmDelivery(ctx, plan.Call.ID, plan.Call.LeaseGeneration)
	if err != nil || completed.Status != gateway.CallSucceeded || completed.CompletedAt == nil {
		t.Fatalf("delivery confirmation = %+v, %v", completed, err)
	}
	if replay, err := gatewayService.ConfirmDelivery(ctx, plan.Call.ID, plan.Call.LeaseGeneration); err != nil || replay.Status != gateway.CallSucceeded {
		t.Fatalf("delivery confirmation replay = %+v, %v", replay, err)
	}
	marketBySuccess, cursor, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "success_rate", Limit: 1})
	if err != nil || len(marketBySuccess) != 1 || marketBySuccess[0].OfferID != offerTwo.ID || marketBySuccess[0].CallSuccessRate == nil || *marketBySuccess[0].CallSuccessRate != "1.0000" || marketBySuccess[0].CallCount == nil || *marketBySuccess[0].CallCount != 1 || cursor == "" {
		t.Fatalf("success-rate market metrics = %#v, cursor=%q, err=%v", marketBySuccess, cursor, err)
	}
	secondMarketPage, _, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "success_rate", Limit: 1, Cursor: cursor})
	if err != nil || len(secondMarketPage) != 1 || secondMarketPage[0].OfferID != offerOne.ID || secondMarketPage[0].CallSuccessRate == nil || *secondMarketPage[0].CallSuccessRate != "0.0000" {
		t.Fatalf("success-rate cursor page = %#v, err=%v", secondMarketPage, err)
	}
	marketByTTFT, _, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "ttft"})
	if err != nil || len(marketByTTFT) != 2 || marketByTTFT[0].OfferID != offerTwo.ID || marketByTTFT[0].TTFTMilliseconds == nil || *marketByTTFT[0].TTFTMilliseconds != 5 {
		t.Fatalf("TTFT market metrics = %#v, err=%v", marketByTTFT, err)
	}
	marketByTPS, _, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "tps"})
	if err != nil || len(marketByTPS) != 2 || marketByTPS[0].OfferID != offerTwo.ID || marketByTPS[0].TokensPerSecond == nil || marketByTPS[0].CallCount == nil {
		t.Fatalf("TPS market metrics = %#v, err=%v", marketByTPS, err)
	}
	providerChannel, err := channelService.GetMine(ctx, providerTwo, offerTwo.ChannelID)
	if err != nil || len(providerChannel.Offers) != 1 || providerChannel.Offers[0].ProviderIncome == nil || *providerChannel.Offers[0].ProviderIncome != completed.ProviderCharge {
		t.Fatalf("provider income metrics = %+v, %v", providerChannel, err)
	}

	providerOneView, err := gatewayService.GetCall(ctx, providerOne, plan.Call.ID)
	if err != nil || len(providerOneView.Attempts) != 1 || providerOneView.ConsumerAccountID != "" || providerOneView.APIKeyID != "" || providerOneView.FinalOfferID != "" || providerOneView.ProviderCharge != 0 {
		t.Fatalf("failed provider projection = %+v, %v", providerOneView, err)
	}
	providerTwoView, err := gatewayService.GetCall(ctx, providerTwo, plan.Call.ID)
	if err != nil || len(providerTwoView.Attempts) != 1 || providerTwoView.ConsumerAccountID != "" || providerTwoView.APIKeyID != "" || providerTwoView.FinalOfferID != offerTwo.ID || providerTwoView.ProviderCharge <= 0 {
		t.Fatalf("final provider projection = %+v, %v", providerTwoView, err)
	}
	if _, err := gatewayService.GetCall(ctx, outsider, plan.Call.ID); !errors.Is(err, gateway.ErrNotFound) {
		t.Fatalf("unrelated account call read = %v, want not found", err)
	}

	t.Run("raw error normalization and terminal guard", func(t *testing.T) {
		failurePlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		first := startAndFailGatewayCandidate(t, ctx, gatewayService, failurePlan.Call.ID, failurePlan.Candidates[0], "first_error", "short")
		if _, err := gatewayService.CompleteAttempt(ctx, first.ID, gateway.AttemptResult{LeaseGeneration: first.LeaseGeneration, Status: gateway.AttemptFailed}); !errors.Is(err, gateway.ErrConflict) {
			t.Fatalf("terminal attempt completion = %v, want conflict", err)
		}
		second, err := gatewayService.StartAttempt(ctx, failurePlan.Call.ID, failurePlan.Candidates[1])
		if err != nil {
			t.Fatal(err)
		}
		raw := strings.Repeat("错误", 1600) + "\x00" + string([]byte{0xff})
		stored, err := gatewayService.CompleteAttempt(ctx, second.ID, gateway.AttemptResult{
			LeaseGeneration: second.LeaseGeneration,
			Status:          gateway.AttemptFailed, HTTPStatus: http.StatusBadGateway, ErrorCode: "second_error", RawError: raw,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.RawError) > gateway.MaxStoredRawErrorBytes || strings.ContainsRune(stored.RawError, '\x00') || !utf8.ValidString(stored.RawError) || !stored.RawErrorTruncated {
			t.Fatalf("normalized raw error = bytes:%d utf8:%t nul:%t truncated:%t", len(stored.RawError), utf8.ValidString(stored.RawError), strings.ContainsRune(stored.RawError, '\x00'), stored.RawErrorTruncated)
		}
		failed, err := gatewayService.Finalize(ctx, failurePlan.Call.ID, gateway.FinalizeOutcome{LeaseGeneration: failurePlan.Call.LeaseGeneration, Status: gateway.CallFailed, CompletionReason: "second_error", HTTPStatus: http.StatusBadGateway})
		if err != nil || failed.Status != gateway.CallFailed || failed.ProviderCharge != 0 || failed.PlatformFee != 0 {
			t.Fatalf("failed finalization = %+v, %v", failed, err)
		}
		assertSingleGatewaySettlement(t, ctx, pool, failurePlan.Call.ID)
	})

	t.Run("capture and release are atomic", func(t *testing.T) {
		rollbackPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, rollbackPlan.Call.ID, rollbackPlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		success := gateway.AttemptResult{LeaseGeneration: attempt.LeaseGeneration, Status: gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true, Usage: usage}
		if _, err := pool.Exec(ctx, `
			CREATE FUNCTION reject_gateway_release() RETURNS trigger AS $$ BEGIN
				IF NEW.kind = 'release' THEN RAISE EXCEPTION 'injected release failure'; END IF;
				RETURN NEW;
			END $$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_gateway_release BEFORE INSERT ON ledger_hold_events
			FOR EACH ROW EXECUTE FUNCTION reject_gateway_release()`); err != nil {
			t.Fatal(err)
		}
		_, finalizeErr := gatewayService.Finalize(ctx, rollbackPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: rollbackPlan.Call.LeaseGeneration,
			Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerOne.ID, HTTPStatus: http.StatusOK, Usage: usage,
			SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
		})
		if finalizeErr == nil {
			t.Fatal("injected release failure did not abort finalizer")
		}
		if _, err := pool.Exec(ctx, `DROP TRIGGER reject_gateway_release ON ledger_hold_events; DROP FUNCTION reject_gateway_release()`); err != nil {
			t.Fatal(err)
		}
		var callStatus, attemptStatus, holdStatus string
		var settlementCount, captureEventCount int
		var amount, remaining, captured, released int64
		if err := pool.QueryRow(ctx, `SELECT status FROM api_calls WHERE id = $1`, rollbackPlan.Call.ID).Scan(&callStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM api_call_attempts WHERE id = $1`, attempt.ID).Scan(&attemptStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT status, amount_nano, remaining_nano, captured_nano, released_nano FROM ledger_holds WHERE id = $1`, rollbackPlan.Call.HoldID).Scan(&holdStatus, &amount, &remaining, &captured, &released); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_call_settlements WHERE call_id = $1`, rollbackPlan.Call.ID).Scan(&settlementCount); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_hold_events WHERE hold_id = $1 AND kind = 'capture'`, rollbackPlan.Call.HoldID).Scan(&captureEventCount); err != nil {
			t.Fatal(err)
		}
		if callStatus != string(gateway.CallInProgress) || attemptStatus != string(gateway.AttemptInProgress) || holdStatus != "active" || remaining != amount || captured != 0 || released != 0 || settlementCount != 0 || captureEventCount != 0 {
			t.Fatalf("partial finalizer escaped rollback: call=%s attempt=%s hold=%s amount=%d remaining=%d captured=%d released=%d settlements=%d capture_events=%d", callStatus, attemptStatus, holdStatus, amount, remaining, captured, released, settlementCount, captureEventCount)
		}
		if _, err := gatewayService.Finalize(ctx, rollbackPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: rollbackPlan.Call.LeaseGeneration,
			Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerOne.ID, HTTPStatus: http.StatusOK, Usage: usage,
			SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := gatewayService.ConfirmDelivery(ctx, rollbackPlan.Call.ID, rollbackPlan.Call.LeaseGeneration); err != nil {
			t.Fatal(err)
		}
		assertSingleGatewaySettlement(t, ctx, pool, rollbackPlan.Call.ID)
	})

	t.Run("persistent success settlement failure degrades to zero-charge incomplete", func(t *testing.T) {
		failedPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, failedPlan.Call.ID, failedPlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		success := gateway.AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration,
			Status:          gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true,
			TTFTObserved: true, TTFT: 5 * time.Millisecond, Duration: 25 * time.Millisecond, Usage: usage,
		}
		if _, err := pool.Exec(ctx, `
			CREATE FUNCTION reject_gateway_capture() RETURNS trigger AS $$ BEGIN
				IF NEW.kind = 'capture' THEN RAISE EXCEPTION 'injected capture failure'; END IF;
				RETURN NEW;
			END $$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_gateway_capture BEFORE INSERT ON ledger_hold_events
			FOR EACH ROW EXECUTE FUNCTION reject_gateway_capture()`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS reject_gateway_capture ON ledger_hold_events; DROP FUNCTION IF EXISTS reject_gateway_capture()`)
		})
		if _, err := gatewayService.Finalize(ctx, failedPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: failedPlan.Call.LeaseGeneration,
			Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerOne.ID, HTTPStatus: http.StatusOK, Usage: usage,
			SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
		}); err == nil {
			t.Fatal("injected capture failure did not roll back atomic success")
		}
		if _, err := gatewayService.CompleteAttempt(ctx, attempt.ID, gateway.AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration,
			Status:          gateway.AttemptIncomplete, HTTPStatus: http.StatusOK, ErrorCode: "settlement_failed", RawError: "settlement could not be committed",
			TTFTObserved: true, TTFT: success.TTFT, Duration: success.Duration,
		}); err != nil {
			t.Fatal(err)
		}
		terminal, err := gatewayService.Finalize(ctx, failedPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: failedPlan.Call.LeaseGeneration,
			Status:          gateway.CallIncomplete, CompletionReason: "settlement_failed", FinalOfferID: offerOne.ID, HTTPStatus: http.StatusOK,
		})
		if err != nil {
			t.Fatal(err)
		}
		var attemptStatus, holdStatus, settlementKind string
		var remaining, captured, released int64
		if err := pool.QueryRow(ctx, `
			SELECT attempt.status, hold.status, hold.remaining_nano, hold.captured_nano, hold.released_nano, settlement.kind
			FROM api_call_attempts attempt
			JOIN api_calls call ON call.id = attempt.call_id
			JOIN ledger_holds hold ON hold.id = call.hold_id
			JOIN api_call_settlements settlement ON settlement.call_id = call.id
			WHERE call.id = $1`, failedPlan.Call.ID).Scan(&attemptStatus, &holdStatus, &remaining, &captured, &released, &settlementKind); err != nil {
			t.Fatal(err)
		}
		if terminal.Status != gateway.CallIncomplete || terminal.ProviderCharge != 0 || terminal.PlatformFee != 0 ||
			attemptStatus != string(gateway.AttemptIncomplete) || holdStatus != "closed" || remaining != 0 || captured != 0 || released == 0 || settlementKind != "released" {
			t.Fatalf("zero-charge compensation = call:%+v attempt:%s hold:%s remaining:%d captured:%d released:%d settlement:%s", terminal, attemptStatus, holdStatus, remaining, captured, released, settlementKind)
		}
	})

	t.Run("delivery confirmation and orphan compensation serialize", func(t *testing.T) {
		recoveryPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, recoveryPlan.Call.ID, recoveryPlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		success := gateway.AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration,
			Status:          gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true, Usage: usage,
		}
		if pending, err := gatewayService.Finalize(ctx, recoveryPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: recoveryPlan.Call.LeaseGeneration,
			Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: offerOne.ID, HTTPStatus: http.StatusOK, Usage: usage,
			SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
		}); err != nil || pending.Status != gateway.CallPendingDelivery {
			t.Fatalf("pending delivery = %+v, %v", pending, err)
		}
		expireGatewayCall(t, ctx, pool, recoveryPlan.Call.ID)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, confirmErr := gatewayService.ConfirmDelivery(ctx, recoveryPlan.Call.ID, recoveryPlan.Call.LeaseGeneration)
			if confirmErr != nil && !errors.Is(confirmErr, gateway.ErrConflict) {
				errs <- confirmErr
				return
			}
			errs <- nil
		}()
		go func() {
			defer wg.Done()
			_, recoverErr := gatewayService.RecoverOrphans(ctx, time.Now().Add(time.Hour), 100)
			errs <- recoverErr
		}()
		wg.Wait()
		close(errs)
		for raceErr := range errs {
			if raceErr != nil {
				t.Fatal(raceErr)
			}
		}
		recovered, err := gatewayService.GetCall(ctx, consumer, recoveryPlan.Call.ID)
		if err != nil || (recovered.Status != gateway.CallSucceeded && recovered.Status != gateway.CallIncomplete) {
			t.Fatalf("delivery race terminal = %+v, %v", recovered, err)
		}
		assertSingleGatewaySettlement(t, ctx, pool, recoveryPlan.Call.ID)
		var compensationCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_call_compensations WHERE call_id = $1`, recoveryPlan.Call.ID).Scan(&compensationCount); err != nil {
			t.Fatal(err)
		}
		if (recovered.Status == gateway.CallSucceeded && compensationCount != 0) || (recovered.Status == gateway.CallIncomplete && compensationCount != 1) {
			t.Fatalf("delivery race facts = status:%s compensations:%d", recovered.Status, compensationCount)
		}
	})

	t.Run("orphan after semantic commit without a complete result releases hold", func(t *testing.T) {
		pendingPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, pendingPlan.Call.ID, pendingPlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := gatewayService.MarkAttemptCommitted(ctx, attempt.ID, gateway.AttemptCommitObservation{
			LeaseGeneration: attempt.LeaseGeneration,
			TTFT:            time.Millisecond,
			Duration:        2 * time.Millisecond,
		}); err != nil {
			t.Fatal(err)
		}
		expireGatewayCall(t, ctx, pool, pendingPlan.Call.ID)
		if _, err := gatewayService.RecoverOrphans(ctx, time.Now().Add(time.Hour), 100); err != nil {
			t.Fatal(err)
		}
		var callStatus, decisionCode, attemptStatus, holdStatus string
		var settlementCount int
		var holdRemaining, holdReleased int64
		if err := pool.QueryRow(ctx, `
				SELECT call.status, call.decision_code, attempt.status, hold.status,
					hold.remaining_nano, hold.released_nano,
					(SELECT count(*) FROM api_call_settlements settlement WHERE settlement.call_id = call.id)
				FROM api_calls call
				JOIN api_call_attempts attempt ON attempt.call_id = call.id
				JOIN ledger_holds hold ON hold.id = call.hold_id
				WHERE call.id = $1`, pendingPlan.Call.ID).Scan(&callStatus, &decisionCode, &attemptStatus, &holdStatus, &holdRemaining, &holdReleased, &settlementCount); err != nil {
			t.Fatal(err)
		}
		if callStatus != string(gateway.CallIncomplete) || decisionCode != "orphan_recovered_after_commit" || attemptStatus != string(gateway.AttemptIncomplete) || holdStatus != "closed" || holdRemaining != 0 || holdReleased == 0 || settlementCount != 1 {
			t.Fatalf("committed orphan recovery = call:%s decision:%s attempt:%s hold:%s remaining:%d released:%d settlements:%d", callStatus, decisionCode, attemptStatus, holdStatus, holdRemaining, holdReleased, settlementCount)
		}
	})

	t.Run("invalid succeeded attempt cannot strand authorization", func(t *testing.T) {
		invalidPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, invalidPlan.Call.ID, invalidPlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := gatewayService.MarkAttemptCommitted(ctx, attempt.ID, gateway.AttemptCommitObservation{
			LeaseGeneration: attempt.LeaseGeneration,
			TTFT:            time.Millisecond,
			Duration:        2 * time.Millisecond,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := gatewayService.CompleteAttempt(ctx, attempt.ID, gateway.AttemptResult{
			LeaseGeneration: attempt.LeaseGeneration,
			Status:          gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: true,
		}); !errors.Is(err, gateway.ErrInvalidInput) {
			t.Fatalf("invalid success completion = %v, want database-backed invalid input", err)
		}
		terminal, err := gatewayService.Finalize(ctx, invalidPlan.Call.ID, gateway.FinalizeOutcome{
			LeaseGeneration: invalidPlan.Call.LeaseGeneration,
			Status:          gateway.CallIncomplete, CompletionReason: "orphan_recovered_after_commit", FinalOfferID: invalidPlan.Candidates[0].Lease.OfferID,
		})
		if err != nil || terminal.Status != gateway.CallIncomplete || terminal.ProviderCharge != 0 || terminal.PlatformFee != 0 {
			t.Fatalf("invalid success finalization = %+v, %v", terminal, err)
		}
		var remaining, released int64
		if err := pool.QueryRow(ctx, `SELECT remaining_nano, released_nano FROM ledger_holds WHERE id = $1`, invalidPlan.Call.HoldID).Scan(&remaining, &released); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 || released == 0 {
			t.Fatalf("invalid success hold = remaining:%d released:%d", remaining, released)
		}
		assertSingleGatewaySettlement(t, ctx, pool, invalidPlan.Call.ID)
	})

	t.Run("complete and orphan recovery serialize", func(t *testing.T) {
		racePlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		attempt, err := gatewayService.StartAttempt(ctx, racePlan.Call.ID, racePlan.Candidates[0])
		if err != nil {
			t.Fatal(err)
		}
		expireGatewayCall(t, ctx, pool, racePlan.Call.ID)
		var wg sync.WaitGroup
		completeResult := make(chan error, 1)
		recoverResult := make(chan error, 1)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, completeErr := gatewayService.CompleteAttempt(ctx, attempt.ID, gateway.AttemptResult{
				LeaseGeneration: attempt.LeaseGeneration,
				Status:          gateway.AttemptFailed, HTTPStatus: http.StatusBadGateway, ErrorCode: "race_failure", RawError: "upstream failed",
			})
			completeResult <- completeErr
		}()
		go func() {
			defer wg.Done()
			_, recoverErr := gatewayService.RecoverOrphans(ctx, time.Now().Add(time.Hour), 100)
			recoverResult <- recoverErr
		}()
		wg.Wait()
		completeErr := <-completeResult
		recoverErr := <-recoverResult
		if completeErr != nil && !errors.Is(completeErr, gateway.ErrConflict) {
			t.Fatal(completeErr)
		}
		if recoverErr != nil {
			t.Fatal(recoverErr)
		}
		// SKIP LOCKED may intentionally leave a call claimed by CompleteAttempt
		// for the next maintenance pass; prove that pass deterministically closes it.
		if _, err := gatewayService.RecoverOrphans(ctx, time.Now().Add(time.Hour), 100); err != nil {
			t.Fatal(err)
		}
		terminal, err := gatewayService.GetCall(ctx, consumer, racePlan.Call.ID)
		if err != nil || (terminal.Status != gateway.CallFailed && terminal.Status != gateway.CallIncomplete) {
			t.Fatalf("complete/orphan terminal = %+v, %v", terminal, err)
		}
		assertSingleGatewaySettlement(t, ctx, pool, racePlan.Call.ID)
	})

	t.Run("pool member validation version must be explicitly refreshed", func(t *testing.T) {
		staleOffer := createGatewayOffer(t, ctx, store, channelService, providerOne, "Stale relay", "https://gateway-stale.example", "upstream-secret-stale", model.ID, "vendor-stale-v1")
		staleKey, err := gatewayService.CreateAPIKey(ctx, consumer, gateway.KeyConfigInput{
			DisplayName: "版本固定 Key",
			Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{staleOffer.ID}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		baselineDashboard, err := gatewayService.Dashboard(ctx, consumer)
		if err != nil {
			t.Fatal(err)
		}
		updatedOffer, err := channelService.UpdateOffer(ctx, providerOne, staleOffer.ID, staleOffer.Version, "vendor-stale-v2", money.FromNano(money.Scale))
		if err != nil {
			t.Fatal(err)
		}
		passValidation(t, ctx, store, providerOne, updatedOffer.ID)
		loaded, err := gatewayService.GetAPIKey(ctx, consumer, staleKey.APIKey.ID)
		if err != nil {
			t.Fatal(err)
		}
		member := loaded.Pools[0].Members[0]
		if member.Eligible || member.IneligibleReason != "validation_version_changed" || member.AddedValidationVersion == member.CurrentValidationVersion {
			t.Fatalf("stale pool member projection = %+v", member)
		}
		staleDashboard, err := gatewayService.Dashboard(ctx, consumer)
		if err != nil || staleDashboard.PendingItems != baselineDashboard.PendingItems+1 {
			t.Fatalf("stale pool dashboard = %+v, baseline=%+v, err=%v", staleDashboard, baselineDashboard, err)
		}
		staleAuthentication, err := gatewayService.Authenticate(ctx, staleKey.Secret)
		if err != nil {
			t.Fatal(err)
		}
		rejected, err := gatewayService.BeginCall(ctx, staleAuthentication, channel.ProtocolOpenAIChat, model.ID)
		if !errors.Is(err, gateway.ErrRejected) || rejected.Call.DecisionCode != "no_eligible_offer" {
			t.Fatalf("stale pool call = %+v, %v", rejected, err)
		}
		renamed, err := gatewayService.UpdateAPIKey(ctx, consumer, loaded.ID, loaded.Version, gateway.KeyConfigInput{
			DisplayName: "仅改名称仍保持旧验证版本",
			Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{staleOffer.ID}}},
		})
		if err != nil || renamed.Pools[0].Members[0].Eligible || renamed.Pools[0].Members[0].AddedValidationVersion != member.AddedValidationVersion {
			t.Fatalf("rename silently refreshed stale member = %+v, %v", renamed, err)
		}
		removed, err := gatewayService.UpdateAPIKey(ctx, consumer, renamed.ID, renamed.Version, gateway.KeyConfigInput{
			DisplayName: renamed.DisplayName,
			Pools:       nil,
		})
		if err != nil || len(removed.Pools) != 0 {
			t.Fatalf("explicit stale member removal = %+v, %v", removed, err)
		}
		refreshed, err := gatewayService.UpdateAPIKey(ctx, consumer, removed.ID, removed.Version, gateway.KeyConfigInput{
			DisplayName: removed.DisplayName,
			Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{staleOffer.ID}}},
		})
		if err != nil || !refreshed.Pools[0].Members[0].Eligible || refreshed.Pools[0].Members[0].AddedValidationVersion != refreshed.Pools[0].Members[0].CurrentValidationVersion {
			t.Fatalf("explicitly re-added pool member = %+v, %v", refreshed, err)
		}
		refreshedDashboard, err := gatewayService.Dashboard(ctx, consumer)
		if err != nil || refreshedDashboard.PendingItems != baselineDashboard.PendingItems {
			t.Fatalf("refreshed pool dashboard = %+v, baseline=%+v, err=%v", refreshedDashboard, baselineDashboard, err)
		}
		resumed := beginGatewayCall(t, ctx, gatewayService, staleAuthentication, model.ID)
		if len(resumed.Candidates) != 1 || resumed.Candidates[0].Lease.ValidationVersion != refreshed.Pools[0].Members[0].AddedValidationVersion {
			t.Fatalf("refreshed call candidates = %+v", resumed.Candidates)
		}
		if _, err := gatewayService.Finalize(ctx, resumed.Call.ID, gateway.FinalizeOutcome{LeaseGeneration: resumed.Call.LeaseGeneration, Status: gateway.CallIncomplete, CompletionReason: "no_attempt_completed"}); err != nil {
			t.Fatal(err)
		}
		managedChannel, err := channelService.GetMine(ctx, providerOne, staleOffer.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		paused, err := channelService.SetStatus(ctx, providerOne, managedChannel.ID, managedChannel.Version, channel.StatusPaused, "")
		if err != nil {
			t.Fatal(err)
		}
		pausedDashboard, err := gatewayService.Dashboard(ctx, consumer)
		if err != nil || pausedDashboard.PendingItems != baselineDashboard.PendingItems+1 {
			t.Fatalf("paused member dashboard = %+v, baseline=%+v, err=%v", pausedDashboard, baselineDashboard, err)
		}
		pausedCall, err := gatewayService.BeginCall(ctx, staleAuthentication, channel.ProtocolOpenAIChat, model.ID)
		if !errors.Is(err, gateway.ErrRejected) || pausedCall.Call.DecisionCode != "no_eligible_offer" {
			t.Fatalf("paused pool call = %+v, %v", pausedCall, err)
		}
		if _, err := channelService.SetStatus(ctx, providerOne, paused.ID, paused.Version, channel.StatusPublished, ""); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("long multibyte upstream error code cannot block recovery", func(t *testing.T) {
		failurePlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		startAndFailGatewayCandidate(t, ctx, gatewayService, failurePlan.Call.ID, failurePlan.Candidates[0], "first_error", "first")
		longCode := strings.Repeat("错误", 40)
		stored := startAndFailGatewayCandidate(t, ctx, gatewayService, failurePlan.Call.ID, failurePlan.Candidates[1], longCode, "second")
		if stored.ErrorCode != longCode || utf8.RuneCountInString(stored.ErrorCode) != 80 {
			t.Fatalf("stored multibyte error code = %q", stored.ErrorCode)
		}
		expireGatewayCall(t, ctx, pool, failurePlan.Call.ID)
		if _, err := gatewayService.RecoverOrphans(ctx, time.Now().Add(time.Hour), 100); err != nil {
			t.Fatal(err)
		}
		terminal, err := gatewayService.GetCall(ctx, consumer, failurePlan.Call.ID)
		if err != nil || terminal.Status != gateway.CallFailed || terminal.DecisionCode != "upstream_error_code_too_long" || len(terminal.Attempts) != 2 || terminal.Attempts[1].ErrorCode != longCode {
			t.Fatalf("long-code recovery = %+v, %v", terminal, err)
		}
		assertSingleGatewaySettlement(t, ctx, pool, failurePlan.Call.ID)
	})

	t.Run("concurrent calls on one key retry serialization failures", func(t *testing.T) {
		type beginResult struct {
			plan gateway.CallPlan
			err  error
		}
		results := make(chan beginResult, 2)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				plan, err := gatewayService.BeginCall(ctx, authenticated, channel.ProtocolOpenAIChat, model.ID)
				results <- beginResult{plan: plan, err: err}
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		for result := range results {
			if result.err != nil {
				t.Fatalf("concurrent BeginCall: %v", result.err)
			}
			if _, err := gatewayService.Finalize(ctx, result.plan.Call.ID, gateway.FinalizeOutcome{LeaseGeneration: result.plan.Call.LeaseGeneration, Status: gateway.CallIncomplete, CompletionReason: "no_attempt_completed"}); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("only an observed streaming generation interval contributes TPS", func(t *testing.T) {
		cases := []struct {
			name         string
			measureTPS   bool
			ttftObserved bool
			ttft         time.Duration
			duration     time.Duration
		}{
			{name: "nonstream response", measureTPS: false, ttftObserved: false, duration: 100 * time.Millisecond},
			{name: "zero streaming generation interval", measureTPS: true, ttftObserved: true, ttft: 10 * time.Millisecond, duration: 10 * time.Millisecond},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				metricPlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
				attempt, err := gatewayService.StartAttempt(ctx, metricPlan.Call.ID, metricPlan.Candidates[0])
				if err != nil {
					t.Fatal(err)
				}
				success := gateway.AttemptResult{
					LeaseGeneration: attempt.LeaseGeneration,
					Status:          gateway.AttemptSucceeded, HTTPStatus: http.StatusOK, SemanticCommitted: testCase.ttftObserved,
					MeasureTPS: testCase.measureTPS, TTFTObserved: testCase.ttftObserved, TTFT: testCase.ttft, Duration: testCase.duration, Usage: usage,
				}
				pending, err := gatewayService.Finalize(ctx, metricPlan.Call.ID, gateway.FinalizeOutcome{
					LeaseGeneration: metricPlan.Call.LeaseGeneration,
					Status:          gateway.CallSucceeded, CompletionReason: "completed", FinalOfferID: metricPlan.Candidates[0].Lease.OfferID,
					HTTPStatus: http.StatusOK, Usage: usage, SuccessAttemptID: attempt.ID, SuccessAttempt: &success,
				})
				if err != nil || pending.Status != gateway.CallPendingDelivery || len(pending.Attempts) != 1 {
					t.Fatalf("metric pending delivery = %+v, %v", pending, err)
				}
				completedAttempt := pending.Attempts[0]
				if testCase.ttftObserved {
					if completedAttempt.TTFT == nil || *completedAttempt.TTFT != testCase.ttft {
						t.Fatalf("observed TTFT = %+v", completedAttempt)
					}
				} else if completedAttempt.TTFT != nil {
					t.Fatalf("nonstream TTFT was fabricated = %+v", completedAttempt)
				}
				if completedAttempt.Duration == nil || *completedAttempt.Duration != testCase.duration || completedAttempt.TokensPerSecondNano != nil {
					t.Fatalf("nullable completed metrics = %+v", completedAttempt)
				}
				var tps sql.NullInt64
				if err := pool.QueryRow(ctx, `SELECT tokens_per_second_nano FROM api_call_attempts WHERE id = $1`, attempt.ID).Scan(&tps); err != nil {
					t.Fatal(err)
				}
				if tps.Valid {
					t.Fatalf("fabricated TPS = %d", tps.Int64)
				}
				if _, err := gatewayService.ConfirmDelivery(ctx, metricPlan.Call.ID, metricPlan.Call.LeaseGeneration); err != nil {
					t.Fatal(err)
				}
				loadedCall, err := gatewayService.GetCall(ctx, consumer, metricPlan.Call.ID)
				if err != nil || len(loadedCall.Attempts) != 1 || loadedCall.Attempts[0].Duration == nil || loadedCall.Attempts[0].TokensPerSecondNano != nil || (testCase.ttftObserved != (loadedCall.Attempts[0].TTFT != nil)) {
					t.Fatalf("loaded nullable metrics = %+v, %v", loadedCall.Attempts, err)
				}
			})
		}
		market, _, err := channelService.ListMarket(ctx, consumer, channel.MarketQuery{Sort: "tps"})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range market {
			if item.OfferID == offerOne.ID && item.TokensPerSecond != nil {
				t.Fatalf("nonstream/zero-interval samples entered market TPS: %+v", item)
			}
		}
	})

	t.Run("api call snapshots and final facts are immutable", func(t *testing.T) {
		immutablePlan := beginGatewayCall(t, ctx, gatewayService, authenticated, model.ID)
		if _, err := pool.Exec(ctx, `UPDATE api_calls SET canonical_model_id = canonical_model_id || '-tampered' WHERE id = $1`, immutablePlan.Call.ID); err == nil || !strings.Contains(err.Error(), "api call snapshot is immutable") {
			t.Fatalf("in-progress snapshot tamper = %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE api_calls SET heartbeat_at = now(), lease_expires_at = now() + interval '1 minute' WHERE id = $1`, immutablePlan.Call.ID); err != nil {
			t.Fatalf("legitimate lease heartbeat: %v", err)
		}
		if _, err := gatewayService.Finalize(ctx, immutablePlan.Call.ID, gateway.FinalizeOutcome{LeaseGeneration: immutablePlan.Call.LeaseGeneration, Status: gateway.CallIncomplete, CompletionReason: "no_attempt_completed"}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE api_calls SET completion_reason = 'tampered' WHERE id = $1`, immutablePlan.Call.ID); err == nil || !strings.Contains(err.Error(), "completed api call is immutable") {
			t.Fatalf("terminal fact tamper = %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM api_calls WHERE id = $1`, immutablePlan.Call.ID); err == nil || !strings.Contains(err.Error(), "api calls cannot be deleted") {
			t.Fatalf("terminal call delete = %v", err)
		}

		rejected, err := gatewayService.BeginCall(ctx, authenticated, channel.ProtocolAnthropic, model.ID)
		if !errors.Is(err, gateway.ErrRejected) || rejected.Call.Status != gateway.CallRejected || rejected.Call.DecisionCode != "pool_not_found" {
			t.Fatalf("rejected immutable call = %+v, %v", rejected, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE api_calls SET decision_code = 'tampered' WHERE id = $1`, rejected.Call.ID); err == nil || !strings.Contains(err.Error(), "completed api call is immutable") {
			t.Fatalf("rejected call tamper = %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM api_calls WHERE id = $1`, rejected.Call.ID); err == nil || !strings.Contains(err.Error(), "api calls cannot be deleted") {
			t.Fatalf("rejected call delete = %v", err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE api_fee_rates SET fee_rate_nano = fee_rate_nano + 1
			WHERE version = (SELECT fee_rate_version FROM api_calls WHERE id = $1)`, immutablePlan.Call.ID); err == nil || !strings.Contains(err.Error(), "api gateway fact is immutable") {
			t.Fatalf("fee-rate version tamper = %v", err)
		}
		if _, err := pool.Exec(ctx, `
			DELETE FROM api_fee_rates
			WHERE version = (SELECT fee_rate_version FROM api_calls WHERE id = $1)`, immutablePlan.Call.ID); err == nil || !strings.Contains(err.Error(), "api gateway history cannot be physically deleted") {
			t.Fatalf("fee-rate version delete = %v", err)
		}
	})
}

func inviteGatewayAccount(t *testing.T, ctx context.Context, service *identity.Service, admin identity.Account, username, displayName string, credit money.Amount) identity.Account {
	t.Helper()
	created, err := service.CreateInvitedAccount(ctx, admin, username, displayName, credit, false, identity.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return changeGatewayPassword(t, ctx, service, created.Account, created.InitialPassword, "Gateway-member-password-2026")
}

func changeGatewayPassword(t *testing.T, ctx context.Context, service *identity.Service, account identity.Account, current, replacement string) identity.Account {
	t.Helper()
	changed, err := service.ChangePassword(ctx, account.ID, current, replacement)
	if err != nil {
		t.Fatal(err)
	}
	return changed.Account
}

func createGatewayOffer(t *testing.T, ctx context.Context, store *storepg.Store, service *channel.Service, owner identity.Account, name, baseURL, credential, modelID, upstreamModel string) channel.Offer {
	t.Helper()
	created, err := service.Create(ctx, owner, name, baseURL, credential, []channel.OfferInput{{
		ModelID: modelID, Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: upstreamModel, Multiplier: money.FromNano(money.Scale),
	}})
	if err != nil {
		t.Fatal(err)
	}
	passValidation(t, ctx, store, owner, created.Offers[0].ID)
	published, err := service.SetStatus(ctx, owner, created.ID, created.Version, channel.StatusPublished, "")
	if err != nil {
		t.Fatal(err)
	}
	return published.Offers[0]
}

func beginGatewayCall(t *testing.T, ctx context.Context, service *gateway.Service, authenticated gateway.AuthenticatedKey, modelID string) gateway.CallPlan {
	t.Helper()
	plan, err := service.BeginCall(ctx, authenticated, channel.ProtocolOpenAIChat, modelID)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func startAndFailGatewayCandidate(t *testing.T, ctx context.Context, service *gateway.Service, callID string, candidate gateway.Candidate, code, raw string) gateway.Attempt {
	t.Helper()
	attempt, err := service.StartAttempt(ctx, callID, candidate)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteAttempt(ctx, attempt.ID, gateway.AttemptResult{LeaseGeneration: attempt.LeaseGeneration, Status: gateway.AttemptFailed, HTTPStatus: http.StatusBadGateway, ErrorCode: code, RawError: raw})
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func expireGatewayCall(t *testing.T, ctx context.Context, pool *pgxpool.Pool, callID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE api_calls SET heartbeat_at = now() - interval '1 hour', lease_expires_at = now() - interval '1 hour' WHERE id = $1`, callID); err != nil {
		t.Fatal(err)
	}
}

func assertSingleGatewaySettlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, callID string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_call_settlements WHERE call_id = $1`, callID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("settlement count for %s = %d, want 1", callID, count)
	}
}
