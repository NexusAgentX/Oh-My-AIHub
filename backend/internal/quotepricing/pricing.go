// Package quotepricing prices immutable group offers using reliable response
// facts. It never reads a directory, guesses usage or accesses upstream APIs.
package quotepricing

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var ErrQuote = errors.New("invalid or missing group quote")

// Book is loaded with the candidate snapshot, never on the request hot path.
// Its models and all price tables remain immutable for an in-flight call.
type Book struct {
	GroupID           string           `json:"group_id"`
	GroupRevision     int64            `json:"group_revision"`
	ConnectionVersion int64            `json:"connection_version"`
	Offers            map[string]Offer `json:"offers"`
}
type Offer struct {
	ModelID       string       `json:"model_id"`
	UpstreamModel string       `json:"upstream_model"`
	Revision      int64        `json:"revision"`
	Quote         market.Quote `json:"quote"`
}

// Freeze validates and detaches a book from mutable administrative objects.
func Freeze(book Book) (*Book, error) {
	if book.GroupID == "" || book.GroupRevision < 1 || len(book.Offers) == 0 || len(book.Offers) > 4096 {
		return nil, ErrQuote
	}
	for id, offer := range book.Offers {
		if id != offer.ModelID || !gatewaycontract.ValidModelID(id) || (offer.UpstreamModel != "" && !gatewaycontract.ValidModelID(offer.UpstreamModel)) || offer.Revision < 1 || offer.Quote.ID == "" || offer.Quote.Version < 1 || market.ValidateQuote(offer.Quote) != nil {
			return nil, ErrQuote
		}
	}
	data, err := json.Marshal(book)
	if err != nil {
		return nil, err
	}
	var frozen Book
	if err = json.Unmarshal(data, &frozen); err != nil {
		return nil, err
	}
	return &frozen, nil
}

// Unit is an independently reliable response measurement. Quantities use nano
// units, permitting fractional seconds. ReceiptKey is a protected scope tag
// from verified execution facts; seeing a remote ID alone does not create it.
type Unit struct {
	Unit            string `json:"unit"`
	Tool            string `json:"tool,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	QuantityNano    int64  `json:"quantity_nano"`
	ImageSize       string `json:"image_size,omitempty"`
	ImageQuality    string `json:"image_quality,omitempty"`
	ImageBackground string `json:"image_background,omitempty"`
	ServiceTier     string `json:"service_tier,omitempty"`
	ReceiptKey      string `json:"-"`
}
type Facts struct {
	Usage             ledger.Usage
	MainReliable      bool
	IterationsReplace bool
	IndependentImage  bool
	Units             []Unit
}
type Reference struct {
	ModelID      string `json:"model_id"`
	GroupModelID string `json:"group_model_id"`
	Revision     int64  `json:"revision"`
	QuoteID      string `json:"quote_id"`
	QuoteVersion int64  `json:"quote_version"`
}
type Line struct {
	Reference
	Kind         string              `json:"kind"`
	State        string              `json:"state"`
	Reason       string              `json:"reason,omitempty"`
	Currency     string              `json:"currency,omitempty"`
	RateNano     int64               `json:"rate_nano"`
	DiscountNano int64               `json:"discount_nano"`
	TierSeq      int                 `json:"tier_seq"`
	Usage        *ledger.Usage       `json:"usage,omitempty"`
	Prices       *market.TokenPrices `json:"selected_prices,omitempty"`
	Unit         *Unit               `json:"unit,omitempty"`
	UnitPrice    *money.Amount       `json:"unit_price,omitempty"`
	ReceiptKey   string              `json:"-"`
	Cost         money.Amount        `json:"cost"`
	Price        ledger.PriceResult  `json:"-"`
}
type Statement struct {
	Notes         []string     `json:"notes"`
	Version       string       `json:"version"`
	GroupID       string       `json:"group_id"`
	GroupRevision int64        `json:"group_revision"`
	ConfigVersion int64        `json:"config_version"`
	StartedAt     time.Time    `json:"started_at"`
	USDCNYNano    int64        `json:"usd_cny_rate_nano"`
	FeeRateNano   int64        `json:"fee_rate_nano"`
	Lines         []Line       `json:"components"`
	Cost          money.Amount `json:"cost"`
	Fee           money.Amount `json:"fee"`
}

func reference(offer Offer) Reference {
	return Reference{ModelID: offer.ModelID, GroupModelID: offer.Quote.GroupModelID, Revision: offer.Revision, QuoteID: offer.Quote.ID, QuoteVersion: offer.Quote.Version}
}

func (b *Book) childOffer(model string) (Offer, bool) {
	var match Offer
	for _, offer := range b.Offers {
		if (offer.ModelID == model || offer.UpstreamModel == model) && model != "" {
			if match.ModelID != "" {
				return Offer{}, false
			}
			match = offer
		}
	}
	return match, match.ModelID != ""
}

func Calculate(book *Book, mainModel string, facts Facts, at time.Time, discountNano, usdRateNano, feeRateNano int64) (Statement, error) {
	if book == nil || discountNano < 0 || usdRateNano < 0 || feeRateNano < 0 || feeRateNano > ledger.UnitRate || len(facts.Units) > 128 {
		return Statement{}, ErrQuote
	}
	main, exists := book.Offers[mainModel]
	if !exists {
		return Statement{}, ErrQuote
	}
	statement := Statement{Version: "group-quote.v1", GroupID: book.GroupID, GroupRevision: book.GroupRevision, ConfigVersion: book.ConnectionVersion, StartedAt: at, USDCNYNano: usdRateNano, FeeRateNano: feeRateNano, Lines: []Line{}}
	statement.Notes = []string{}
	if facts.Usage.Detail != nil {
		for _, note := range facts.Usage.Detail.Notes {
			if note != "图像工具仅作观察，暂不收取图像子模型费用" && note != "工具用量仅作观察，暂不收取工具附加费" {
				statement.Notes = append(statement.Notes, note)
			}
		}
	}
	missing := func(model, kind, reason string) {
		statement.Lines = append(statement.Lines, Line{Reference: Reference{ModelID: model}, Kind: kind, State: "unpriced", Reason: reason})
	}
	singleTokens := facts.MainReliable && main.Quote.BillingUnit == "per_million_tokens" && len(facts.Units) == 0 && (facts.Usage.Detail == nil || len(facts.Usage.Detail.Iterations) == 0)
	if !facts.MainReliable && !facts.IndependentImage {
		missing(mainModel, "main", "missing_main_usage")
		return statement, nil
	}
	if facts.IndependentImage || main.Quote.BillingUnit != "per_million_tokens" {
		if main.Quote.BillingUnit == "per_million_tokens" && !facts.MainReliable {
			missing(mainModel, "main", "missing_main_usage")
			return statement, nil
		}
		if main.Quote.BillingUnit != "per_million_tokens" {
			expected := "request"
			if main.Quote.BillingUnit == "per_successful_image" {
				expected = "successful_image"
			}
			reliable := false
			for _, unit := range facts.Units {
				reliable = reliable || unit.Tool == "" && (unit.ModelID == "" || unit.ModelID == mainModel) && unit.Unit == expected
			}
			if !reliable {
				missing(mainModel, "main", "missing_reliable_unit")
				return statement, nil
			}
		}
	}
	addTokens := func(offer Offer, usage ledger.Usage, kind string) error {
		if offer.Quote.Currency == "USD" && usdRateNano <= 0 {
			return ErrQuote
		}
		if offer.Quote.BillingUnit != "per_million_tokens" {
			missing(offer.ModelID, kind, "missing_reliable_unit")
			return nil
		}
		tiers := make([]ledger.PriceTier, len(offer.Quote.Tiers))
		for i, tier := range offer.Quote.Tiers {
			tiers[i] = tier.Ledger()
		}
		rate := ledger.ModelRate(offer.Quote.Currency, usdRateNano)
		var priced ledger.PriceResult
		var err error
		if singleTokens {
			priced, err = ledger.CalculatePriceAt(usage, offer.Quote.Prices.Ledger(), tiers, at, discountNano, rate, feeRateNano)
		} else {
			priced, err = ledger.CalculatePriceComponentAt(usage, offer.Quote.Prices.Ledger(), tiers, at, discountNano, rate)
		}
		if err != nil {
			return err
		}
		prompt, _ := ledger.PromptSideTokens(usage)
		selected, seq := ledger.SelectPriceTier(offer.Quote.Prices.Ledger(), tiers, prompt, at, usage.Detail)
		prices := market.TokenPrices{Input: selected.InputPerMillion, Output: selected.OutputPerMillion, CacheWrite: selected.CacheWritePerMillion, CacheRead: selected.CacheReadPerMillion, Details: selected.TokenPrices}
		if prices.Details == nil {
			prices.Details = map[string]money.Amount{}
		}
		statement.Lines = append(statement.Lines, Line{Reference: reference(offer), Kind: kind, State: "priced", Currency: offer.Quote.Currency, RateNano: rate, DiscountNano: discountNano, TierSeq: seq, Usage: &usage, Prices: &prices, Price: priced, Cost: priced.Cost})
		return nil
	}
	if facts.MainReliable && (!facts.IterationsReplace || facts.Usage.Detail == nil || len(facts.Usage.Detail.Iterations) == 0) && main.Quote.BillingUnit == "per_million_tokens" {
		usage := facts.Usage
		if usage.Detail != nil {
			detail := *usage.Detail
			detail.Iterations = nil
			usage.Detail = &detail
		}
		if err := addTokens(main, usage, "main"); err != nil {
			return Statement{}, err
		}
	}
	if facts.MainReliable && facts.Usage.Detail != nil {
		for _, iteration := range facts.Usage.Detail.Iterations {
			model := iteration.Model
			if model == "" && (iteration.Type == "" || iteration.Type == "message" || iteration.Type == "compaction") {
				model = mainModel
			}
			offer, ok := book.childOffer(model)
			if !ok {
				missing(model, iteration.Type, "missing_group_quote")
				continue
			}
			if offer.Quote.BillingUnit != "per_million_tokens" {
				expected := "request"
				if offer.Quote.BillingUnit == "per_successful_image" {
					expected = "successful_image"
				}
				reliable := false
				for _, unit := range facts.Units {
					measured, found := book.childOffer(unit.ModelID)
					reliable = reliable || found && measured.ModelID == offer.ModelID && unit.Tool == "" && unit.Unit == expected
				}
				if !reliable {
					missing(offer.ModelID, iteration.Type, "missing_reliable_unit")
				}
				continue
			}
			if err := addTokens(offer, iteration.Usage, iteration.Type); err != nil {
				return Statement{}, err
			}
		}
	}
	seen := map[string]bool{}
	for _, unit := range facts.Units {
		if unit.QuantityNano < 0 || unit.QuantityNano > 9_000_000_000_000_000 {
			return Statement{}, ErrQuote
		}
		model := unit.ModelID
		if model == "" {
			model = mainModel
		}
		offer, ok := book.childOffer(model)
		if unit.ModelID == "" {
			offer, ok = main, true
		}
		if !ok {
			missing(model, unit.Unit, "missing_group_quote")
			statement.Lines[len(statement.Lines)-1].Unit = &unit
			continue
		}
		if unit.Tool == "" && ((offer.Quote.BillingUnit == "per_million_tokens") || (offer.Quote.BillingUnit == "per_request" && unit.Unit != "request") || (offer.Quote.BillingUnit == "per_successful_image" && unit.Unit != "successful_image")) {
			continue
		}
		if (unit.Unit == "tool_session" || unit.Unit == "tool_second") && unit.ReceiptKey == "" {
			missing(model, unit.Unit, "missing_creation_receipt")
			statement.Lines[len(statement.Lines)-1].Unit = &unit
			statement.Lines[len(statement.Lines)-1].Reference = reference(offer)
			continue
		}
		if unit.ReceiptKey != "" && seen[unit.ReceiptKey] {
			continue
		}
		price, ok := selectUnitPrice(offer.Quote.UnitPrices, unit)
		if !ok {
			missing(model, unit.Unit, "missing_unit_price")
			statement.Lines[len(statement.Lines)-1].Unit = &unit
			statement.Lines[len(statement.Lines)-1].Reference = reference(offer)
			continue
		}
		rate := ledger.ModelRate(offer.Quote.Currency, usdRateNano)
		if offer.Quote.Currency == "USD" && usdRateNano <= 0 {
			return Statement{}, ErrQuote
		}
		priced, err := ledger.CalculateUnitComponent(unit.QuantityNano, price, discountNano, rate)
		if err != nil {
			return Statement{}, err
		}
		seen[unit.ReceiptKey] = unit.ReceiptKey != ""
		statement.Lines = append(statement.Lines, Line{Reference: reference(offer), Kind: unit.Unit, State: "priced", Currency: offer.Quote.Currency, RateNano: rate, DiscountNano: discountNano, Unit: &unit, UnitPrice: &price, ReceiptKey: unit.ReceiptKey, Price: priced, Cost: priced.Cost})
	}
	if singleTokens && len(statement.Lines) == 1 {
		statement.Cost, statement.Fee = statement.Lines[0].Price.Cost, statement.Lines[0].Price.Fee
		return statement, nil
	}
	return statement.Reprice(nil)
}

// Reprice excludes already claimed receipts, keeping every other reliable
// component. The persistence layer calls it before locking ledger balances.
func (s Statement) Reprice(duplicates map[string]bool) (Statement, error) {
	s.Lines = slices.Clone(s.Lines)
	parts := make([]ledger.PriceResult, 0, len(s.Lines))
	for i := range s.Lines {
		line := &s.Lines[i]
		if line.State != "priced" {
			continue
		}
		if line.ReceiptKey != "" && duplicates[line.ReceiptKey] {
			line.State = "duplicate"
			line.Reason = "execution_already_billed"
			line.Cost = 0
			continue
		}
		parts = append(parts, line.Price)
	}
	priced, err := ledger.CombinePrices(parts, s.FeeRateNano)
	if err != nil {
		return Statement{}, err
	}
	s.Cost, s.Fee = priced.Cost, priced.Fee
	return s, nil
}
func (s Statement) ReceiptKeys() []string {
	keys := []string{}
	for _, line := range s.Lines {
		if line.State == "priced" && line.ReceiptKey != "" {
			keys = append(keys, line.ReceiptKey)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
func selectUnitPrice(prices []market.UnitPrice, unit Unit) (money.Amount, bool) {
	actualTier := unit.ServiceTier
	if actualTier == "openai:priority" {
		actualTier = "openai:fast"
	}
	for _, candidate := range prices {
		if candidate.Unit != unit.Unit || candidate.Tool != unit.Tool {
			continue
		}
		matches := true
		for _, pair := range [][2]string{{candidate.ImageSize, unit.ImageSize}, {candidate.ImageQuality, unit.ImageQuality}, {candidate.ImageBackground, unit.ImageBackground}, {candidate.ServiceTier, actualTier}} {
			if pair[0] != "" {
				matches = matches && pair[0] == pair[1]
			}
		}
		if matches {
			return candidate.Price, true
		}
	}
	return 0, false
}
