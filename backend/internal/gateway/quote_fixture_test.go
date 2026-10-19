package gateway

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

// quotedCandidate freezes prices supplied by the test setup. It does not read
// a directory, synthesize capability evidence or implement runtime fallback.
func quotedCandidate(t testing.TB, candidate Candidate, main catalog.Model, children ...catalog.Model) Candidate {
	t.Helper()
	book := quotepricing.Book{GroupID: "group", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{}}
	for _, model := range append([]catalog.Model{main}, children...) {
		currency := model.PriceCurrency
		if currency == "" {
			currency = ledger.CurrencyCNY
		}
		quote := market.Quote{ID: "quote-" + model.ID, GroupModelID: "group-" + model.ID, Version: 1, Currency: currency, BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: model.InputPrice, Output: model.OutputPrice, CacheWrite: model.CacheWritePrice, CacheRead: model.CacheReadPrice, Details: model.TokenPrices}}
		for _, tier := range model.PriceTiers {
			quote.Tiers = append(quote.Tiers, market.Tier{Name: tier.Name, Conditions: market.Conditions{ServiceTier: tier.ServiceTier, MinPromptTokens: tier.MinPromptTokens, MaxPromptTokens: tier.MaxPromptTokens, Timezone: tier.Timezone, Weekdays: tier.Weekdays, StartMinute: tier.StartMinute, EndMinute: tier.EndMinute}, Prices: market.TokenPrices{Input: tier.InputPrice, Output: tier.OutputPrice, CacheWrite: tier.CacheWritePrice, CacheRead: tier.CacheReadPrice, Details: tier.TokenPrices}})
		}
		book.Offers[model.ID] = quotepricing.Offer{ModelID: model.ID, Revision: 1, Quote: quote}
	}
	var err error
	candidate.Quotes, err = quotepricing.Freeze(book)
	if err != nil {
		t.Fatal(err)
	}
	candidate.GroupID, candidate.GroupModelID = book.GroupID, book.Offers[main.ID].Quote.GroupModelID
	return candidate
}
