package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
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
		{"unconfigured routing preference", routingResponse(routing.Pref{}), map[string]any{
			"order": emptyList, "excluded": emptyList, "max_attempts": nil, "ttft_timeout_ms": nil, "updated_at": nil,
		}},
		{"model without tiers or prices", newAdminModelJSON(catalog.Model{}), map[string]any{
			"price_tiers": emptyList, "context_window": nil,
			"base_prices": map[string]any{
				"input": "0", "output": "0", "cache_write": "0", "cache_read": "0", "token_prices": emptyObject,
			},
		}},
		{"price tier without weekday predicate", priceTierResponse(1, ledger.PriceTier{Weekdays: []int{}}), map[string]any{
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
