package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// 响应结构体必须保持线上格式：规范要求存在的字段始终输出（含 null），空列表是 [] 而不是 null，
// 金额是十进制字符串。门禁只校验被流程测试走到的数据；这里直接覆盖零值与空值的边界。
func TestResponseTypesKeepNullAndEmptyShapes(t *testing.T) {
	emptyList, emptyObject := []any{}, map[string]any{}
	cases := []struct {
		name  string
		value any
		want  map[string]any
	}{
		{"admin account without activity", newAdminAccountJSON(identity.AdminAccount{
			Account: identity.Account{CreditLimit: money.FromNano(5 * money.Scale)}, Balance: money.FromNano(-money.Scale / 2),
		}), map[string]any{
			"credit_limit": "5", "balance": "-0.5", "available": "4.5",
			"password_changed_at": nil, "last_active_at": nil,
		}},
		{"settings without extra hosts", newSettingsJSON(settings.Settings{DefaultCreditLimit: money.FromNano(money.Scale)}), map[string]any{
			"default_credit_limit": "1", "extra_blocked_hosts": emptyList,
		}},
		{"audit entry without actor or detail", auditEntryResponse(audit.Entry{ID: 7}), map[string]any{
			"id": "7", "actor": nil, "detail": emptyObject,
		}},
		{"unconfigured routing preference", newRoutingPreferenceJSON(routing.Pref{}), map[string]any{
			"order": emptyList, "excluded": emptyList, "max_attempts": nil, "ttft_timeout_ms": nil, "updated_at": nil,
		}},
		{"model without tiers or prices", newAdminModelJSON(catalog.Model{}), map[string]any{
			"price_tiers": emptyList, "context_window": nil,
			"base_prices": map[string]any{
				"input": "0", "output": "0", "cache_write": "0", "cache_read": "0", "token_prices": emptyObject,
			},
		}},
		{"price tier without weekday predicate", newPriceTierJSON(1, ledger.PriceTier{Weekdays: []int{}}), map[string]any{
			"seq": float64(1), "min_prompt_tokens": nil, "max_prompt_tokens": nil, "weekdays": nil,
			"start_minute_of_day": nil, "end_minute_of_day": nil,
		}},
		{"unlimited key", newApiKeyJSON(apikey.Key{CreatedAt: time.Unix(0, 0).UTC(), Spend: apikey.Spend{Today: money.FromNano(money.Scale / 4)}}), map[string]any{
			"expires_at": nil, "last_used_at": nil, "budget_daily": nil, "budget_monthly": nil, "budget_total": nil,
			"spend": map[string]any{"today": "0.25", "month": "0", "total": "0"},
		}},
		{"empty account page", adminAccountPageJSON{Items: []adminAccountJSON{}, NextCursor: nextCursor(false, "")}, map[string]any{
			"items": emptyList, "next_cursor": nil,
		}},
		{"nullable amount in map based responses", map[string]any{"max": nullableAmount(nil), "min": nullableAmount(new(money.Amount))}, map[string]any{
			"max": nil, "min": "0",
		}},
		{"market order without limits or payment channels", newC2cOrderJSON(c2c.MarketOrder{}), map[string]any{
			"available": "0", "min_per_trade": "0", "max_per_trade": nil, "payment_channels": emptyList,
		}},
		{"my order without methods", newC2cMyOrderJSON(c2c.MyOrder{}), map[string]any{
			"total": "0", "max_per_trade": nil, "payment_methods": emptyList, "closed_at": nil,
		}},
		{"trade without optional fields", newC2cTradeJSON(c2c.TradeView{}), map[string]any{
			"amount": "0", "payment_methods": emptyList, "buyer_note": nil, "dispute_opened_by": nil, "buyer_statement": nil,
			"seller_statement": nil, "resolution_reason": nil, "paid_at": nil, "released_at": nil, "cancelled_at": nil,
			"disputed_at": nil, "resolved_at": nil,
		}},
		{"points entry without related record or key", newPointsEntryJSON(ledger.EntryView{ID: 12}), map[string]any{
			"id": "12", "amount": "0", "related": nil, "api_key": nil,
		}},
		{"points entry whose key has no name", newPointsEntryJSON(ledger.EntryView{APIKeyID: new(string)}), map[string]any{
			"api_key": map[string]any{"id": "", "name": ""},
		}},
		{"empty points summary", newPointsEntrySummaryJSON(observe.EntrySummary{}), map[string]any{
			"by_day": emptyList, "by_key": emptyList,
		}},
		{"points summary of calls without a key", newPointsEntrySummaryJSON(observe.EntrySummary{ByKey: []observe.KeySpend{{Spend: money.FromNano(money.Scale), Entries: 2}}}), map[string]any{
			"by_key": []any{map[string]any{"api_key": nil, "spend": "1", "entries": float64(2)}},
		}},
		{"model without online channels", newCatalogModelJSON(catalog.Model{}, nil, time.Unix(0, 0)), map[string]any{
			"context_window": nil, "current_tier": nil, "lowest_prices": nil, "formats": emptyList, "online_channels": float64(0),
		}},
		{"call that has not finished", newRecentCallJSON(gateway.CallSummary{Cost: 3, Fee: 1}), map[string]any{
			"completed_at": nil, "model_id": nil, "tag": nil, "api_key": nil, "channel": nil, "ttft_ms": nil, "duration_ms": nil,
			"cost": "0.000000003", "charged": "0",
		}},
		{"channel without models or overrides", (&app{gateway: gateway.NewEngine(gateway.Dependencies{})}).newChannelJSON(channel.Channel{}, nil, time.Unix(0, 0)), map[string]any{
			"suspended_reason": nil, "cooldown_until": nil, "models": emptyList,
			"advanced": map[string]any{
				"user_agent": nil, "header_rules": map[string]any{"set": emptyList, "remove": emptyList}, "concurrency_limit": nil, "rpm_limit": nil,
				"daily_revenue_cap": nil, "ttft_timeout_ms": nil, "total_timeout_ms": nil, "cooldown_failures": nil, "cooldown_seconds": nil,
			},
			"today": map[string]any{"revenue": "0", "calls": float64(0), "success_rate": nil},
		}},
		{"channel model without recorded format tests", (&app{gateway: gateway.NewEngine(gateway.Dependencies{})}).newChannelJSON(
			channel.Channel{Models: []channel.Model{{ModelID: "m", Formats: []channel.Format{channel.FormatGemini}}}}, nil, time.Unix(0, 0),
		), map[string]any{
			"models": []any{map[string]any{
				"model_id": "m", "display_name": "", "upstream_model": "", "multiplier": "0", "formats": []any{"gemini"}, "format_tests": emptyObject,
				"enabled": false, "current_prices": map[string]any{"input": "0", "output": "0", "cache_write": "0", "cache_read": "0"},
			}},
		}},
		{"channel format test with nothing recorded", newFormatTestsJSON(map[channel.Format]channel.FormatTest{channel.FormatGemini: {}})["gemini"], map[string]any{
			"ok": false, "status_code": nil, "error": nil, "duration_ms": nil,
		}},
		{"discovered model without a catalog match", discoveredModelJSON{ID: "x"}, map[string]any{
			"matched_model_id": nil,
		}},
		{"empty cursor page", pageJSON[c2cOrderJSON]{Items: []c2cOrderJSON{}}, map[string]any{
			"items": emptyList, "next_cursor": nil,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				value, present := got[key]
				if !present {
					t.Errorf("%s is missing from %s", key, raw)
					continue
				}
				if !reflect.DeepEqual(value, want) {
					t.Errorf("%s = %#v, want %#v (in %s)", key, value, want, raw)
				}
			}
		})
	}
}

// summary 只在按天或按 Key 分组时出现；省略字段与 null 是两回事。
func TestPointsEntryPageOmitsSummaryUnlessGrouped(t *testing.T) {
	ungrouped, err := json.Marshal(pointsEntryPageJSON{Items: []pointsEntryJSON{}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(ungrouped, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, present := decoded["summary"]; present || decoded["next_cursor"] != nil || len(decoded) != 2 {
		t.Fatalf("ungrouped page = %s", ungrouped)
	}
	summary := newPointsEntrySummaryJSON(observe.EntrySummary{})
	grouped, err := json.Marshal(pointsEntryPageJSON{Items: []pointsEntryJSON{}, Summary: &summary})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(grouped); got != `{"items":[],"next_cursor":null,"summary":{"by_day":[],"by_key":[]}}` {
		t.Fatalf("grouped page = %s", got)
	}
}

// writePage 把领域分页转换成响应分页：没有下一页时 next_cursor 为 null，空页的 items 是 []。
func TestWritePageKeepsEmptyItemsAndCursor(t *testing.T) {
	for name, tc := range map[string]struct {
		page c2c.Page[c2c.MarketOrder]
		want string
	}{
		"empty last page":          {c2c.Page[c2c.MarketOrder]{}, `{"items":[],"next_cursor":null}`},
		"empty page with a cursor": {c2c.Page[c2c.MarketOrder]{Next: "n"}, `{"items":[],"next_cursor":"n"}`},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writePage(recorder, tc.page, newC2cOrderJSON)
			if got := recorder.Body.String(); got != tc.want+"\n" {
				t.Fatalf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

// 管理员视角的渠道是在渠道上多一个 owner，其余字段原样展开。
func TestAdminChannelAddsOwnerToChannelFields(t *testing.T) {
	a := &app{gateway: gateway.NewEngine(gateway.Dependencies{})}
	item := channel.Channel{ID: "c1", Name: "渠道", Owner: channel.Owner{ID: "o1", Username: "owner", DisplayName: "车主"}}
	raw, err := json.Marshal(a.newAdminChannelJSON(item, nil, time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	owner, _ := decoded["owner"].(map[string]any)
	if decoded["id"] != "c1" || decoded["name"] != "渠道" || len(decoded) != 12 ||
		!reflect.DeepEqual(owner, map[string]any{"id": "o1", "username": "owner", "display_name": "车主"}) {
		t.Fatalf("admin channel = %s", raw)
	}
}
