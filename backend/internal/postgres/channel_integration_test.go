package postgres_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

func TestChannelIntegration(t *testing.T) {
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
	schema := "channel_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop channel schema: %v", err)
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
	changedAdmin, err := identityService.ChangePassword(ctx, admin.ID, "Bootstrap-password-2026", "Channel-admin-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	admin = changedAdmin.Account
	ownerCreated, err := identityService.CreateInvitedAccount(ctx, admin, "channel.owner", "渠道共享者", money.Amount(10*money.Scale), false, identity.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	ownerChanged, err := identityService.ChangePassword(ctx, ownerCreated.Account.ID, ownerCreated.InitialPassword, "Channel-owner-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerChanged.Account
	consumerCreated, err := identityService.CreateInvitedAccount(ctx, admin, "channel.consumer", "渠道消费者", money.Amount(10*money.Scale), false, identity.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	consumerChanged, err := identityService.ChangePassword(ctx, consumerCreated.Account.ID, consumerCreated.InitialPassword, "Channel-consumer-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	consumer := consumerChanged.Account

	catalogService := catalog.NewService(store)
	model, err := catalogService.Create(ctx, admin, catalog.Model{
		ID: "openai/gpt-5-mini", Name: "GPT-5 mini", Provider: "OpenAI", ContextWindow: 128000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"}, SupportsTools: true,
		InputPrice: money.FromNano(250_000_000), OutputPrice: money.FromNano(2_000_000_000),
		CacheWritePrice: 0, CacheReadPrice: money.FromNano(25_000_000), Status: catalog.StatusActive,
	})
	if err != nil || model.ID == "" {
		t.Fatalf("create model: %+v, %v", model, err)
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	keyring, err := channel.ParseKeyring("v1="+key, "v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := channel.NewOutboundPolicyWithResolver(nil, nil, integrationPublicResolver{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := channel.NewService(store, keyring, policy)
	if err != nil {
		t.Fatal(err)
	}
	maximumPriceModel, err := catalogService.Create(ctx, admin, catalog.Model{
		ID: "test/maximum-price", Name: "Maximum price", Provider: "Test", ContextWindow: 8192,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: catalog.MaxPriceNanoPerMillion, OutputPrice: catalog.MaxPriceNanoPerMillion,
		CacheWritePrice: catalog.MaxPriceNanoPerMillion, CacheReadPrice: catalog.MaxPriceNanoPerMillion,
		Status: catalog.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE models SET input_price_nano_per_million = $2 WHERE id = $1`, maximumPriceModel.ID, catalog.MaxPriceNanoPerMillion.Nano()+1); err == nil {
		t.Fatal("database accepted a catalog price that can overflow channel projections")
	}
	maximumPriceChannel, err := service.Create(ctx, owner, "Maximum Price Relay", "https://maximum-price.example", "maximum-price-secret", []channel.OfferInput{{
		ModelID: maximumPriceModel.ID, Protocol: channel.ProtocolOpenAIResponse, UpstreamModelID: "maximum-price",
		Multiplier: money.FromNano(1_000 * money.Scale),
	}})
	if err != nil {
		t.Fatal(err)
	}
	passValidation(t, ctx, store, owner, maximumPriceChannel.Offers[0].ID)
	maximumPriceChannel, err = service.SetStatus(ctx, owner, maximumPriceChannel.ID, maximumPriceChannel.Version, channel.StatusPublished, "")
	if err != nil {
		t.Fatal(err)
	}
	maximumPriceMarket, _, err := service.ListMarket(ctx, consumer, channel.MarketQuery{ModelID: maximumPriceModel.ID})
	if err != nil || len(maximumPriceMarket) != 1 || maximumPriceMarket[0].InputPrice.String() != "100000000" || maximumPriceMarket[0].OutputPrice.String() != "100000000" {
		t.Fatalf("maximum price market projection = %#v, %v", maximumPriceMarket, err)
	}
	if _, err := service.SetStatus(ctx, owner, maximumPriceChannel.ID, maximumPriceChannel.Version, channel.StatusPaused, ""); err != nil {
		t.Fatal(err)
	}
	raceModel, err := catalogService.Create(ctx, admin, catalog.Model{
		ID: "openai/model-disable-race", Name: "Model disable race", Provider: "OpenAI", ContextWindow: 8192,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: money.FromNano(100_000_000), OutputPrice: money.FromNano(200_000_000), Status: catalog.StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	raceChannel, err := service.Create(ctx, owner, "Model Lock Relay", "https://model-lock.example", "model-lock-secret", []channel.OfferInput{{
		ModelID: raceModel.ID, Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: "model-disable-race", Multiplier: money.FromNano(money.Scale),
	}})
	if err != nil {
		t.Fatal(err)
	}
	disableModel, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disableModel.Exec(ctx, `UPDATE models SET status = 'disabled', version = version + 1 WHERE id = $1`, raceModel.ID); err != nil {
		t.Fatal(err)
	}
	addResult := make(chan error, 1)
	go func() {
		_, addErr := service.AddOffer(context.Background(), owner, raceChannel.ID, raceChannel.Version, channel.OfferInput{
			ModelID: raceModel.ID, Protocol: channel.ProtocolOpenAIResponse, UpstreamModelID: "model-disable-race", Multiplier: money.FromNano(money.Scale),
		})
		addResult <- addErr
	}()
	select {
	case addErr := <-addResult:
		t.Fatalf("add offer bypassed in-flight model disable: %v", addErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := disableModel.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case addErr := <-addResult:
		if !errors.Is(addErr, channel.ErrUnavailable) {
			t.Fatalf("add offer after model disable error = %v", addErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("add offer remained blocked after model disable committed")
	}
	const upstreamSecret = "integration-upstream-secret"
	created, err := service.Create(ctx, owner, "Aurora Relay", "https://gateway.example/prefix", upstreamSecret, []channel.OfferInput{
		{ModelID: model.ID, Protocol: channel.ProtocolOpenAIResponse, UpstreamModelID: "gpt-5-mini", Multiplier: money.FromNano(1_250_000_000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != channel.StatusDraft || !created.CredentialConfigured || created.CredentialVersion != 1 || len(created.Offers) != 1 {
		t.Fatalf("created channel = %+v", created)
	}
	var plaintextOccurrences int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM channel_credentials
		WHERE position(convert_to($1, 'UTF8') in ciphertext) > 0`, upstreamSecret).Scan(&plaintextOccurrences); err != nil {
		t.Fatal(err)
	}
	if plaintextOccurrences != 0 {
		t.Fatal("upstream credential was stored as plaintext")
	}
	market, _, err := service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if err != nil || len(market) != 0 {
		t.Fatalf("draft market = %#v, %v", market, err)
	}
	if _, err := service.SetStatus(ctx, owner, created.ID, created.Version, channel.StatusPublished, ""); !errors.Is(err, channel.ErrUnavailable) {
		t.Fatalf("publish without validated offer error = %v", err)
	}

	passValidation(t, ctx, store, owner, created.Offers[0].ID)
	published, err := service.SetStatus(ctx, owner, created.ID, created.Version, channel.StatusPublished, "")
	if err != nil {
		t.Fatal(err)
	}
	market, _, err = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if err != nil || len(market) != 1 || market[0].InputPrice.String() != "0.3125" || market[0].CallCount != nil || market[0].LastTestedAt == nil {
		t.Fatalf("published market = %#v, %v", market, err)
	}
	publishedOwnerView, err := service.GetMine(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	publishedOwnerOffer := findOffer(t, publishedOwnerView.Offers, created.Offers[0].ID)
	if !publishedOwnerOffer.Eligible || publishedOwnerOffer.IneligibleReason != "" {
		t.Fatalf("published owner eligibility = %+v", publishedOwnerOffer)
	}
	var originalCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT ciphertext FROM channel_credentials WHERE channel_id = $1`, created.ID).Scan(&originalCiphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_credentials SET ciphertext = $2 WHERE channel_id = $1`, created.ID, bytes.Repeat([]byte{9}, 17)); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateCredentialInventory(ctx); err == nil {
		t.Fatal("credential inventory accepted corrupted ciphertext")
	}
	statuses, leases, err := service.ResolveRoutingLeases(ctx, []string{created.Offers[0].ID})
	if err != nil || len(statuses) != 1 || statuses[0].IneligibleReason != "credential_unavailable" || len(leases) != 0 {
		t.Fatalf("corrupted credential routing = %#v %#v %v", statuses, leases, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_credentials SET ciphertext = $2 WHERE channel_id = $1`, created.ID, originalCiphertext); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateCredentialInventory(ctx); err != nil {
		t.Fatalf("restored credential inventory: %v", err)
	}
	var originalCredentialTarget channel.ReencryptTarget
	credentialInventory, err := store.CredentialInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range credentialInventory {
		if target.ChannelID == created.ID {
			originalCredentialTarget = target
			break
		}
	}
	if originalCredentialTarget.ChannelID == "" {
		t.Fatal("created channel credential was absent from inventory")
	}
	reencrypted := channel.EncryptedCredential{Version: originalCredentialTarget.Credential.Version, KeyID: "v2", Nonce: bytes.Repeat([]byte{8}, 12), Ciphertext: bytes.Repeat([]byte{8}, 17)}
	if err := store.StoreReencryptedCredential(ctx, originalCredentialTarget, reencrypted, admin.ID); err != nil {
		t.Fatalf("first re-encryption: %v", err)
	}
	staleReplacement := channel.EncryptedCredential{Version: originalCredentialTarget.Credential.Version, KeyID: "v3", Nonce: bytes.Repeat([]byte{6}, 12), Ciphertext: bytes.Repeat([]byte{6}, 17)}
	if err := store.StoreReencryptedCredential(ctx, originalCredentialTarget, staleReplacement, admin.ID); !errors.Is(err, channel.ErrConflict) {
		t.Fatalf("stale re-encryption error = %v", err)
	}
	credentialInventory, err = store.CredentialInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var currentCredentialTarget channel.ReencryptTarget
	for _, target := range credentialInventory {
		if target.ChannelID == created.ID {
			currentCredentialTarget = target
			break
		}
	}
	if err := store.StoreReencryptedCredential(ctx, currentCredentialTarget, originalCredentialTarget.Credential, admin.ID); err != nil {
		t.Fatalf("restore re-encrypted credential: %v", err)
	}

	_, err = store.StartValidation(ctx, owner, raceChannel.Offers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.ExpireValidationAttempts(ctx, time.Now().Add(time.Second)); err != nil || recovered != 1 {
		t.Fatalf("recover abandoned validation = %d, %v", recovered, err)
	}
	abandonedHistory, err := service.ListValidationAttempts(ctx, owner, raceChannel.Offers[0].ID, 10)
	if err != nil || len(abandonedHistory) != 1 || abandonedHistory[0].Status != channel.ValidationFailed || abandonedHistory[0].ErrorCategory != channel.ErrorTimeout {
		t.Fatalf("abandoned validation history = %#v, %v", abandonedHistory, err)
	}
	if err := store.CompleteValidation(ctx, abandonedHistory[0]); !errors.Is(err, channel.ErrConflict) {
		t.Fatalf("expired attempt accepted a second completion: %v", err)
	}

	offer := published.Offers[0]
	updatedOffer, err := service.UpdateOffer(ctx, owner, offer.ID, offer.Version, offer.UpstreamModelID, money.FromNano(2_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if updatedOffer.ValidationVersion != offer.ValidationVersion {
		t.Fatal("multiplier-only edit incorrectly invalidated validation")
	}
	market, _, _ = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if len(market) != 1 || market[0].InputPrice.String() != "0.5" {
		t.Fatalf("multiplier market = %#v", market)
	}

	channelForAdd, err := service.GetMine(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondOffer, err := service.AddOffer(ctx, owner, created.ID, channelForAdd.Version, channel.OfferInput{
		ModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: "gpt-5-mini", Multiplier: money.FromNano(2_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddOffer(ctx, owner, created.ID, channelForAdd.Version, channel.OfferInput{
		ModelID: model.ID, Protocol: channel.ProtocolAnthropic, UpstreamModelID: "gpt-5-mini", Multiplier: money.FromNano(2_000_000_000),
	}); !errors.Is(err, channel.ErrConflict) {
		t.Fatalf("stale add-offer CAS error = %v", err)
	}
	channelForAdd, err = service.GetMine(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	thirdOffer, err := service.AddOffer(ctx, owner, created.ID, channelForAdd.Version, channel.OfferInput{
		ModelID: model.ID, Protocol: channel.ProtocolAnthropic, UpstreamModelID: "gpt-5-mini", Multiplier: money.FromNano(2_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	deletedThird, err := service.SetOfferStatus(ctx, owner, thirdOffer.ID, thirdOffer.Version, channel.OfferDeleted)
	if err != nil || deletedThird.Multiplier.String() != "2" {
		t.Fatalf("delete third offer = %#v, %v", deletedThird, err)
	}
	beforeSharedUpdate, err := service.GetMine(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstBeforeShared := findOffer(t, beforeSharedUpdate.Offers, created.Offers[0].ID)
	secondBeforeShared := findOffer(t, beforeSharedUpdate.Offers, secondOffer.ID)
	if _, err := service.UpdateOffer(ctx, owner, firstBeforeShared.ID, firstBeforeShared.Version, firstBeforeShared.UpstreamModelID, money.FromNano(2_500_000_000)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateOffer(ctx, owner, secondBeforeShared.ID, secondBeforeShared.Version, secondBeforeShared.UpstreamModelID, money.FromNano(2_500_000_000)); !errors.Is(err, channel.ErrConflict) {
		t.Fatalf("stale sibling multiplier CAS error = %v", err)
	}
	afterSharedUpdate, err := service.GetMine(ctx, owner, created.ID)
	if err != nil || findOffer(t, afterSharedUpdate.Offers, thirdOffer.ID).Multiplier.String() != "2" {
		t.Fatalf("deleted multiplier snapshot drifted: %#v, %v", afterSharedUpdate, err)
	}
	passValidation(t, ctx, store, owner, secondOffer.ID)
	firstPage, cursor, err := service.ListMarket(ctx, consumer, channel.MarketQuery{Limit: 1})
	if err != nil || len(firstPage) != 1 || cursor == "" {
		t.Fatalf("first market page = %#v %q %v", firstPage, cursor, err)
	}
	if _, _, err := service.ListMarket(ctx, consumer, channel.MarketQuery{Cursor: "not-a-market-cursor", Limit: 1}); !errors.Is(err, channel.ErrInvalidInput) {
		t.Fatalf("malformed market cursor error = %v", err)
	}
	if _, _, err := service.ListMarket(ctx, consumer, channel.MarketQuery{Cursor: cursor, Sort: "rating", Limit: 1}); !errors.Is(err, channel.ErrInvalidInput) {
		t.Fatalf("market cursor reused across sort error = %v", err)
	}
	currentForCursor, err := service.GetMine(ctx, owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	cursorOffer := findOffer(t, currentForCursor.Offers, firstPage[0].OfferID)
	disabledCursor, err := service.SetOfferStatus(ctx, owner, cursorOffer.ID, cursorOffer.Version, channel.OfferDisabled)
	if err != nil {
		t.Fatal(err)
	}
	secondPage, _, err := service.ListMarket(ctx, consumer, channel.MarketQuery{Cursor: cursor, Limit: 1})
	if err != nil || len(secondPage) != 1 || secondPage[0].OfferID == firstPage[0].OfferID {
		t.Fatalf("market cursor after ineligibility = %#v, %v", secondPage, err)
	}
	if _, err := service.SetOfferStatus(ctx, owner, disabledCursor.ID, disabledCursor.Version, channel.OfferActive); err != nil {
		t.Fatal(err)
	}

	staleTarget, err := store.StartValidation(ctx, owner, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	market, _, _ = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if len(market) != 1 {
		t.Fatalf("in-progress offer was not excluded independently: %#v", market)
	}
	latestBeforeBaseUpdate, err := service.GetMine(ctx, owner, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedChannel, err := service.Update(ctx, owner, published.ID, latestBeforeBaseUpdate.Version, published.DisplayName, "https://gateway.example/new-prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	completeAttempt(t, ctx, store, staleTarget.Attempt, channel.ValidationPassed)
	market, _, _ = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if len(market) != 0 {
		t.Fatal("old validation completion restored a new validation version")
	}
	passValidation(t, ctx, store, owner, offer.ID)
	market, _, _ = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	if len(market) != 1 {
		t.Fatalf("current validation did not restore market: %#v", market)
	}
	history, err := service.ListValidationAttempts(ctx, owner, offer.ID, 10)
	if err != nil || len(history) != 3 || history[0].ValidationVersion != 2 || history[0].AttemptSeq != 1 || history[0].Status != channel.ValidationPassed || history[0].HTTPStatus != 204 {
		t.Fatalf("owner validation history = %#v, %v", history, err)
	}
	adminHistory, err := service.ListValidationAttempts(ctx, admin, offer.ID, 2)
	if err != nil || len(adminHistory) != 2 {
		t.Fatalf("administrator validation history = %#v, %v", adminHistory, err)
	}
	if _, err := service.ListValidationAttempts(ctx, consumer, offer.ID, 10); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("unrelated consumer validation history error = %v", err)
	}
	var completedAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'channel.validation_completed' AND target_id = $1`, offer.ID).Scan(&completedAuditCount); err != nil {
		t.Fatal(err)
	}
	if completedAuditCount != 3 {
		t.Fatalf("validation completion audit count = %d", completedAuditCount)
	}

	type ratingResult struct {
		channel channel.Channel
		err     error
	}
	ratingResults := make(chan ratingResult, 2)
	ratingStart := make(chan struct{})
	for _, input := range []struct {
		actor identity.Account
		score int
	}{{owner, 5}, {consumer, 3}} {
		input := input
		go func() {
			<-ratingStart
			value, rateErr := service.Rate(context.Background(), input.actor, created.ID, input.score)
			ratingResults <- ratingResult{channel: value, err: rateErr}
		}()
	}
	close(ratingStart)
	observedRatingCounts := map[int64]bool{}
	for range 2 {
		result := <-ratingResults
		if result.err != nil {
			t.Fatal(result.err)
		}
		observedRatingCounts[result.channel.RatingCount] = true
	}
	if !observedRatingCounts[1] || !observedRatingCounts[2] {
		t.Fatalf("concurrent rating responses did not serialize: %#v", observedRatingCounts)
	}
	rated, err := service.GetMarketChannel(ctx, consumer, created.ID)
	if err != nil || rated.AverageRating == nil || *rated.AverageRating != "4.00" || rated.RatingCount != 2 {
		t.Fatalf("ratings = %+v, %v", rated, err)
	}
	paused, err := service.SetStatus(ctx, owner, created.ID, updatedChannel.Version, channel.StatusPaused, "")
	if err != nil {
		t.Fatal(err)
	}
	market, _, _ = service.ListMarket(ctx, consumer, channel.MarketQuery{})
	publicPaused, publicErr := service.GetMarketChannel(ctx, consumer, created.ID)
	if len(market) != 0 || publicErr != nil || publicPaused.Status != channel.StatusPaused || len(publicPaused.Offers) != 0 {
		t.Fatalf("paused market/detail = %#v %+v %v", market, publicPaused, publicErr)
	}
	newStatus := identity.StatusDisabled
	consumerDisabled, err := identityService.UpdateAccount(ctx, admin, consumer.ID, identity.AccountUpdate{ExpectedVersion: consumer.Version, Status: &newStatus})
	if err != nil || consumerDisabled.Status != identity.StatusDisabled {
		t.Fatalf("disable consumer: %+v, %v", consumerDisabled, err)
	}
	ownerView, err := service.GetMine(ctx, owner, created.ID)
	if err != nil || ownerView.RatingCount != 2 {
		t.Fatalf("disabled rater disappeared: %+v, %v", ownerView, err)
	}
	pausedOwnerOffer := findOffer(t, ownerView.Offers, created.Offers[0].ID)
	if pausedOwnerOffer.Eligible || pausedOwnerOffer.IneligibleReason != "channel_unpublished" {
		t.Fatalf("paused owner eligibility = %+v", pausedOwnerOffer)
	}

	resumed, err := service.SetStatus(ctx, owner, created.ID, paused.Version, channel.StatusPublished, "")
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := service.RevokeCredential(ctx, owner, created.ID, resumed.Version)
	if err != nil || revoked.CredentialConfigured || revoked.CredentialVersion != 2 {
		t.Fatalf("revoked credential = %+v, %v", revoked, err)
	}
	revokedOwnerOffer := findOffer(t, revoked.Offers, created.Offers[0].ID)
	if revokedOwnerOffer.Eligible || revokedOwnerOffer.IneligibleReason != "credential_unavailable" {
		t.Fatalf("revoked owner eligibility = %+v", revokedOwnerOffer)
	}
	if _, err := service.Update(ctx, owner, created.ID, resumed.Version, created.DisplayName, "https://gateway.example/new-prefix", nil); !errors.Is(err, channel.ErrConflict) {
		t.Fatalf("stale channel update error = %v", err)
	}
	statuses, leases, err = service.ResolveRoutingLeases(ctx, []string{offer.ID, "00000000-0000-4000-8000-000000000001"})
	if err != nil || len(statuses) != 2 || statuses[0].IneligibleReason != "credential_unavailable" || statuses[1].IneligibleReason != "not_found" || len(leases) != 0 {
		t.Fatalf("routing after revoke = %#v %#v %v", statuses, leases, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM channel_validation_attempts WHERE offer_id = $1`, offer.ID); err == nil {
		t.Fatal("validation history trigger allowed deletion")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM channel_offers WHERE id = $1`, thirdOffer.ID); err == nil {
		t.Fatal("channel history trigger allowed physical offer deletion")
	}
}

type integrationPublicResolver struct{}

func (integrationPublicResolver) LookupNetIP(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

func findOffer(t *testing.T, offers []channel.Offer, offerID string) channel.Offer {
	t.Helper()
	for _, offer := range offers {
		if offer.ID == offerID {
			return offer
		}
	}
	t.Fatalf("offer %s not found", offerID)
	return channel.Offer{}
}

func passValidation(t *testing.T, ctx context.Context, store *storepg.Store, actor identity.Account, offerID string) {
	t.Helper()
	target, err := store.StartValidation(ctx, actor, offerID)
	if err != nil {
		t.Fatal(err)
	}
	completeAttempt(t, ctx, store, target.Attempt, channel.ValidationPassed)
}

func completeAttempt(t *testing.T, ctx context.Context, store *storepg.Store, attempt channel.ValidationAttempt, status channel.ValidationStatus) {
	t.Helper()
	completed := attempt.StartedAt.Add(time.Millisecond)
	attempt.Status = status
	attempt.Duration = time.Millisecond
	attempt.CompletedAt = &completed
	attempt.HTTPStatus = 204
	if status == channel.ValidationFailed {
		attempt.ErrorCategory = channel.ErrorUpstream
		attempt.RawError = "upstream rejected request"
	}
	if err := store.CompleteValidation(ctx, attempt); err != nil {
		t.Fatal(err)
	}
}
