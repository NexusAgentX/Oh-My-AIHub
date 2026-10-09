package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"testing"
	"time"
)

func TestDetailedObserver(t *testing.T) {
	cases := []struct {
		name   string
		format channel.Format
		body   string
		tokens map[string]int64
		tier   string
		output int64
	}{
		{"mixed ttl", channel.FormatAnthropic, `{"usage":{"input_tokens":1,"output_tokens":2,"service_tier":"priority","cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}`, map[string]int64{"cache_write_5m": 10, "cache_write_1h": 20}, "anthropic:priority", 2},
		{"audio", channel.FormatOpenAIChat, `{"service_tier":"default","usage":{"prompt_tokens":30,"completion_tokens":20,"prompt_tokens_details":{"audio_tokens":10},"completion_tokens_details":{"audio_tokens":5,"reasoning_tokens":3}}}`, map[string]int64{"input_audio": 10, "output_audio": 5}, "openai:default", 20},
		{"gemini", channel.FormatGemini, `{"usageMetadata":{"serviceTier":"STANDARD","promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":10,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":70},{"modality":"TEXT","tokenCount":30}],"cacheTokensDetails":[{"modality":"AUDIO","tokenCount":40}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":20}]}}`, map[string]int64{"input_audio": 30, "input_text": 30, "cache_read_audio": 40, "output_text": 20}, "gemini:STANDARD", 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := observe(tc.format, false, "application/json", tc.body, 7)
			if got.Usage.OutputTokens != tc.output || got.Usage.Detail == nil || got.Usage.Detail.ServiceTier != tc.tier {
				t.Fatalf("%+v", got.Usage)
			}
			for key, n := range tc.tokens {
				if got.Usage.Detail.Tokens[key] != n {
					t.Fatalf("%+v", got.Usage.Detail)
				}
			}
		})
	}
}
func TestStreamFactsAndReplacement(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
	for _, raw := range []string{`{"service_tier":"priority","choices":[{"delta":{"reasoning_content":"private thinking not stored"}}]}`, `{"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"audio_tokens":30}}}`, `{"service_tier":"default","usage":{"prompt_tokens":10,"completion_tokens":20}}`} {
		o.absorb([]byte(raw))
	}
	got := o.Finish()
	if got.Usage.Detail.ServiceTier != "openai:default" || got.Usage.Detail.ThinkingMode != "qwen_thinking" || len(got.Usage.Detail.Tokens) != 0 || got.Usage.OutputTokens != 20 {
		t.Fatalf("%+v", got.Usage.Detail)
	}
	snap := priceSnapshot(catalog.Model{}, ledger.PriceResult{}, ledger.FixedPointScale, 0, got.Usage, time.Now())
	var decoded struct {
		Detail ledger.UsageDetail `json:"detail"`
	}
	_ = json.Unmarshal(snap, &decoded)
	if decoded.Detail.ServiceTier != "openai:default" || decoded.Detail.ThinkingMode != "qwen_thinking" {
		t.Fatal(string(snap))
	}
}
func TestCacheIntersectionNeverGuessedAndStaleDetailsCleared(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIChat, true, "")
	o.absorb([]byte(`{"usage":{"prompt_tokens":100,"completion_tokens":0,"prompt_tokens_details":{"audio_tokens":30}}}`))
	o.absorb([]byte(`{"usage":{"prompt_tokens":100,"completion_tokens":0,"prompt_tokens_details":{"audio_tokens":30,"cached_tokens":40}}}`))
	d := o.Finish().Usage.Detail
	if len(d.Tokens) != 0 || len(d.Notes) == 0 {
		t.Fatal(d)
	}
	a := NewObserver(channel.FormatAnthropic, true, "")
	a.absorb([]byte(`{"usage":{"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}`))
	a.absorb([]byte(`{"usage":{"output_tokens":15}}`))
	if a.Finish().Usage.Detail.Tokens["cache_write_1h"] != 20 {
		t.Fatal("partial erased ttl")
	}
	a.absorb([]byte(`{"usage":{"cache_creation":null}}`))
	if len(a.Finish().Usage.Detail.Tokens) != 0 {
		t.Fatal("invalid retained ttl")
	}
	g := NewObserver(channel.FormatGemini, true, "")
	g.absorb([]byte(`{"usageMetadata":{"promptTokenCount":20,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":20}]}}`))
	g.absorb([]byte(`{"usageMetadata":{"promptTokenCount":10}}`))
	if d := g.Finish().Usage.Detail; d != nil && len(d.Tokens) != 0 {
		t.Fatal("stale gemini tokens")
	}
}

func TestInvalidGeminiDetailsAndShrinkingAnthropicFallback(t *testing.T) {
	for _, body := range []string{
		`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":0,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":100}],"cacheTokensDetails":[{"modality":"AUDIO","tokenCount":20}]}}`,
		`{"usageMetadata":{"candidatesTokenCount":10,"candidatesTokensDetails":[{"modality":"AUDIO"}]}}`,
	} {
		got := observe(channel.FormatGemini, false, "application/json", body, 1000)
		if got.Usage.Detail != nil && len(got.Usage.Detail.Tokens) > 0 {
			t.Fatalf("invalid detail accepted: %+v", got.Usage.Detail)
		}
	}
	a := NewObserver(channel.FormatAnthropic, true, "")
	a.absorb([]byte(`{"usage":{"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":0}}}`))
	a.absorb([]byte(`{"usage":{"cache_creation_input_tokens":50,"output_tokens":2}}`))
	got := a.Finish()
	if got.Usage.Detail == nil || len(got.Usage.Detail.Tokens) > 0 || len(got.Usage.Detail.Notes) == 0 {
		t.Fatal(got.Usage.Detail)
	}
	if _, err := ledger.CalculatePriceV2(got.Usage, ledger.Prices{CacheWritePerMillion: 1}, nil, time.Now(), ledger.FixedPointScale, 0); err != nil {
		t.Fatal("fallback must remain billable", err)
	}
}

func TestSnapshotKeepsExactWinningPricesBeforeMultiplier(t *testing.T) {
	model := catalog.Model{PriceTiers: []ledger.PriceTier{{Name: "nano", ServiceTier: "openai:default", InputPrice: 1, TokenPrices: map[string]money.Amount{"input_audio": 1}}}}
	usage := ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{ServiceTier: "openai:default", Tokens: map[string]int64{"input_audio": 1_000_000}}}
	now := time.Now()
	priced, err := ledger.CalculatePriceV2(usage, model.BasePrices(), model.PriceTiers, now, 100_000_000, 0)
	if err != nil || priced.Cost != 1 {
		t.Fatalf("%+v %v", priced, err)
	}
	raw := priceSnapshot(model, priced, 100_000_000, 0, usage, now)
	model.PriceTiers[0].TokenPrices["input_audio"] = 900
	var snapshot struct {
		Selected struct {
			Input  string            `json:"input"`
			Tokens map[string]string `json:"token_prices"`
		} `json:"selected_prices"`
		Multiplier string `json:"multiplier"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Selected.Input != "0.000000001" || snapshot.Selected.Tokens["input_audio"] != "0.000000001" || snapshot.Multiplier != "0.1" {
		t.Fatal(string(raw))
	}
}

func TestAnthropicSpeedPricing(t *testing.T) {
	for _, tc := range []struct {
		name, speed, tail string
		fast              bool
	}{
		{"fast", `,"speed":"fast"`, "", true},
		{"standard", `,"speed":"standard"`, "", false},
		{"missing", "", "", false},
		{"unknown", `,"speed":"unknown"`, "", false},
		{"later tier", `,"speed":"fast"`, `{"service_tier":"standard","usage":{"service_tier":"standard","output_tokens":1000000}}`, true},
		{"delta fast", "", `{"usage":{"speed":"fast","output_tokens":1000000}}`, true},
		{"delta standard", `,"speed":"fast"`, `{"usage":{"speed":"standard","output_tokens":1000000}}`, false},
	} {
		for _, stream := range []bool{false, true} {
			if !stream && tc.tail != "" {
				continue
			}
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				body := `{"usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_creation_input_tokens":2000000,"cache_read_input_tokens":1000000,"cache_creation":{"ephemeral_5m_input_tokens":1000000,"ephemeral_1h_input_tokens":1000000},"service_tier":"standard"` + tc.speed + `}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + body + "}\n\n"
					if tc.tail != "" {
						body += "event: message_delta\ndata: " + tc.tail + "\n\n"
					}
				}
				got := observe(channel.FormatAnthropic, stream, contentType, body, 7)
				wantTier := "anthropic:standard"
				if tc.fast {
					wantTier = "anthropic:fast"
				}
				if !got.Found || got.Usage.Detail == nil || got.Usage.Detail.ServiceTier != wantTier {
					t.Fatalf("usage: %+v detail: %+v", got.Usage, got.Usage.Detail)
				}
				base := ledger.Prices{InputPerMillion: 1, OutputPerMillion: 1, CacheWritePerMillion: 1, CacheReadPerMillion: 1}
				tiers := []ledger.PriceTier{{ServiceTier: "anthropic:fast", InputPrice: 2, OutputPrice: 3, CacheWritePrice: 4, CacheReadPrice: 5, TokenPrices: map[string]money.Amount{"cache_write_5m": 6, "cache_write_1h": 7}}}
				priced, err := ledger.CalculatePriceV2(got.Usage, base, tiers, time.Now(), ledger.FixedPointScale, 0)
				want := money.Amount(5)
				if tc.fast {
					want = 23
				}
				if err != nil || priced.Cost != want {
					t.Fatalf("price=%+v err=%v want=%v", priced, err, want)
				}
			})
		}
	}
}

func TestAnthropicFastWithoutServiceTierAndRequestFallback(t *testing.T) {
	for _, speed := range []string{"fast", "standard", ""} {
		t.Run(speed, func(t *testing.T) {
			o := NewObserver(channel.FormatAnthropic, false, "application/json")
			// A requested premium tier is metadata, never an actual billing fact.
			o.detail.RequestedServiceTier = "fast"
			o.Write([]byte(`{"usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_creation_input_tokens":1000000,"cache_read_input_tokens":1000000,"speed":"`+speed+`"}}`), time.Now())
			got := o.Finish()
			tiers := []ledger.PriceTier{{ServiceTier: "anthropic:fast", InputPrice: 2, OutputPrice: 3, CacheWritePrice: 4, CacheReadPrice: 5}}
			base := ledger.Prices{InputPerMillion: 1, OutputPerMillion: 1, CacheWritePerMillion: 1, CacheReadPerMillion: 1}
			priced, err := ledger.CalculatePriceV2(got.Usage, base, tiers, time.Now(), ledger.FixedPointScale, 0)
			want := money.Amount(4)
			if speed == "fast" {
				want = 14
			}
			if err != nil || priced.Cost != want {
				t.Fatalf("price=%+v err=%v want=%v", priced, err, want)
			}
		})
	}
}
