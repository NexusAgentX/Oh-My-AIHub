package typical

import (
	"encoding/json"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

// Decode projects only trusted billing facts. Snapshot prices/discounts and
// ledger net payment are deliberately not an input to the workload profile.
func Decode(raw RawCall) (Sample, bool) {
	if raw.Outcome != "succeeded" || raw.ID == "" || !gatewaycontract.ValidModelID(raw.ModelID) || len(raw.Snapshot) > 256<<10 || raw.StartedAt.IsZero() || raw.CompletedAt.IsZero() {
		return Sample{}, false
	}
	var env struct {
		Detail     *ledger.UsageDetail `json:"detail"`
		GroupQuote struct {
			Version string `json:"version"`
			GroupID string `json:"group_id"`
			Lines   []struct {
				quotepricing.Reference
				Kind  string             `json:"kind"`
				State string             `json:"state"`
				Usage *ledger.Usage      `json:"usage"`
				Unit  *quotepricing.Unit `json:"unit"`
			} `json:"components"`
		} `json:"group_quote"`
	}
	if json.Unmarshal(raw.Snapshot, &env) != nil || env.GroupQuote.Version != "group-quote.v1" || env.GroupQuote.GroupID == "" || len(env.GroupQuote.Lines) == 0 || len(env.GroupQuote.Lines) > 128 {
		return Sample{}, false
	}
	usage := raw.Usage
	usage.Detail = cleanDetail(env.Detail)
	sample := Sample{ID: raw.ID, GroupID: env.GroupQuote.GroupID, ModelID: raw.ModelID, At: raw.StartedAt, CompletedAt: raw.CompletedAt, Facts: quotepricing.Facts{Usage: usage, MainReliable: raw.UsageStatus == "reported", IndependentImage: raw.Format == "openai_images"}}
	seen := map[string]bool{}
	request := false
	for i, line := range env.GroupQuote.Lines {
		if line.State == "duplicate" {
			continue
		}
		if line.State != "priced" || line.QuoteID == "" || line.QuoteVersion < 1 || !gatewaycontract.ValidModelID(line.ModelID) {
			return Sample{}, false
		}
		if !seen[line.QuoteID] {
			sample.References = append(sample.References, line.Reference)
			seen[line.QuoteID] = true
		}
		if line.Usage != nil {
			sample.Facts.MainReliable = true
		}
		if line.Unit != nil {
			unit := *line.Unit
			if unit.QuantityNano < 0 || unit.QuantityNano > 9_000_000_000_000_000 || len(unit.Tool) > 128 || len(unit.Unit) > 64 || len(unit.ImageSize) > 64 || len(unit.ImageQuality) > 32 || len(unit.ImageBackground) > 32 || len(unit.ServiceTier) > 64 {
				return Sample{}, false
			}
			unit.ModelID = line.ModelID
			if unit.ModelID == raw.ModelID {
				unit.ModelID = ""
			}
			if unit.Unit == "tool_session" || unit.Unit == "tool_second" {
				unit.ReceiptKey = "verified-" + string(rune('a'+i))
			}
			request = request || unit.Tool == "" && unit.ModelID == "" && unit.Unit == "request"
			sample.Facts.Units = append(sample.Facts.Units, unit)
		}
	}
	// One completed native business request is a measured request count, allowing
	// token/request quotes to share the same workload only when tokens are known.
	if !request {
		sample.Facts.Units = append(sample.Facts.Units, quotepricing.Unit{Unit: "request", QuantityNano: ledger.UnitRate})
	}
	if usage.Detail != nil && len(usage.Detail.Iterations) > 0 {
		sample.Facts.IterationsReplace = raw.Format == "anthropic"
		lineIndex := 0
		for i := range usage.Detail.Iterations {
			it := &usage.Detail.Iterations[i]
			for lineIndex < len(env.GroupQuote.Lines) {
				line := env.GroupQuote.Lines[lineIndex]
				lineIndex++
				if line.Kind == it.Type && line.Usage != nil && line.ModelID != "" {
					it.Model = line.ModelID
					break
				}
			}
		}
	}
	if !sample.Facts.MainReliable && !sample.Facts.IndependentImage {
		return Sample{}, false
	}
	if _, err := ledger.CalculatePriceAt(usage, ledger.Prices{}, nil, raw.StartedAt, ledger.UnitRate, ledger.UnitRate, 0); err != nil {
		return Sample{}, false
	}
	sample, err := CleanSample(sample)
	return sample, err == nil
}
func cleanDetail(d *ledger.UsageDetail) *ledger.UsageDetail {
	if d == nil {
		return nil
	}
	out := &ledger.UsageDetail{Tokens: map[string]int64{}}
	if len(d.ServiceTier) <= 64 {
		out.ServiceTier = d.ServiceTier
	}
	for key, value := range d.Tokens {
		if ledger.TokenBucket(key) >= 0 && value >= 0 {
			out.Tokens[key] = value
		}
	}
	if len(d.Iterations) <= 128 {
		for _, it := range d.Iterations {
			if len(it.Type) > 64 || it.Model != "" && !gatewaycontract.ValidModelID(it.Model) {
				continue
			}
			it.Usage.Detail = cleanLeafDetail(it.Usage.Detail)
			out.Iterations = append(out.Iterations, it)
		}
	}
	return out
}
func cleanLeafDetail(d *ledger.UsageDetail) *ledger.UsageDetail {
	if d == nil {
		return nil
	}
	copy := *d
	copy.Iterations = nil
	return cleanDetail(&copy)
}
func validSample(s Sample) bool {
	return s.ID != "" && s.GroupID != "" && gatewaycontract.ValidModelID(s.ModelID) && !s.At.IsZero() && !s.CompletedAt.IsZero() && len(s.References) > 0 && len(s.References) <= 128 && !strings.ContainsAny(s.ModelID, "\n\r")
}

// CleanSample is the persistence boundary even for internal adapters. Only
// fixed numeric usage and bounded billing selectors survive serialization.
func CleanSample(s Sample) (Sample, error) {
	if !validSample(s) || len(s.ID) > 64 || len(s.GroupID) > 64 || len(s.Facts.Units) > 129 {
		return Sample{}, ErrInvalid
	}
	s.Facts.Usage.Detail = cleanDetail(s.Facts.Usage.Detail)
	s.Facts.Units = append([]quotepricing.Unit(nil), s.Facts.Units...)
	for _, ref := range s.References {
		if !gatewaycontract.ValidModelID(ref.ModelID) || ref.QuoteID == "" || len(ref.QuoteID) > 64 || len(ref.GroupModelID) > 64 || ref.QuoteVersion < 1 {
			return Sample{}, ErrInvalid
		}
	}
	for i := range s.Facts.Units {
		u := &s.Facts.Units[i]
		u.ReceiptKey = ""
		switch u.Unit {
		case "request", "successful_image", "tool_call", "tool_session", "tool_second", "tool_query":
		default:
			return Sample{}, ErrInvalid
		}
		if u.QuantityNano < 0 || u.QuantityNano > 9_000_000_000_000_000 || len(u.Tool) > 128 || len(u.ImageSize) > 64 || len(u.ImageQuality) > 32 || len(u.ImageBackground) > 32 || len(u.ServiceTier) > 64 || u.ModelID != "" && !gatewaycontract.ValidModelID(u.ModelID) {
			return Sample{}, ErrInvalid
		}
	}
	if _, err := ledger.CalculatePriceAt(s.Facts.Usage, ledger.Prices{}, nil, s.At, ledger.UnitRate, ledger.UnitRate, 0); err != nil {
		return Sample{}, ErrInvalid
	}
	return s, nil
}
