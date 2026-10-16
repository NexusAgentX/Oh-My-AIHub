package gateway

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func geoPriceStrings(values map[string]int64) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		out[k] = money.FromNano(v).String()
	}
	return out
}

func (e *Engine) priceUsage(call *callState, model catalog.Model, candidate Candidate, usage ledger.Usage, defaults settings.Settings, feeRate int64) (ledger.PriceResult, []byte, error) {
	if usage.Detail == nil || usage.Detail.InferenceGeo == "" {
		if geo := endpointGeo(candidate.BaseURL); geo != "" {
			if usage.Detail == nil {
				usage.Detail = &ledger.UsageDetail{}
			}
			usage.Detail.InferenceGeo = geo
		}
	}
	// The ordinary path still performs one pricing calculation and no catalog lookups.
	if usage.Detail == nil || len(usage.Detail.Iterations) == 0 {
		noteSelectedUnpriced(usage, model, call.started)
		priced, err := ledger.CalculatePriceAt(usage, model.BasePrices(), model.PriceTiers, call.started, candidate.MultiplierNano, candidate.RateNano, feeRate)
		if err != nil {
			return priced, nil, err
		}
		return priced, priceSnapshot(model, priced, candidate, defaults.USDCNYRateNano, feeRate, usage, call.started), nil
	}
	d := usage.Detail
	type component struct {
		Model string `json:"model"`

		Kind     string          `json:"kind"`
		Usage    ledger.Usage    `json:"usage"`
		Snapshot json.RawMessage `json:"price_snapshot"`
	}
	parts := []ledger.PriceResult{}
	components := []component{}
	appendPart := func(m catalog.Model, u ledger.Usage, kind string) error {
		if u.Detail != nil && u.Detail.InferenceGeo == "" {
			u.Detail.InferenceGeo = d.InferenceGeo
		}
		noteSelectedUnpriced(u, m, call.started)
		c := candidate
		c.RateNano = ledger.ChannelRate(m.PriceCurrency, c.FXDiscount, defaults.USDCNYRateNano)
		priced, err := ledger.CalculatePriceComponentAt(u, m.BasePrices(), m.PriceTiers, call.started, c.MultiplierNano, c.RateNano)
		if err != nil {
			return err
		}
		parts = append(parts, priced)
		components = append(components, component{Model: m.ID, Kind: kind, Usage: u, Snapshot: priceSnapshot(m, priced, c, defaults.USDCNYRateNano, 0, u, call.started)})
		return nil
	}
	// Anthropic iterations replace the aggregate tokens. Other sub-inferences
	// (Responses image tools) supplement the main model's own usage.
	if call.format != "anthropic" || len(d.Iterations) == 0 {
		base := usage
		copyDetail := *d
		copyDetail.Iterations = nil
		base.Detail = &copyDetail
		if err := appendPart(model, base, "main"); err != nil {
			return ledger.PriceResult{}, nil, err
		}
	}
	for _, iteration := range d.Iterations {
		m := model
		if iteration.Type == "advisor_message" || iteration.Type == "image_generation_call" || iteration.Model != "" && iteration.Model != model.ID && iteration.Model != candidate.UpstreamModel {
			found := false
			for _, child := range call.submodels {
				if child.ID == iteration.Model {
					m = child
					found = true
					break
				}
			}
			if !found {
				d.Notes = appendNote(d.Notes, fmt.Sprintf("子推理模型 %s 未配置当次价格，未计价", boundedModelName(iteration.Model)))
				continue
			}
		}
		if err := appendPart(m, iteration.Usage, iteration.Type); err != nil {
			return ledger.PriceResult{}, nil, err
		}
	}

	priced, err := ledger.CombinePrices(parts, feeRate)
	if err != nil {
		return priced, nil, err
	}
	raw := priceSnapshot(model, priced, candidate, defaults.USDCNYRateNano, feeRate, usage, call.started)
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		return priced, nil, err
	}
	saved["components"], _ = json.Marshal(components)
	raw, err = json.Marshal(saved)
	return priced, raw, err
}

func boundedModelName(value string) string {
	if !catalog.ValidModelID(value) {
		return "unknown"
	}
	return value
}

func endpointGeo(base string) string {
	if !strings.Contains(base, "us.api.openai.com") && !strings.Contains(base, "eu.api.openai.com") && !strings.Contains(base, "aiplatform.googleapis.com") {
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
func noteUnpriced(d *ledger.UsageDetail, prices ledger.Prices) {
	if d.InferenceGeo != "" && d.InferenceGeo != "global" {
		if _, ok := prices.GeoMultipliers[d.InferenceGeo]; !ok {
			d.Notes = appendNote(d.Notes, "地域 "+d.InferenceGeo+" 未配置倍率，按基础价格")
		}
	}
}

func appendNote(notes []string, note string) []string {
	for _, existing := range notes {
		if existing == note {
			return notes
		}
	}
	if len(notes) >= maxBillingItems {
		return notes
	}
	return append(notes, note)
}
func noteSelectedUnpriced(u ledger.Usage, m catalog.Model, at time.Time) {
	if u.Detail == nil {
		return
	}
	prompt, _ := ledger.PromptSideTokens(u)
	prices, _ := ledger.SelectPriceTier(m.BasePrices(), m.PriceTiers, prompt, at, u.Detail)
	noteUnpriced(u.Detail, prices)
}
