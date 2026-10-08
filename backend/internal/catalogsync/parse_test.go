package catalogsync

import (
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"os"
	"testing"
	"time"
)

func parse(t *testing.T, body string) Entry {
	t.Helper()
	return Parse("test", json.RawMessage(body), "2")
}
func TestExactConversionAndInvalidNumbers(t *testing.T) {
	for raw, want := range map[string]int64{"0": 0, "0.000001": 2_000_000_000, "1e-18": 1} {
		got, err := Price(json.RawMessage(raw), "2")
		if err != nil || int64(got) != want {
			t.Fatalf("%s: %v %v", raw, got, err)
		}
	}
	for _, raw := range []string{"-1", "null", "\"1\"", "1e99999999", "0.12345678901234567890123456789"} {
		if _, err := Price(json.RawMessage(raw), "2"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestMissingZeroCacheFallbackAndTierBoundary(t *testing.T) {
	e := parse(t, `{"provider":"test","input_cost_per_token":0.000001,"output_cost_per_token":0,"input_cost_per_token_above_200k_tokens":0.000002}`)
	if len(e.Problems) > 0 {
		t.Fatal(e.Problems)
	}
	if e.Model.OutputPrice != 0 || e.Model.CacheReadPrice != e.Model.InputPrice {
		t.Fatal(e.Model)
	}
	p, _ := ledger.SelectPriceTier(e.Model.BasePrices(), e.Model.PriceTiers, 200000, time.Now())
	if p.InputPerMillion != 2_000_000_000 {
		t.Fatal(p)
	}
	p, _ = ledger.SelectPriceTier(e.Model.BasePrices(), e.Model.PriceTiers, 200001, time.Now())
	if p.InputPerMillion != 4_000_000_000 || p.CacheWritePerMillion != 4_000_000_000 {
		t.Fatal(p)
	}
	e = parse(t, `{"input_cost_per_token":0}`)
	if len(e.Problems) == 0 {
		t.Fatal("missing output accepted")
	}
}
func TestUnsupportedTokenConditionsAndAdditionalFees(t *testing.T) {
	e := parse(t, `{"input_cost_per_token":0,"output_cost_per_token":0,"web_search_cost_per_request":0.1}`)
	if len(e.Problems) > 0 || len(e.Warnings) == 0 {
		t.Fatal(e)
	}
	e = parse(t, `{"input_cost_per_token":0,"output_cost_per_token":0,"input_cost_per_token_fast":0.2}`)
	if len(e.Problems) == 0 {
		t.Fatal("unknown condition accepted")
	}
	e = parse(t, `{"provider":"openai","input_cost_per_token":0.1,"output_cost_per_token":0.1,"input_cost_per_token_priority":0.2,"input_cost_per_token_above_200k_tokens":0.3}`)
	if len(e.Problems) == 0 {
		t.Fatal("ambiguous intersection accepted")
	}
}
func TestDecodeAndIdentity(t *testing.T) {
	for _, body := range []string{`{}`, `{"a":`, `{"a":{},"a":{}}`, `{"a":{"mode":"chat"}} trailing`} {
		if _, err := Decode([]byte(body), "1"); err == nil {
			t.Fatal(body)
		}
	}
	if ModelID("gpt-4") != "gpt-4" || ModelID("prefix/a/b") != "b" || !legalID.MatchString(ModelID("a/b")) {
		t.Fatal("unstable identity")
	}
}
func TestExplicitOffPeakAndConflict(t *testing.T) {
	e := parse(t, `{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"off_peak_pricing":{"input_cost_per_token":0.0000005,"windows":[{"hours_utc":"00:00-00:00","weekdays":[6,7]}]}}`)
	if len(e.Problems) > 0 || len(e.Model.PriceTiers) != 1 {
		t.Fatal(e.Problems)
	}
	p, _ := ledger.SelectPriceTier(e.Model.BasePrices(), e.Model.PriceTiers, 10, time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC))
	if p.InputPerMillion != 1_000_000_000 {
		t.Fatal(p)
	}
	e = parse(t, `{"input_cost_per_token":0,"output_cost_per_token":0,"off_peak_pricing":{},"off_peak_cost_multiplier":0.5}`)
	if len(e.Problems) == 0 {
		t.Fatal("conflicting peak accepted")
	}
}
func TestRealDatasheet(t *testing.T) {
	path := os.Getenv("BIFROST_TEST_DATASHEET")
	if path == "" {
		t.Skip("optional real source fixture")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Decode(raw, "1")
	if err != nil {
		t.Fatal(err)
	}
	ready, review := 0, 0
	for _, e := range entries {
		if len(e.Problems) == 0 {
			ready++
		} else {
			review++
		}
	}
	t.Logf("chat/Responses=%d ready=%d review=%d", len(entries), ready, review)
}

func TestServiceCrossBucketAndCacheTTL(t *testing.T) {
	e := parse(t, `{"provider":"openai","input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_creation_input_token_cost":0.00000125,"cache_creation_input_token_cost_above_1hr":0.000002,"cache_read_input_token_cost":0.0000001,"input_cost_per_audio_token":0.00004,"input_cost_per_token_priority":0.000002,"output_cost_per_token_priority":0.000004}`)
	if len(e.Problems) > 0 || len(e.Model.PriceTiers) != 1 {
		t.Fatal(e.Problems)
	}
	tier := e.Model.PriceTiers[0]
	if tier.ServiceTier != "openai:priority" || tier.InputPrice.String() != "4" || tier.TokenPrices["input_audio"].String() != "80" || tier.TokenPrices["cache_write_1h"].String() != "4" || tier.TokenPrices["cache_write_5m"].String() != "2.5" {
		t.Fatalf("service/cached modality inherited incorrectly: %+v", tier)
	}
	e = parse(t, `{"provider":"openai","input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_read_input_token_cost_priority":0.0000002,"cache_read_input_token_cost_above_200k_tokens":0.0000004,"input_cost_per_token_priority_above_200k_tokens":0.000005}`)
	if len(e.Problems) == 0 {
		t.Fatal("partial cross bucket accepted")
	}
}
