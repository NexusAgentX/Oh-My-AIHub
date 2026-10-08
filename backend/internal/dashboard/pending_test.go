package dashboard

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type fakeKeys struct {
	keys []gateway.APIKey
	err  error
}

func (f fakeKeys) ListAPIKeys(context.Context, identity.Account) ([]gateway.APIKey, error) {
	return f.keys, f.err
}

type fakeChannels struct {
	channels []channel.Channel
	err      error
}

func (f fakeChannels) ListMine(context.Context, identity.Account) ([]channel.Channel, error) {
	return f.channels, f.err
}

type fakeTrades struct {
	trades []c2c.Trade
	err    error
}

func (f fakeTrades) MyActivity(context.Context, identity.Account) ([]c2c.Order, []c2c.Trade, error) {
	return nil, f.trades, f.err
}

func ids(items []PendingItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func kinds(items []PendingItem) []Kind {
	result := make([]Kind, 0, len(items))
	for _, item := range items {
		result = append(result, item.Kind)
	}
	return result
}

func member(offer, name string, eligible bool) gateway.PoolMember {
	return gateway.PoolMember{OfferID: offer, ChannelID: "ch-" + offer, ChannelDisplayName: name, Eligible: eligible}
}

func pool(id string, members ...gateway.PoolMember) gateway.ModelPool {
	return gateway.ModelPool{
		ID: id, CanonicalModelID: "openai/gpt-5", ModelName: "GPT-5", Protocol: channel.ProtocolOpenAIResponse, Members: members,
	}
}

func TestC2CPendingItemsDependOnlyOnTradeParticipantsAndStatus(t *testing.T) {
	// 结果只取决于交易买卖双方身份与交易状态，测试不涉及订单方向。
	trades := []c2c.Trade{
		{ID: "t1", Status: c2c.TradePaid, SellerAccountID: "me", BuyerAccountID: "x", BuyerDisplayName: "买家", Quantity: money.FromNano(10 * money.Scale), FiatAmountFen: 1000},
		{ID: "t2", Status: c2c.TradePaid, SellerAccountID: "x", BuyerAccountID: "me", Quantity: money.FromNano(10 * money.Scale), FiatAmountFen: 1000},
		{ID: "t3", Status: c2c.TradeAwaitingPayment, SellerAccountID: "x", BuyerAccountID: "me", SellerDisplayName: "卖家", Quantity: money.FromNano(5 * money.Scale), FiatAmountFen: 505},
		{ID: "t4", Status: c2c.TradeAwaitingPayment, SellerAccountID: "me", BuyerAccountID: "x"},
		{ID: "t5", Status: c2c.TradePaid, SellerAccountID: "me", BuyerAccountID: "y", BuyerDisplayName: "另一位", Quantity: money.FromNano(money.Scale), FiatAmountFen: 100},
	}
	items := derive("me", nil, nil, trades)
	if got, want := ids(items), []string{"c2c-release-t1", "c2c-release-t5", "c2c-payment-t3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if items[0].To != "/c2c/trades/t1" || items[0].Title != "买家 已付款 ¥10.00" || items[0].Detail != "确认收款后放行 10 积分" {
		t.Fatalf("release item = %+v", items[0])
	}
	if items[2].Title != "向 卖家 付款 ¥5.05" || items[2].Tone != ToneWarning || items[2].Label != "待付款" {
		t.Fatalf("payment item = %+v", items[2])
	}
}

func TestChannelPendingItems(t *testing.T) {
	failedOffer := channel.Offer{Status: channel.OfferActive, ModelName: "GPT-5", Protocol: channel.ProtocolOpenAIResponse,
		LatestValidation: &channel.ValidationAttempt{Status: channel.ValidationFailed}}
	deletedFailed := channel.Offer{Status: channel.OfferDeleted, ModelName: "旧", Protocol: channel.ProtocolGemini,
		LatestValidation: &channel.ValidationAttempt{Status: channel.ValidationFailed}}
	passed := channel.Offer{Status: channel.OfferActive, LatestValidation: &channel.ValidationAttempt{Status: channel.ValidationPassed}}
	untested := channel.Offer{Status: channel.OfferActive}
	channels := []channel.Channel{
		{ID: "c1", DisplayName: "失败渠道", Status: channel.StatusPublished, Offers: []channel.Offer{failedOffer, deletedFailed}},
		{ID: "c2", DisplayName: "暂停渠道", Status: channel.StatusPaused},
		{ID: "c3", DisplayName: "正常", Status: channel.StatusPublished, Offers: []channel.Offer{passed, untested}},
		{ID: "c4", DisplayName: "已删除", Status: channel.StatusDeleted, Offers: []channel.Offer{failedOffer}},
		{ID: "c5", DisplayName: "暂停且失败", Status: channel.StatusPaused, Offers: []channel.Offer{failedOffer}},
	}
	items := derive("me", nil, channels, nil)
	if got, want := ids(items), []string{"channel-failed-c1", "channel-failed-c5", "channel-paused-c2", "channel-paused-c5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if items[0].Detail != "GPT-5 · OpenAI Responses" || items[0].Tone != ToneDanger || items[0].To != "/channels/c1" {
		t.Fatalf("failed item = %+v", items[0])
	}
	if items[2].Tone != ToneWarning || items[2].Label != "已暂停" {
		t.Fatalf("paused item = %+v", items[2])
	}
}

func TestRoutePendingItemsOnlyForActiveKeys(t *testing.T) {
	active := gateway.APIKey{ID: "k1", DisplayName: "主力", Status: gateway.KeyActive, Pools: []gateway.ModelPool{
		pool("p1", member("o1", "渠道一", true)),
		pool("p2", member("o2", "渠道二", true), member("o3", "渠道三", false)),
	}}
	disabled := gateway.APIKey{ID: "k2", DisplayName: "停用", Status: gateway.KeyDisabled, Pools: []gateway.ModelPool{pool("p9", member("o9", "渠道九", false))}}
	deleted := gateway.APIKey{ID: "k3", DisplayName: "已删", Status: gateway.KeyDeleted, Pools: []gateway.ModelPool{pool("p8", member("o8", "渠道八", true))}}
	items := derive("me", []gateway.APIKey{active, disabled, deleted}, nil, nil)
	if got, want := kinds(items), []Kind{KindRouteIneligible, KindRouteSingle}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if items[0].Title != "渠道三 暂不可用" || items[0].Detail != "主力 · GPT-5 · OpenAI Responses" || items[0].To != "/keys/k1" || items[0].Tone != ToneDanger {
		t.Fatalf("ineligible item = %+v", items[0])
	}
	if items[1].ID != "route-single-k1-p1" || items[1].Detail != "主力 · 只有 渠道一，没有备用" ||
		items[1].To != "/market?model=openai%2Fgpt-5&protocol=openai_responses" || items[1].Tone != ToneInfo {
		t.Fatalf("single item = %+v", items[1])
	}
	// 只有一个成员且不可用：两类事项同时出现。
	both := gateway.APIKey{ID: "k4", DisplayName: "单一失效", Status: gateway.KeyActive, Pools: []gateway.ModelPool{pool("p4", member("o4", "渠道四", false))}}
	if got, want := ids(derive("me", []gateway.APIKey{both}, nil, nil)), []string{"route-ineligible-k4-p4-o4", "route-single-k4-p4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestPendingItemsAreOrderedByKindAndNeverNil(t *testing.T) {
	service := New(
		fakeKeys{keys: []gateway.APIKey{{ID: "k1", DisplayName: "主力", Status: gateway.KeyActive, Pools: []gateway.ModelPool{pool("p1", member("o1", "渠道一", true))}}}},
		fakeChannels{channels: []channel.Channel{{ID: "c2", DisplayName: "暂停渠道", Status: channel.StatusPaused}}},
		fakeTrades{trades: []c2c.Trade{{ID: "t1", Status: c2c.TradeAwaitingPayment, BuyerAccountID: "me", SellerDisplayName: "卖家"}}},
	)
	items, err := service.PendingItems(context.Background(), identity.Account{ID: "me"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kinds(items), []Kind{KindC2CPayment, KindChannelPaused, KindRouteSingle}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	empty, err := New(fakeKeys{}, fakeChannels{}, fakeTrades{}).PendingItems(context.Background(), identity.Account{ID: "me"})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
}

func TestPendingItemsFailWhenAnySourceFails(t *testing.T) {
	boom := errors.New("boom")
	actor := identity.Account{ID: "me"}
	for name, service := range map[string]*Service{
		"keys":     New(fakeKeys{err: boom}, fakeChannels{}, fakeTrades{}),
		"channels": New(fakeKeys{}, fakeChannels{err: boom}, fakeTrades{}),
		"trades":   New(fakeKeys{}, fakeChannels{}, fakeTrades{err: boom}),
	} {
		if items, err := service.PendingItems(context.Background(), actor); !errors.Is(err, boom) || items != nil {
			t.Fatalf("%s: items = %v, err = %v", name, items, err)
		}
	}
}
