// Package dashboard 聚合工作台需要的跨领域只读视图。
//
// 待处理事项（Feature #160）由服务端从 API Key、渠道与 C2C 交易三个领域服务计算，
// 前端只负责展示。聚合不拥有任何持久化状态，也不修改任何领域数据。
package dashboard

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

// Kind 是待处理事项的种类，同时决定列表排序。
type Kind string

const (
	KindC2CRelease      Kind = "c2c_release"
	KindC2CPayment      Kind = "c2c_payment"
	KindChannelFailed   Kind = "channel_failed"
	KindChannelPaused   Kind = "channel_paused"
	KindRouteIneligible Kind = "route_ineligible"
	KindRouteSingle     Kind = "route_single"
)

// Tone 是展示强度。
type Tone string

const (
	ToneDanger  Tone = "danger"
	ToneWarning Tone = "warning"
	ToneInfo    Tone = "info"
)

// PendingItem 是一条待处理事项。Label、Title、Detail 为可直接展示的中文文案，
// To 为前端路由。
type PendingItem struct {
	ID     string
	Kind   Kind
	Label  string
	Tone   Tone
	Title  string
	Detail string
	To     string
}

// KeySource 提供当前账户的 API Key（含模型协议池）。
type KeySource interface {
	ListAPIKeys(context.Context, identity.Account) ([]gateway.APIKey, error)
}

// ChannelSource 提供当前账户自己共享的渠道。
type ChannelSource interface {
	ListMine(context.Context, identity.Account) ([]channel.Channel, error)
}

// TradeSource 提供当前账户参与的 C2C 订单与交易。
type TradeSource interface {
	MyActivity(context.Context, identity.Account) ([]c2c.Order, []c2c.Trade, error)
}

// Service 组合各领域服务计算待处理事项。
type Service struct {
	keys     KeySource
	channels ChannelSource
	trades   TradeSource
}

func New(keys KeySource, channels ChannelSource, trades TradeSource) *Service {
	return &Service{keys: keys, channels: channels, trades: trades}
}

// PendingItems 返回当前账户的待处理事项，按种类固定顺序、同类保持来源顺序。
// 任一来源失败则整体失败，不返回残缺列表。
func (s *Service) PendingItems(ctx context.Context, actor identity.Account) ([]PendingItem, error) {
	keys, err := s.keys.ListAPIKeys(ctx, actor)
	if err != nil {
		return nil, err
	}
	channels, err := s.channels.ListMine(ctx, actor)
	if err != nil {
		return nil, err
	}
	_, trades, err := s.trades.MyActivity(ctx, actor)
	if err != nil {
		return nil, err
	}
	return derive(actor.ID, keys, channels, trades), nil
}

var kindOrder = []Kind{
	KindC2CRelease, KindC2CPayment, KindChannelFailed, KindChannelPaused, KindRouteIneligible, KindRouteSingle,
}

func derive(accountID string, keys []gateway.APIKey, channels []channel.Channel, trades []c2c.Trade) []PendingItem {
	byKind := make(map[Kind][]PendingItem, len(kindOrder))
	add := func(item PendingItem) { byKind[item.Kind] = append(byKind[item.Kind], item) }

	// C2C 待办只取决于交易的买卖双方身份与状态，与订单方向无关。
	for _, trade := range trades {
		fiat := fmt.Sprintf("¥%.2f", float64(trade.FiatAmountFen)/100)
		switch {
		case trade.Status == c2c.TradePaid && trade.SellerAccountID == accountID:
			add(PendingItem{
				ID: "c2c-release-" + trade.ID, Kind: KindC2CRelease, Label: "待放行", Tone: ToneWarning,
				Title:  fmt.Sprintf("%s 已付款 %s", trade.BuyerDisplayName, fiat),
				Detail: fmt.Sprintf("确认收款后放行 %s 积分", trade.Quantity.String()),
				To:     "/c2c/trades/" + trade.ID,
			})
		case trade.Status == c2c.TradeAwaitingPayment && trade.BuyerAccountID == accountID:
			add(PendingItem{
				ID: "c2c-payment-" + trade.ID, Kind: KindC2CPayment, Label: "待付款", Tone: ToneWarning,
				Title:  fmt.Sprintf("向 %s 付款 %s", trade.SellerDisplayName, fiat),
				Detail: fmt.Sprintf("付款后标记已付款，买入 %s 积分", trade.Quantity.String()),
				To:     "/c2c/trades/" + trade.ID,
			})
		}
	}

	for _, item := range channels {
		if item.Status == channel.StatusPaused {
			add(PendingItem{
				ID: "channel-paused-" + item.ID, Kind: KindChannelPaused, Label: "已暂停", Tone: ToneWarning,
				Title: item.DisplayName, Detail: "渠道已暂停，暂时不会被调用", To: "/channels/" + item.ID,
			})
		}
		if item.Status == channel.StatusDeleted {
			continue
		}
		var failed []string
		for _, offer := range item.Offers {
			if offer.Status != channel.OfferDeleted && offer.LatestValidation != nil &&
				offer.LatestValidation.Status == channel.ValidationFailed {
				failed = append(failed, offer.ModelName+" · "+protocolLabel(offer.Protocol))
			}
		}
		if len(failed) > 0 {
			add(PendingItem{
				ID: "channel-failed-" + item.ID, Kind: KindChannelFailed, Label: "校验失败", Tone: ToneDanger,
				Title: item.DisplayName, Detail: strings.Join(failed, "、"), To: "/channels/" + item.ID,
			})
		}
	}

	for _, key := range keys {
		if key.Status != gateway.KeyActive {
			continue
		}
		for _, pool := range key.Pools {
			route := pool.ModelName + " · " + protocolLabel(pool.Protocol)
			for _, member := range pool.Members {
				if member.Eligible {
					continue
				}
				add(PendingItem{
					ID:   fmt.Sprintf("route-ineligible-%s-%s-%s", key.ID, pool.ID, member.OfferID),
					Kind: KindRouteIneligible, Label: "需更新", Tone: ToneDanger,
					Title: member.ChannelDisplayName + " 暂不可用", Detail: key.DisplayName + " · " + route,
					To: "/keys/" + key.ID,
				})
			}
			if len(pool.Members) == 1 {
				add(PendingItem{
					ID:   fmt.Sprintf("route-single-%s-%s", key.ID, pool.ID),
					Kind: KindRouteSingle, Label: "单渠道", Tone: ToneInfo,
					Title:  route,
					Detail: fmt.Sprintf("%s · 只有 %s，没有备用", key.DisplayName, pool.Members[0].ChannelDisplayName),
					To:     "/market?model=" + url.QueryEscape(pool.CanonicalModelID) + "&protocol=" + string(pool.Protocol),
				})
			}
		}
	}

	items := make([]PendingItem, 0)
	for _, kind := range kindOrder {
		items = append(items, byKind[kind]...)
	}
	return items
}

func protocolLabel(protocol channel.Protocol) string {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		return "OpenAI Chat Completions"
	case channel.ProtocolOpenAIResponse:
		return "OpenAI Responses"
	case channel.ProtocolAnthropic:
		return "Anthropic Messages"
	case channel.ProtocolGemini:
		return "Gemini GenerateContent"
	}
	return string(protocol)
}
