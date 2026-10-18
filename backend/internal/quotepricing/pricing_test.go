package quotepricing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func amount(t *testing.T, value string) money.Amount {
	t.Helper()
	a, err := money.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func offer(t *testing.T, id, currency, price string) Offer {
	return Offer{ModelID: id, Revision: 1, Quote: market.Quote{ID: "quote-" + id, GroupModelID: "gm-" + id, Version: 1, Currency: currency, BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: amount(t, price)}}}
}
func book(t *testing.T, offers ...Offer) *Book {
	t.Helper()
	b := Book{GroupID: "group", GroupRevision: 1, ConnectionVersion: 2, Offers: map[string]Offer{}}
	for _, o := range offers {
		b.Offers[o.ModelID] = o
	}
	f, err := Freeze(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestCandidateQuotesFXDiscountChildrenAndWholeTable(t *testing.T) {
	main := offer(t, "same-id", "USD", "2")
	main.Quote.UnitPrices = []market.UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: amount(t, "0.5")}}
	child := offer(t, "image-model", "CNY", "2")
	facts := Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{Iterations: []ledger.UsageIteration{{Type: "image_generation_call", Model: "image-model", Usage: ledger.Usage{InputTokens: 1_000_000}}}}}, Units: []Unit{{Unit: "tool_call", Tool: "web_search", QuantityNano: 2 * ledger.UnitRate}}}
	frozen := book(t, main, child)
	statement, err := Calculate(frozen, "same-id", facts, time.Unix(123, 0), ledger.UnitRate/2, 7*ledger.UnitRate, ledger.UnitRate/100)
	if err != nil || statement.Cost != amount(t, "11.5") || statement.Fee != amount(t, "0.115") {
		t.Fatalf("expected 7 main + 1 child + 3.5 tool: %+v %v", statement, err)
	}
	main.Quote.Prices.Input = amount(t, "999")
	if frozen.Offers["same-id"].Quote.Prices.Input != amount(t, "2") {
		t.Fatal("in-flight quote changed")
	}
	other := book(t, offer(t, "same-id", "CNY", "3"))
	second, err := Calculate(other, "same-id", Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000}}, statement.StartedAt, ledger.UnitRate, 99*ledger.UnitRate, 0)
	if err != nil || second.Cost != amount(t, "3") {
		t.Fatalf("candidate borrowed other group's USD price: %+v %v", second, err)
	}
}
func TestMissingFactsPreserveReliableParentAndOtherParts(t *testing.T) {
	b := book(t, offer(t, "main", "CNY", "2"))
	facts := Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{Iterations: []ledger.UsageIteration{{Type: "advisor_message", Model: "missing", Usage: ledger.Usage{InputTokens: 99}}}}}, Units: []Unit{{Unit: "tool_query", Tool: "search", QuantityNano: ledger.UnitRate}}}
	s, err := Calculate(b, "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "2") || len(s.Lines) != 3 || s.Lines[1].Reason != "missing_group_quote" || s.Lines[2].Reason != "missing_unit_price" {
		t.Fatalf("reliable parent discarded: %+v %v", s, err)
	}
	facts.MainReliable = false
	s, err = Calculate(b, "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != 0 || len(s.Lines) != 1 || s.Lines[0].Reason != "missing_main_usage" {
		t.Fatalf("child supplied missing main usage: %+v %v", s, err)
	}
}
func TestImagesRequestAndSuccessfulProductUnitsAndZero(t *testing.T) {
	for _, run := range []struct{ billing, unit, price, want string }{{"per_request", "request", "2", "2"}, {"per_successful_image", "successful_image", "2", "8"}, {"per_successful_image", "successful_image", "0", "0"}} {
		o := offer(t, "image", "CNY", "0")
		o.Quote.BillingUnit = run.billing
		o.Quote.UnitPrices = []market.UnitPrice{{Unit: run.unit, Price: amount(t, run.price)}}
		facts := Facts{IndependentImage: true, Units: []Unit{{Unit: "request", QuantityNano: ledger.UnitRate}, {Unit: "successful_image", QuantityNano: 4 * ledger.UnitRate}}}
		s, err := Calculate(book(t, o), "image", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
		if err != nil || s.Cost != amount(t, run.want) || len(s.Lines) != 1 || s.Lines[0].State != "priced" {
			t.Fatalf("%s: %+v %v", run.billing, s, err)
		}
	}
}
func TestUnitsSelectFactsNotRequestedTierAndCombineBeforeRounding(t *testing.T) {
	o := offer(t, "main", "CNY", "0")
	o.Quote.UnitPrices = []market.UnitPrice{{Unit: "tool_call", Tool: "search", Price: money.FromNano(1)}, {Unit: "tool_call", Tool: "search", ServiceTier: "openai:fast", Price: money.FromNano(10)}}
	facts := Facts{MainReliable: true, Usage: ledger.Usage{Detail: &ledger.UsageDetail{RequestedServiceTier: "openai:fast", ServiceTier: "openai:default"}}, Units: []Unit{{Unit: "tool_call", Tool: "search", QuantityNano: ledger.UnitRate / 10}, {Unit: "tool_call", Tool: "search", QuantityNano: ledger.UnitRate / 10}, {Unit: "tool_call", Tool: "search", QuantityNano: ledger.UnitRate / 10}}}
	s, err := Calculate(book(t, o), "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, ledger.UnitRate/2)
	if err != nil || s.Cost.Nano() != 1 || s.Fee.Nano() != 1 {
		t.Fatalf("components rounded before combining: %+v %v", s, err)
	}
}
func TestReceiptRepricingNeverDiscardsOtherChargesOrLeaksScope(t *testing.T) {
	o := offer(t, "main", "CNY", "2")
	o.Quote.UnitPrices = []market.UnitPrice{{Unit: "tool_session", Tool: "code", Price: amount(t, "3")}}
	key := strings.Repeat("a", 64)
	facts := Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000}, Units: []Unit{{Unit: "tool_session", Tool: "code", QuantityNano: ledger.UnitRate, ReceiptKey: key}, {Unit: "tool_session", Tool: "code", QuantityNano: ledger.UnitRate, ReceiptKey: key}}}
	s, err := Calculate(book(t, o), "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "5") || len(s.ReceiptKeys()) != 1 {
		t.Fatalf("repeated event billed: %+v %v", s, err)
	}
	repriced, err := s.Reprice(map[string]bool{key: true})
	if err != nil || repriced.Cost != amount(t, "2") || s.Cost != amount(t, "5") || repriced.Lines[1].State != "duplicate" {
		t.Fatalf("duplicate discarded reliable main: %+v %v", repriced, err)
	}
	encoded, _ := json.Marshal(repriced)
	if strings.Contains(string(encoded), key) || !strings.Contains(string(encoded), "quote-main") {
		t.Fatal("private receipt scope exposed or price provenance lost")
	}
	facts.Units[0].ReceiptKey = ""
	facts.Units = facts.Units[:1]
	s, err = Calculate(book(t, o), "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "2") || s.Lines[1].Reason != "missing_creation_receipt" {
		t.Fatal("first seen container was charged as new")
	}
}

func TestFourBucketsTTLModalitiesAndActualServiceTier(t *testing.T) {
	o := offer(t, "main", "CNY", "1")
	o.Quote.Prices = market.TokenPrices{Input: amount(t, "1"), Output: amount(t, "2"), CacheWrite: amount(t, "3"), CacheRead: amount(t, "0.1"), Details: map[string]money.Amount{"input_image": amount(t, "10"), "output_audio": amount(t, "20"), "cache_write_5m": amount(t, "2"), "cache_write_1h": amount(t, "4"), "cache_read_image": amount(t, "0.05")}}
	fast := o.Quote.Prices
	fast.Input = amount(t, "100")
	o.Quote.Tiers = []market.Tier{{Name: "actual-fast", Conditions: market.Conditions{ServiceTier: "openai:fast"}, Prices: fast}}
	usage := ledger.Usage{InputTokens: 1000, OutputTokens: 500, CacheWriteTokens: 300, CacheReadTokens: 200, Detail: &ledger.UsageDetail{RequestedServiceTier: "openai:fast", ServiceTier: "openai:default", Tokens: map[string]int64{"input_image": 200, "output_audio": 100, "cache_write_5m": 100, "cache_write_1h": 200, "cache_read_image": 50}}}
	s, err := Calculate(book(t, o), "main", Facts{MainReliable: true, Usage: usage}, time.Now(), ledger.UnitRate/2, 7*ledger.UnitRate, ledger.UnitRate/100)
	if err != nil || s.Cost != amount(t, "0.00330875") || s.Fee != amount(t, "0.000033088") || s.Lines[0].TierSeq != 0 {
		t.Fatalf("four-bucket/modal/TTL/tier error: %+v %v", s, err)
	}
}

func TestReceiptIdentityUsesEngineCredentialExecutionAndPeriod(t *testing.T) {
	root := make([]byte, 32)
	scope := strings.Repeat("a", 64)
	one, err := ExecutionReceipt(root, "group", scope, "execution", "period-1", "tool_session")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := ExecutionReceipt(root, "group", scope, "execution", "period-1", "tool_session")
	if one != again {
		t.Fatal("same engine/key execution not deduplicated")
	}
	for _, parts := range [][5]string{{"other-group", scope, "execution", "period-1", "tool_session"}, {"group", strings.Repeat("b", 64), "execution", "period-1", "tool_session"}, {"group", scope, "execution-2", "period-1", "tool_session"}, {"group", scope, "execution", "period-2", "tool_session"}, {"group", scope, "execution", "period-1", "tool_second"}} {
		next, err := ExecutionReceipt(root, parts[0], parts[1], parts[2], parts[3], parts[4])
		if err != nil || next == one {
			t.Fatal("distinct billing scope collided", err)
		}
	}
}

func TestRuntimeQuantityNeedsReliableBillingPeriodAndPreservesFraction(t *testing.T) {
	o := offer(t, "main", "CNY", "0")
	o.Quote.UnitPrices = []market.UnitPrice{{Unit: "tool_second", Tool: "code", Price: amount(t, "2")}}
	facts := Facts{MainReliable: true, Units: []Unit{{Unit: "tool_second", Tool: "code", QuantityNano: 1_250_000_000}}}
	statement, err := Calculate(book(t, o), "main", facts, time.Now(), ledger.UnitRate/2, ledger.UnitRate, 0)
	if err != nil || statement.Cost != 0 || statement.Lines[1].Reason != "missing_creation_receipt" {
		t.Fatal("runtime without an execution period was billed", err)
	}
	facts.Units[0].ReceiptKey = strings.Repeat("c", 64)
	statement, err = Calculate(book(t, o), "main", facts, time.Now(), ledger.UnitRate/2, ledger.UnitRate, 0)
	if err != nil || statement.Cost != amount(t, "1.25") {
		t.Fatal("fractional reliable runtime priced incorrectly", err)
	}
}

func TestChildAliasAmbiguityNeverSelectsAnotherOfferPrice(t *testing.T) {
	main := offer(t, "main", "CNY", "2")
	child := offer(t, "child", "CNY", "3")
	child.UpstreamModel = "native-image"
	facts := Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{Iterations: []ledger.UsageIteration{{Type: "image_generation_call", Model: "native-image", Usage: ledger.Usage{InputTokens: 1_000_000}}}}}}
	s, err := Calculate(book(t, main, child), "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "5") {
		t.Fatal("unique same-group native mapping not priced", err)
	}
	ambiguous := offer(t, "native-image", "CNY", "999")
	ambiguous.UpstreamModel = "different-engine-model"
	s, err = Calculate(book(t, main, child, ambiguous), "main", facts, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "2") || s.Lines[1].State != "unpriced" {
		t.Fatal("ambiguous child borrowed a price", err)
	}
}

func TestUnitPriorityUsesActualFastTierAlias(t *testing.T) {
	o := offer(t, "main", "CNY", "0")
	o.Quote.UnitPrices = []market.UnitPrice{{Unit: "tool_call", Tool: "search", ServiceTier: "openai:fast", Price: amount(t, "2")}, {Unit: "tool_call", Tool: "search", Price: amount(t, "1")}}
	s, err := Calculate(book(t, o), "main", Facts{MainReliable: true, Units: []Unit{{Unit: "tool_call", Tool: "search", ServiceTier: "openai:priority", QuantityNano: ledger.UnitRate}}}, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil || s.Cost != amount(t, "2") || s.Lines[1].Unit.ServiceTier != "openai:priority" {
		t.Fatal("actual tier alias lost or requested tier substituted", err)
	}
}
