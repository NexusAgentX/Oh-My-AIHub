package typical

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

func amount(t *testing.T, s string) money.Amount {
	t.Helper()
	a, e := money.Parse(s)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func fixture(t *testing.T) (Input, Config, []Sample, time.Time) {
	t.Helper()
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	qa, qb := "qa", "qb"
	in := Input{USDCNYNano: 2 * ledger.UnitRate, FeeRateNano: 10_000_000, Directory: market.Directory{Suppliers: []market.Supplier{{ID: "s", Kind: "official_api", Enabled: true}}, Groups: []market.Group{{ID: "a", SupplierID: "s", Enabled: true}, {ID: "b", SupplierID: "s", Enabled: true}}, Models: []market.GroupModel{{ID: "ma", GroupID: "a", ModelID: "m", ModelType: "text", Enabled: true, Revision: 1, QuoteID: &qa, IsReference: true}, {ID: "mb", GroupID: "b", ModelID: "m", ModelType: "text", Enabled: true, Revision: 1, QuoteID: &qb}}, Quotes: []market.Quote{{ID: qa, GroupModelID: "ma", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: amount(t, "2"), Output: amount(t, "4"), CacheRead: amount(t, "0.2")}}, {ID: qb, GroupModelID: "mb", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: amount(t, "1"), Output: amount(t, "2"), CacheRead: amount(t, "0.1")}}}}, Channels: []Channel{{ID: "c1", GroupID: "b", ModelID: "m", DiscountNano: 400_000_000}, {ID: "c2", GroupID: "b", ModelID: "m", DiscountNano: 800_000_000}}}
	c := DefaultConfig()
	c.MinSamples = 1
	samples := []Sample{{ID: "call-a", GroupID: "a", ModelID: "m", At: at, CompletedAt: at, References: []quotepricing.Reference{{ModelID: "m", GroupModelID: "ma", QuoteID: qa, QuoteVersion: 1}}, Facts: quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 90, OutputTokens: 10}, Units: []quotepricing.Unit{{Unit: "request", QuantityNano: ledger.UnitRate}}}}, {ID: "call-b", GroupID: "b", ModelID: "m", At: at, CompletedAt: at, References: []quotepricing.Reference{{ModelID: "m", GroupModelID: "mb", QuoteID: qb, QuoteVersion: 1}}, Facts: quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 10, OutputTokens: 10, CacheReadTokens: 80}, Units: []quotepricing.Unit{{Unit: "request", QuantityNano: ledger.UnitRate}}}}}
	return in, c, samples, at
}
func row(t *testing.T, s *Snapshot, level, id string) Estimate {
	t.Helper()
	for _, r := range s.Items {
		if r.Level == level && (level == "model" && r.ModelID == id || level == "group" && r.GroupID == id || level == "channel" && r.ChannelID == id) {
			return r
		}
	}
	t.Fatal("missing", level, id)
	return Estimate{}
}
func str(v *string) string {
	if v == nil {
		return "<unknown>"
	}
	return *v
}
func TestGroupSharedWorkloadDiscountAndReferenceUseSameDistribution(t *testing.T) {
	in, c, samples, at := fixture(t)
	snap, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ level, id, cost, ref, ratio string }{{"group", "a", "2.2", "2.2", "1.000000000"}, {"group", "b", "0.38", "0.76", "0.500000000"}, {"channel", "c1", "0.152", "0.76", "0.200000000"}, {"channel", "c2", "0.304", "0.76", "0.400000000"}, {"model", "m", "1.29", "1.48", "0.871621622"}} {
		r := row(t, snap, tc.level, tc.id)
		if str(r.Cost) != tc.cost || str(r.ReferenceCost) != tc.ref || str(r.Ratio) != tc.ratio || r.Unit != "tokens" || r.Quantity != 1_000_000 {
			t.Fatalf("%s/%s: %+v", tc.level, tc.id, r)
		}
	}
	a, b := row(t, snap, "group", "a"), row(t, snap, "group", "b")
	if str(a.CacheReadShare) != "0.000000000" || str(b.CacheReadShare) != "0.888888889" {
		t.Fatal("cache structures were averaged", a, b)
	}
	one, two := row(t, snap, "channel", "c1"), row(t, snap, "channel", "c2")
	x, _ := json.Marshal(one.Workload)
	y, _ := json.Marshal(two.Workload)
	if string(x) != string(y) || one.Samples != two.Samples {
		t.Fatal("per-channel cache profile created")
	}
}
func TestQuoteVersionColdStartFXAndZeroReference(t *testing.T) {
	in, c, samples, at := fixture(t)
	old, _ := json.Marshal(samples)
	in.Directory.Quotes[1].Version = 2
	in.Directory.Quotes[1].Prices.CacheRead = amount(t, "0.2")
	snap, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	b := row(t, snap, "group", "b")
	if b.OwnSamples != 0 || b.Samples != 2 || b.Source != "model_site_fallback" || str(b.Cost) != "0.78" {
		t.Fatal("old quote polluted current group profile", b)
	}
	now, _ := json.Marshal(samples)
	if string(now) != string(old) {
		t.Fatal("historical samples were rewritten")
	}
	in.Directory.Quotes[1].Currency = "USD"
	usd, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	if str(row(t, usd, "group", "b").Cost) != "1.56" || usd.InputVersion == snap.InputVersion {
		t.Fatal("FX not applied/versioned")
	}
	in.Directory.Quotes[0].Prices = market.TokenPrices{}
	zero, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	b = row(t, zero, "group", "b")
	if b.Ratio != nil || str(b.ReferenceCost) != "0" || b.Reason != "zero_reference" {
		t.Fatal("zero reference divided", b)
	}
}
func TestMixedRequestTokenQuoteRequiresMeasuredConversion(t *testing.T) {
	in, c, samples, at := fixture(t)
	in.Directory.Quotes[1].BillingUnit = "per_request"
	in.Directory.Quotes[1].Prices = market.TokenPrices{}
	in.Directory.Quotes[1].UnitPrices = []market.UnitPrice{{Unit: "request", Price: amount(t, "0.01")}}
	snap, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	if str(row(t, snap, "group", "b").Cost) != "100" {
		t.Fatal("request/token conversion lost measured requests")
	}
	for i := range samples {
		samples[i].Facts.Usage = ledger.Usage{}
	}
	snap, err = Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	b := row(t, snap, "group", "b")
	if b.Cost != nil || b.State != "unknown" || b.Reason != "missing_reliable_conversion" || str(b.RequestCost) != "1" {
		t.Fatal("unmeasured conversion invented", b)
	}
}
func TestConditionalModalAndToolPricesRetainCallDistribution(t *testing.T) {
	in, c, samples, at := fixture(t)
	threshold := int64(50)
	in.Directory.Quotes[1].Prices.Details = map[string]money.Amount{"input_audio": amount(t, "5")}
	in.Directory.Quotes[1].Tiers = []market.Tier{{Name: "long-priority", Conditions: market.Conditions{ServiceTier: "openai:fast", MinPromptTokens: &threshold}, Prices: market.TokenPrices{Input: amount(t, "10"), Output: amount(t, "20"), CacheRead: amount(t, "1"), Details: map[string]money.Amount{"input_audio": amount(t, "50")}}}}
	in.Directory.Quotes[1].UnitPrices = []market.UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: amount(t, "0.2")}}
	s := samples[1]
	s.Facts.Usage.Detail = &ledger.UsageDetail{ServiceTier: "openai:priority", Tokens: map[string]int64{"input_audio": 5}}
	s.Facts.Units = append(s.Facts.Units, quotepricing.Unit{Unit: "tool_call", Tool: "web_search", QuantityNano: ledger.UnitRate})
	samples = []Sample{s}
	snap, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	b := row(t, snap, "group", "b")
	// 5*10 + 5*50 + 10*20 + 80*1 = 580 nano-token units;
	// plus 0.2 search, all normalized from 100 measured tokens to one million.
	if str(b.Cost) != "2005.8" || b.Workload.ServiceTiers["openai:priority"] != 1 || b.Workload.Tokens["input_audio"] != 5 {
		t.Fatal("conditional/modal/tool facts flattened", b)
	}
	if b.Ratio != nil || b.Reason != "reference_workload_unpriced" {
		t.Fatal("missing reference tool tariff guessed", b)
	}
}
func TestNoSamplesReferenceAndCancellationRemainExplicit(t *testing.T) {
	in, c, samples, at := fixture(t)
	snap, err := Calculate(t.Context(), in, c, nil, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Items {
		if r.Cost != nil || r.Ratio != nil || r.State != "unknown" {
			t.Fatal("empty model was free", r)
		}
	}
	in.Directory.Models[0].IsReference = false
	snap, err = Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, snap, "group", "b"); r.Cost == nil || r.Ratio != nil || r.Reason != "no_reference" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = Calculate(ctx, in, c, samples, at); err == nil {
		t.Fatal("canceled estimate published")
	}
}
func TestDecodeKeepsReliableUsageAndDiscardsContentAndNetCost(t *testing.T) {
	in, _, _, at := fixture(t)
	book, _ := quotepricing.Freeze(quotepricing.Book{GroupID: "a", GroupRevision: 1, Offers: map[string]quotepricing.Offer{"m": {ModelID: "m", Revision: 1, Quote: in.Directory.Quotes[0]}}})
	usage := ledger.Usage{InputTokens: 10, OutputTokens: 5, Detail: &ledger.UsageDetail{ServiceTier: "openai:priority", Tokens: map[string]int64{"input_audio": 2}, Notes: []string{"secret prompt body"}}}
	statement, err := quotepricing.Calculate(book, "m", quotepricing.Facts{MainReliable: true, Usage: usage}, at, 0, ledger.UnitRate, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{"group_quote": statement, "detail": usage.Detail, "unrelated": "secret prompt body"})
	raw := RawCall{ID: "c", ModelID: "m", Outcome: "succeeded", Format: "openai_responses", StartedAt: at, CompletedAt: at, Usage: usage, UsageStatus: "reported", Snapshot: encoded}
	sample, ok := Decode(raw)
	if !ok {
		t.Fatal("own channel zero net price lost usage")
	}
	saved, _ := json.Marshal(sample)
	if strings.Contains(string(saved), "secret") || strings.Contains(string(saved), "discount") || sample.Facts.Usage.InputTokens != 10 {
		t.Fatal("body/discount retained or usage lost", string(saved))
	}
	raw.Outcome = "upstream_failed"
	if _, ok = Decode(raw); ok {
		t.Fatal("failed call polluted profile")
	}
	raw.Outcome = "succeeded_unbilled"
	if _, ok = Decode(raw); ok {
		t.Fatal("missing reliable usage polluted profile")
	}
}
