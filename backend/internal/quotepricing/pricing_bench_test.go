package quotepricing

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Pure calculator cost, no network, server, database or end-to-end A/B.
func BenchmarkGroupTokenQuote(b *testing.B) {
	book, err := Freeze(Book{GroupID: "group", GroupRevision: 1, Offers: map[string]Offer{"model": {ModelID: "model", Revision: 1, Quote: market.Quote{ID: "quote", GroupModelID: "gm", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.FromNano(1_000_000_000)}}}}})
	if err != nil {
		b.Fatal(err)
	}
	at := time.Unix(123, 0)
	facts := Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1000}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		statement, err := Calculate(book, "model", facts, at, ledger.UnitRate, ledger.UnitRate, 0)
		if err != nil || statement.Cost != 1_000_000 {
			b.Fatal("invalid pricing")
		}
	}
}
