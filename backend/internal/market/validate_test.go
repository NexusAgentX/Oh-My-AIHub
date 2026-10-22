package market

import (
	"encoding/json"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestDefaultsPreserveInheritanceAndExplicitEmptyRules(t *testing.T) {
	for _, v := range []Defaults{{}, {HeaderRules: &HeaderRules{Set: []HeaderSet{}, Remove: []string{}}}} {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var got Defaults
		if Decode(body, &got) != nil || (v.HeaderRules == nil) != (got.HeaderRules == nil) {
			t.Fatal("null and empty header block collapsed")
		}
		if ValidateDefaults(got) != nil {
			t.Fatal("valid defaults rejected")
		}
	}
	for _, v := range []Defaults{{HeaderRules: &HeaderRules{Set: []HeaderSet{{Name: "Authorization", Value: "forbidden"}}}}, {HeaderRules: &HeaderRules{Remove: []string{"Content-Length"}}}} {
		if ValidateDefaults(v) == nil {
			t.Fatal("transport credentials editable through rules")
		}
	}
	for _, value := range []string{`{"untyped_extension":1}`, `{} {}`, `{} trailing`} {
		var v Defaults
		if Decode([]byte(value), &v) == nil {
			t.Fatal("unvalidated JSON accepted")
		}
	}
}

func TestCapabilityDigestCoversOnlyWhatAProbeAnswersFor(t *testing.T) {
	m := GroupModel{GroupID: "g", ModelID: "vendor/model:v1", DisplayName: "m", UpstreamModel: "vendor/model:v1", ModelType: "text"}
	if ValidateGroupModel(m) != nil {
		t.Fatal("native model namespace rejected")
	}
	old := ModelCapabilityDigest(m)
	m.DisplayName = "label"
	quote := "new-price-version"
	m.QuoteID = &quote
	m.Revision = 4
	if ModelCapabilityDigest(m) != old {
		t.Fatal("price/display/revision change invalidated a probe")
	}
	for _, change := range []func(*GroupModel){
		func(v *GroupModel) { v.UpstreamModel = "different/model" },
		func(v *GroupModel) { v.ModelType = "image" },
		func(v *GroupModel) { v.ModelID = "vendor/other:v1" },
		func(v *GroupModel) { window := int64(1 << 20); v.ContextWindow = &window },
		func(v *GroupModel) { output := int64(4096); v.MaxOutputTokens = &output },
	} {
		changed := m
		change(&changed)
		if ModelCapabilityDigest(changed) == old {
			t.Fatalf("model change retained stale evidence: %+v", changed)
		}
	}
}

func TestTypedQuoteValidationAndZeroPrices(t *testing.T) {
	q := Quote{GroupModelID: "m", Currency: "CNY", BillingUnit: "per_million_tokens", Prices: TokenPrices{Details: map[string]money.Amount{"cache_write_1h": 0, "input_image": 0}}, UnitPrices: []UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: 0}}}
	if ValidateQuote(q) != nil {
		t.Fatal("explicit free prices rejected")
	}
	for _, name := range []string{"currency", "negative", "unknown_dimension", "unknown_unit", "missing_unit", "bad_tool", "unconditional_tier", "bad_window"} {
		t.Run(name, func(t *testing.T) {
			v := q
			switch name {
			case "currency":
				v.Currency = "unknown"
			case "negative":
				v.Prices.Input = -1
			case "unknown_dimension":
				v.Prices.Details = map[string]money.Amount{"probability": 1}
			case "unknown_unit":
				v.BillingUnit = "estimated_output_length"
			case "missing_unit":
				v.BillingUnit = "per_successful_image"
			case "bad_tool":
				v.UnitPrices = []UnitPrice{{Unit: "tool_call"}}
			case "unconditional_tier":
				v.Tiers = []Tier{{Name: "no-condition"}}
			case "bad_window":
				minute := int16(1)
				v.Tiers = []Tier{{Conditions: Conditions{StartMinute: &minute}}}
			}
			if ValidateQuote(v) == nil {
				t.Fatal("invalid quote accepted")
			}
		})
	}
	for _, unit := range []struct{ billing, unit string }{{"per_request", "request"}, {"per_successful_image", "successful_image"}} {
		v := q
		v.BillingUnit = unit.billing
		v.UnitPrices = []UnitPrice{{Unit: unit.unit, Price: 0}}
		if ValidateQuote(v) != nil {
			t.Fatal("reliable unit price rejected")
		}
	}
}
