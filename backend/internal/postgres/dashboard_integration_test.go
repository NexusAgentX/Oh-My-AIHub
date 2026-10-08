package postgres_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/dashboard"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// TestDashboardPendingItemsIntegration 用真实领域服务与 PostgreSQL 覆盖六种待处理事项，
// 并验证事项只属于当前账户。
func TestDashboardPendingItemsIntegration(t *testing.T) {
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
	schema := "dashboard_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)) })
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
	consumer := inviteGatewayAccount(t, ctx, identityService, admin, "dash.consumer", "看板消费者", money.Amount(100*money.Scale))
	providerOne := inviteGatewayAccount(t, ctx, identityService, admin, "dash.provider.one", "共享者甲", money.Amount(10*money.Scale))
	providerTwo := inviteGatewayAccount(t, ctx, identityService, admin, "dash.provider.two", "共享者乙", money.Amount(10*money.Scale))
	seller := inviteGatewayAccount(t, ctx, identityService, admin, "dash.seller", "卖家", money.Amount(0))
	buyer := inviteGatewayAccount(t, ctx, identityService, admin, "dash.buyer", "买家", money.Amount(0))
	outsider := inviteGatewayAccount(t, ctx, identityService, admin, "dash.outsider", "无关用户", money.Amount(0))

	model, err := catalog.NewService(store).Create(ctx, admin, catalog.Model{
		ID: "test/dashboard-model", Name: "Dashboard model", Provider: "Test", ContextWindow: 1000,
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
	gatewayService, err := gateway.NewService(store, channelService)
	if err != nil {
		t.Fatal(err)
	}
	c2cKeyring, err := c2c.ParseKeyring("test="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "test")
	if err != nil {
		t.Fatal(err)
	}
	c2cService, err := c2c.NewServiceWithClock(store, c2cKeyring, func() time.Time { return time.Now() })
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.New(gatewayService, channelService, c2cService)
	pending := func(actor identity.Account) []dashboard.PendingItem {
		t.Helper()
		items, err := service.PendingItems(ctx, actor)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	kindsOf := func(items []dashboard.PendingItem) []dashboard.Kind {
		result := []dashboard.Kind{}
		for _, item := range items {
			result = append(result, item.Kind)
		}
		return result
	}

	for _, actor := range []identity.Account{consumer, providerOne, seller, outsider} {
		if items := pending(actor); len(items) != 0 {
			t.Fatalf("fresh account %s has pending items: %+v", actor.Username, items)
		}
	}

	// 路由：单渠道，渠道随后因 upstream 模型变化而使校验版本失效。
	offer := createGatewayOffer(t, ctx, store, channelService, providerOne, "Relay one", "https://dashboard-one.example", "upstream-secret-one", model.ID, "vendor-one")
	key, err := gatewayService.CreateAPIKey(ctx, consumer, gateway.KeyConfigInput{
		DisplayName: "看板 Key",
		Pools:       []gateway.PoolInput{{CanonicalModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, OfferIDs: []string{offer.ID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := pending(consumer)
	if got, want := kindsOf(items), []dashboard.Kind{dashboard.KindRouteSingle}; !reflect.DeepEqual(got, want) {
		t.Fatalf("single-channel route kinds = %v, want %v (%+v)", got, want, items)
	}
	if items[0].To != "/market?model=test%2Fdashboard-model&protocol=openai_chat_completions" {
		t.Fatalf("single-channel link = %q", items[0].To)
	}
	updated, err := channelService.UpdateOffer(ctx, providerOne, offer.ID, offer.Version, "vendor-one-v2", money.FromNano(money.Scale))
	if err != nil {
		t.Fatal(err)
	}
	passValidation(t, ctx, store, providerOne, updated.ID)
	if got, want := kindsOf(pending(consumer)), []dashboard.Kind{dashboard.KindRouteIneligible, dashboard.KindRouteSingle}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stale member kinds = %v, want %v", got, want)
	}
	// 事项只属于当前账户。
	if items := pending(outsider); len(items) != 0 {
		t.Fatalf("outsider sees consumer items: %+v", items)
	}
	// 停用 Key 后路由事项消失。
	if _, err := gatewayService.SetAPIKeyStatus(ctx, consumer, key.APIKey.ID, key.APIKey.Version, gateway.KeyDisabled); err != nil {
		t.Fatal(err)
	}
	if items := pending(consumer); len(items) != 0 {
		t.Fatalf("disabled key still reports route items: %+v", items)
	}

	// 渠道：暂停。
	owned, err := channelService.GetMine(ctx, providerOne, offer.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := channelService.SetStatus(ctx, providerOne, owned.ID, owned.Version, channel.StatusPaused, ""); err != nil {
		t.Fatal(err)
	}
	items = pending(providerOne)
	if got, want := kindsOf(items), []dashboard.Kind{dashboard.KindChannelPaused}; !reflect.DeepEqual(got, want) || items[0].To != "/channels/"+owned.ID {
		t.Fatalf("paused kinds = %v (%+v)", got, items)
	}

	// 渠道：校验失败。
	failed, err := channelService.Create(ctx, providerTwo, "Broken relay", "https://dashboard-two.example", "upstream-secret-two", []channel.OfferInput{{
		ModelID: model.ID, Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: "vendor-two", Multiplier: money.FromNano(money.Scale),
	}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.StartValidation(ctx, providerTwo, failed.Offers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	completeAttempt(t, ctx, store, target.Attempt, channel.ValidationFailed)
	items = pending(providerTwo)
	if got, want := kindsOf(items), []dashboard.Kind{dashboard.KindChannelFailed}; !reflect.DeepEqual(got, want) ||
		items[0].Detail != "Dashboard model · OpenAI Chat Completions" {
		t.Fatalf("failed kinds = %v (%+v)", got, items)
	}

	// C2C：待付款 -> 待放行。只依赖交易买卖双方与状态。
	ledgerService := ledger.NewService(store)
	if _, err := ledgerService.Transfer(ctx, "fund-dash-seller", consumer.ID, seller.ID, money.FromNano(5*money.Scale), "fund dashboard seller", "test_funding", "dash-seller"); err != nil {
		t.Fatal(err)
	}
	method := []c2c.PaymentMethodInput{{Type: c2c.PaymentWeChat, Contact: "wx-private", Instructions: "pay exact amount"}}
	order, err := c2cService.CreateOrder(ctx, seller, "dash-sell", 100, money.FromNano(5*money.Scale), money.FromNano(2*money.Scale), money.FromNano(5*money.Scale), method)
	if err != nil {
		t.Fatal(err)
	}
	trade, err := c2cService.TakeOrder(ctx, buyer, "dash-take", order.ID, money.FromNano(3*money.Scale), order.PaymentMethods[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	items = pending(buyer)
	if got, want := kindsOf(items), []dashboard.Kind{dashboard.KindC2CPayment}; !reflect.DeepEqual(got, want) || items[0].To != "/c2c/trades/"+trade.ID {
		t.Fatalf("buyer awaiting-payment items = %+v", items)
	}
	if items := pending(seller); len(items) != 0 {
		t.Fatalf("seller sees buyer's payment item: %+v", items)
	}
	if _, err := c2cService.MarkPaid(ctx, buyer, "dash-paid", trade.ID, "wx-transaction"); err != nil {
		t.Fatal(err)
	}
	items = pending(seller)
	if got, want := kindsOf(items), []dashboard.Kind{dashboard.KindC2CRelease}; !reflect.DeepEqual(got, want) || items[0].To != "/c2c/trades/"+trade.ID {
		t.Fatalf("seller awaiting-release items = %+v", items)
	}
	if items := pending(buyer); len(items) != 0 {
		t.Fatalf("buyer still has item after paying: %+v", items)
	}
	if _, err := c2cService.ConfirmReceipt(ctx, seller, "dash-release", trade.ID); err != nil {
		t.Fatal(err)
	}
	if items := pending(seller); len(items) != 0 {
		t.Fatalf("seller still has item after release: %+v", items)
	}
}
