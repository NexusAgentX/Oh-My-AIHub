package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

// Shared synthetic fixture exercises the actual calculator and OpenAPI bytes;
// the opt-in browser harness uses this same statement without any upstream.
func groupBillingFixture(t *testing.T) []byte {
	t.Helper()
	main := market.Quote{ID: "10000000-0000-4000-8000-000000000001", GroupModelID: "20000000-0000-4000-8000-000000000001", Version: 2, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.FromNano(2_000_000_000)}, UnitPrices: []market.UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: money.FromNano(500_000_000)}, {Unit: "tool_session", Tool: "code", Price: money.FromNano(3_000_000_000)}}}
	image := market.Quote{ID: "10000000-0000-4000-8000-000000000002", GroupModelID: "20000000-0000-4000-8000-000000000002", Version: 1, Currency: "USD", BillingUnit: "per_successful_image", UnitPrices: []market.UnitPrice{{Unit: "successful_image", Price: money.FromNano(1_000_000_000)}}}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "30000000-0000-4000-8000-000000000001", GroupRevision: 3, ConnectionVersion: 2, Offers: map[string]quotepricing.Offer{"demo-text": {ModelID: "demo-text", Revision: 2, Quote: main}, "demo-image": {ModelID: "demo-image", Revision: 1, Quote: image}}})
	if err != nil {
		t.Fatal(err)
	}
	statement, err := quotepricing.Calculate(book, "demo-text", quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000}, Units: []quotepricing.Unit{{Unit: "tool_call", Tool: "web_search", QuantityNano: ledger.UnitRate}, {Unit: "successful_image", ModelID: "demo-image", QuantityNano: 2 * ledger.UnitRate}, {Unit: "tool_session", Tool: "code", QuantityNano: ledger.UnitRate, ReceiptKey: strings.Repeat("a", 64)}, {Unit: "tool_second", Tool: "unknown_tool", QuantityNano: ledger.UnitRate}}}, time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC), ledger.UnitRate/2, 7*ledger.UnitRate, ledger.UnitRate/100)
	if err != nil {
		t.Fatal(err)
	}
	statement, err = statement.Reprice(map[string]bool{strings.Repeat("a", 64): true})
	if err != nil {
		t.Fatal(err)
	}
	loadOpenAPI(t).assertSchema(t, "GroupChargeStatement", statement)
	base := map[string]any{"base_prices": map[string]string{"input": "2", "output": "0", "cache_write": "0", "cache_read": "0"}, "prices": map[string]string{"input": "1", "output": "0", "cache_write": "0", "cache_read": "0"}, "tier": nil, "multiplier": "0.5", "currency": "CNY", "fee_rate_nano": ledger.UnitRate / 100}
	raw, _ := json.Marshal(base)
	raw, err = gateway.WithQuoteStatement(raw, statement)
	if err != nil {
		t.Fatal(err)
	}
	loadOpenAPI(t).assertSchema(t, "PriceSnapshot", raw)
	return raw
}
func TestGroupBillingStatementMatchesPublicSnapshotContract(t *testing.T) { groupBillingFixture(t) }
