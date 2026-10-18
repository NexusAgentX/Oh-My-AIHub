package gateway

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func (e *Engine) priceUsage(call *callState, model catalog.Model, candidate Candidate, usage ledger.Usage, defaults settings.Settings, feeRate int64) (ledger.PriceResult, []byte, error) {
	if !candidate.ValidOffering(model.ID) {
		return ledger.PriceResult{}, nil, quotepricing.ErrQuote
	}
	if usage.Detail != nil {
		detail := *usage.Detail
		detail.Notes = []string{}
		for _, note := range usage.Detail.Notes {
			if note != "图像工具仅作观察，暂不收取图像子模型费用" && note != "工具用量仅作观察，暂不收取工具附加费" {
				detail.Notes = append(detail.Notes, note)
			}
		}
		usage.Detail = &detail
	}
	facts := call.billingFacts
	facts.Usage = usage
	if offer, ok := candidate.Quotes.Offers[model.ID]; ok && offer.Quote.BillingUnit == "per_request" && facts.MainReliable && !facts.IndependentImage {
		tier := ""
		if usage.Detail != nil {
			tier = usage.Detail.ServiceTier
		}
		facts.Units = append(facts.Units, quotepricing.Unit{Unit: "request", QuantityNano: ledger.UnitRate, ServiceTier: tier})
	}
	statement, err := quotepricing.Calculate(candidate.Quotes, model.ID, facts, call.started, candidate.MultiplierNano, defaults.USDCNYRateNano, feeRate)
	if err != nil {
		return ledger.PriceResult{}, nil, err
	}
	call.pricedStatement = &statement
	priced := ledger.PriceResult{Cost: statement.Cost, Fee: statement.Fee}
	snapshotCandidate := candidate
	snapshotModel := candidate.PricingModel(model)
	snapshotCandidate.RateNano = ledger.ModelRate(snapshotModel.PriceCurrency, defaults.USDCNYRateNano)
	raw, err := WithQuoteStatement(priceSnapshot(snapshotModel, priced, snapshotCandidate, defaults.USDCNYRateNano, feeRate, usage, call.started), statement)
	return priced, raw, err
}

// WithQuoteStatement keeps the historical envelope with versioned provenance.
func WithQuoteStatement(raw []byte, statement quotepricing.Statement) ([]byte, error) {
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(statement)
	if err != nil {
		return nil, err
	}
	saved["group_quote"] = encoded
	return json.Marshal(saved)
}

func endpointGeo(base string) string {
	lower := strings.ToLower(base)
	if !strings.Contains(lower, "us.api.openai.com") && !strings.Contains(lower, "eu.api.openai.com") && !strings.Contains(lower, "aiplatform.googleapis.com") {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "us.api.openai.com":
		return "us"
	case "eu.api.openai.com":
		return "eu"
	}
	// Vertex's explicit location is a routing fact, not a guessed country.
	if strings.HasSuffix(strings.ToLower(parsed.Hostname()), "-aiplatform.googleapis.com") {
		parts := strings.Split(parsed.Path, "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "locations" && validGeo(parts[i+1]) {
				return parts[i+1]
			}
		}
	}
	return ""
}
