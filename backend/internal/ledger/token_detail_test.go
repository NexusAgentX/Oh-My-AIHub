package ledger

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"testing"
	"time"
)

func TestDetailedPricesAreDisjointAndRoundOnce(t *testing.T) {
	base := Prices{InputPerMillion: money.FromNano(1), CacheWritePerMillion: money.FromNano(3), TokenPrices: map[string]money.Amount{"input_audio": 0, "cache_write_5m": 2, "cache_write_1h": 5}}
	usage := Usage{InputTokens: 2, CacheWriteTokens: 3, Detail: &UsageDetail{Tokens: map[string]int64{"input_audio": 1, "cache_write_5m": 1, "cache_write_1h": 1}}}
	got, err := CalculatePriceV2(usage, base, nil, time.Now(), FixedPointScale, 0)
	if err != nil || got.Cost != 1 {
		t.Fatalf("one rounding = %+v %v", got, err)
	}
	usage.InputTokens = 1_000_000
	usage.CacheWriteTokens = 3_000_000
	usage.Detail.Tokens = map[string]int64{"input_audio": 500_000, "cache_write_5m": 1_000_000, "cache_write_1h": 1_000_000}
	got, err = CalculatePriceV2(usage, base, nil, time.Now(), FixedPointScale, 0)
	if err != nil || got.Cost != 11 {
		t.Fatalf("zero override and residual = %+v %v", got, err)
	}
	usage.Detail.Tokens["input_image"] = 600_000
	if _, err = CalculatePriceV2(usage, base, nil, time.Now(), FixedPointScale, 0); err == nil {
		t.Fatal("overlapping/overlarge breakdown accepted")
	}
}
func TestResponseFactsSelectTierAndInheritWinningGenericPrice(t *testing.T) {
	tiers := []PriceTier{{ServiceTier: "openai:priority", InputPrice: 3}, {ServiceTier: "openai:default", InputPrice: 2}, {ThinkingMode: "qwen_thinking", InputPrice: 4}}
	usage := Usage{InputTokens: 1_000_000, Detail: &UsageDetail{ServiceTier: "openai:default", Tokens: map[string]int64{"input_audio": 1_000_000}}}
	got, err := CalculatePriceV2(usage, Prices{InputPerMillion: 1, TokenPrices: map[string]money.Amount{"input_audio": 0}}, tiers, time.Now(), FixedPointScale, 0)
	if err != nil || got.Cost != 2 || got.TierSeq != 2 {
		t.Fatalf("tier inheritance %+v %v", got, err)
	}
	usage.Detail.ServiceTier = ""
	usage.Detail.ThinkingMode = "qwen_thinking"
	got, _ = CalculatePriceV2(usage, Prices{}, tiers, time.Now(), FixedPointScale, 0)
	if got.TierSeq != 3 {
		t.Fatal(got)
	}
	usage.Detail.ThinkingMode = ""
	got, _ = CalculatePriceV2(usage, Prices{}, tiers, time.Now(), FixedPointScale, 0)
	if got.TierSeq != 0 {
		t.Fatal("unknown matched", got)
	}
}
