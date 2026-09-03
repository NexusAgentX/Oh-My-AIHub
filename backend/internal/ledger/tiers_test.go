package ledger

import (
	"testing"
	"time"
)

func tierInt64(value int64) *int64 { return &value }
func tierInt16(value int16) *int16 { return &value }

func TestPriceTierTokenRangePredicateIsHalfOpen(t *testing.T) {
	lower := PriceTier{MinPromptTokens: tierInt64(200_000)}
	if lower.Matches(199_999, time.Now()) {
		t.Fatal("prompt below min matched")
	}
	if !lower.Matches(200_000, time.Now()) {
		t.Fatal("prompt equal to min did not match")
	}
	upper := PriceTier{MaxPromptTokens: tierInt64(200_000)}
	if upper.Matches(200_000, time.Now()) {
		t.Fatal("prompt equal to exclusive max matched")
	}
	if !upper.Matches(199_999, time.Now()) {
		t.Fatal("prompt below max did not match")
	}
}

func TestPriceTierWeekdayWindowMatchesDeepSeekPeakHours(t *testing.T) {
	peak := PriceTier{
		Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3, 4, 5},
		StartMinute: tierInt16(9 * 60), EndMinute: tierInt16(12 * 60),
	}
	// 2026-09-07 is a Monday in Asia/Shanghai.
	cases := []struct {
		at   time.Time
		want bool
		note string
	}{
		{time.Date(2026, 9, 7, 2, 0, 0, 0, time.UTC), true, "Mon 10:00 Shanghai inside window"},
		{time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC), true, "Mon 09:00 Shanghai start inclusive"},
		{time.Date(2026, 9, 7, 3, 59, 0, 0, time.UTC), true, "Mon 11:59 Shanghai inside window"},
		{time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC), false, "Mon 12:00 Shanghai end exclusive"},
		{time.Date(2026, 9, 7, 5, 0, 0, 0, time.UTC), false, "Mon 13:00 Shanghai lunch valley"},
		{time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC), false, "Sat 10:00 Shanghai weekend valley"},
		{time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC), false, "Mon 24:00+ Shanghai outside"},
	}
	for _, testCase := range cases {
		if got := peak.Matches(0, testCase.at); got != testCase.want {
			t.Fatalf("%s: Matches = %v, want %v", testCase.note, got, testCase.want)
		}
	}
}

func TestPriceTierWindowCrossingMidnightBelongsToStartDay(t *testing.T) {
	nightly := PriceTier{
		Timezone: "UTC", Weekdays: []int{5},
		StartMinute: tierInt16(22 * 60), EndMinute: tierInt16(6 * 60),
	}
	// 2026-09-11 is a Friday, 2026-09-12 a Saturday.
	cases := []struct {
		at   time.Time
		want bool
		note string
	}{
		{time.Date(2026, 9, 11, 23, 0, 0, 0, time.UTC), true, "Fri 23:00 inside start-day window"},
		{time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC), true, "Sat 02:00 belongs to Friday window"},
		{time.Date(2026, 9, 12, 6, 0, 0, 0, time.UTC), false, "Sat 06:00 end exclusive"},
		{time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC), false, "Sat 07:00 after window"},
		{time.Date(2026, 9, 10, 23, 0, 0, 0, time.UTC), false, "Thu 23:00 weekday not in set"},
		{time.Date(2026, 9, 12, 23, 0, 0, 0, time.UTC), false, "Sat 23:00 start day is Saturday"},
	}
	for _, testCase := range cases {
		if got := nightly.Matches(0, testCase.at); got != testCase.want {
			t.Fatalf("%s: Matches = %v, want %v", testCase.note, got, testCase.want)
		}
	}
}

func TestPriceTierWeekdayOnlyPredicateMatchesWholeDays(t *testing.T) {
	weekend := PriceTier{Timezone: "UTC", Weekdays: []int{6, 7}}
	if !weekend.Matches(0, time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("Saturday did not match weekend tier")
	}
	if weekend.Matches(0, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("Monday matched weekend tier")
	}
}

func TestPriceTierInvalidTimezoneFailsClosed(t *testing.T) {
	tier := PriceTier{
		Timezone: "Not/AZone", StartMinute: tierInt16(0), EndMinute: tierInt16(60),
	}
	if tier.Matches(0, time.Now()) {
		t.Fatal("tier with invalid timezone matched")
	}
}

func TestSelectPriceTierFirstMatchWinsAndFallsBackToDefault(t *testing.T) {
	defaults := OfficialPricesV1{InputPerMillion: mustPriceAmount(t, "1")}
	longContext := PriceTier{MinPromptTokens: tierInt64(200_000), InputPrice: mustPriceAmount(t, "2")}
	peak := PriceTier{Timezone: "UTC", StartMinute: tierInt16(0), EndMinute: tierInt16(1439), InputPrice: mustPriceAmount(t, "3")}
	tiers := []PriceTier{longContext, peak}

	prices, seq := SelectPriceTier(defaults, tiers, 250_000, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if seq != 1 || prices.InputPerMillion != mustPriceAmount(t, "2") {
		t.Fatalf("long-context request selected seq %d prices %v", seq, prices)
	}
	prices, seq = SelectPriceTier(defaults, tiers, 1_000, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if seq != 2 || prices.InputPerMillion != mustPriceAmount(t, "3") {
		t.Fatalf("short request selected seq %d prices %v", seq, prices)
	}
	prices, seq = SelectPriceTier(defaults, tiers, 1_000, time.Date(2026, 9, 7, 23, 59, 30, 0, time.UTC))
	if seq != 0 || prices.InputPerMillion != mustPriceAmount(t, "1") {
		t.Fatalf("default fallback selected seq %d prices %v", seq, prices)
	}
}

func TestPromptSideTokensSumsPromptBucketsAndIgnoresOutput(t *testing.T) {
	usage := UsageV1{InputTokens: 100, OutputTokens: 999, CacheWriteTokens: 20, CacheReadTokens: 30}
	total, err := PromptSideTokens(usage)
	if err != nil || total != 150 {
		t.Fatalf("PromptSideTokens = %d, %v; want 150, nil", total, err)
	}
	if _, err := PromptSideTokens(UsageV1{InputTokens: -1}); err == nil {
		t.Fatal("negative usage accepted")
	}
}

func TestCalculatePriceV2EqualsV1WithoutTiers(t *testing.T) {
	usage := UsageV1{InputTokens: 1_234_567, OutputTokens: 987_654, CacheWriteTokens: 100_000, CacheReadTokens: 2_000_000}
	prices := OfficialPricesV1{
		InputPerMillion: mustPriceAmount(t, "1"), OutputPerMillion: mustPriceAmount(t, "2"),
		CacheWritePerMillion: mustPriceAmount(t, "0.5"), CacheReadPerMillion: mustPriceAmount(t, "0.25"),
	}
	v1, err := CalculatePriceV1(usage, prices, 1_500_000_000, 1_000_000, false)
	if err != nil {
		t.Fatalf("CalculatePriceV1: %v", err)
	}
	v2, err := CalculatePriceV2(usage, prices, nil, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), 1_500_000_000, 1_000_000, false)
	if err != nil {
		t.Fatalf("CalculatePriceV2: %v", err)
	}
	if v1.ProviderCharge != v2.ProviderCharge || v1.PlatformFee != v2.PlatformFee {
		t.Fatalf("v2 without tiers diverged: v1 %+v v2 %+v", v1, v2)
	}
	if v2.TierSeq != 0 {
		t.Fatalf("default selection reported tier seq %d", v2.TierSeq)
	}
}

func TestCalculatePriceV2ChargesWholeRequestAtGeminiLongContextTier(t *testing.T) {
	defaults := OfficialPricesV1{
		InputPerMillion: mustPriceAmount(t, "1.25"), OutputPerMillion: mustPriceAmount(t, "10"),
	}
	longTier := PriceTier{
		MinPromptTokens: tierInt64(200_000),
		InputPrice:      mustPriceAmount(t, "2.5"), OutputPrice: mustPriceAmount(t, "15"),
	}
	longUsage := UsageV1{InputTokens: 250_000, OutputTokens: 50_000}
	longResult, err := CalculatePriceV2(longUsage, defaults, []PriceTier{longTier}, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), 1_000_000_000, 0, false)
	if err != nil {
		t.Fatalf("CalculatePriceV2 long: %v", err)
	}
	if longResult.TierSeq != 1 || longResult.ProviderCharge != mustPriceAmount(t, "1.375") {
		t.Fatalf("long-context settlement = %+v, want tier 1 charge 1.375", longResult)
	}
	shortUsage := UsageV1{InputTokens: 150_000, OutputTokens: 50_000}
	shortResult, err := CalculatePriceV2(shortUsage, defaults, []PriceTier{longTier}, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), 1_000_000_000, 0, false)
	if err != nil {
		t.Fatalf("CalculatePriceV2 short: %v", err)
	}
	if shortResult.TierSeq != 0 || shortResult.ProviderCharge != mustPriceAmount(t, "0.6875") {
		t.Fatalf("short-context settlement = %+v, want default charge 0.6875", shortResult)
	}
}

func TestCalculatePriceV2DeepSeekPeakAndOffPeakPriceIdenticalTiers(t *testing.T) {
	defaults := OfficialPricesV1{
		InputPerMillion: mustPriceAmount(t, "1.5"), OutputPerMillion: mustPriceAmount(t, "4.5"),
	}
	morningPeak := PriceTier{
		Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3, 4, 5},
		StartMinute: tierInt16(9 * 60), EndMinute: tierInt16(12 * 60),
		InputPrice: mustPriceAmount(t, "3"), OutputPrice: mustPriceAmount(t, "9"),
	}
	afternoonPeak := morningPeak
	afternoonPeak.StartMinute, afternoonPeak.EndMinute = tierInt16(14*60), tierInt16(18*60)
	tiers := []PriceTier{morningPeak, afternoonPeak}
	usage := UsageV1{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	peak, err := CalculatePriceV2(usage, defaults, tiers, time.Date(2026, 9, 7, 2, 0, 0, 0, time.UTC), 1_000_000_000, 0, false)
	if err != nil {
		t.Fatalf("peak: %v", err)
	}
	if peak.TierSeq != 1 || peak.ProviderCharge != mustPriceAmount(t, "12") {
		t.Fatalf("peak settlement = %+v, want tier 1 charge 12", peak)
	}
	offPeak, err := CalculatePriceV2(usage, defaults, tiers, time.Date(2026, 9, 7, 5, 0, 0, 0, time.UTC), 1_000_000_000, 0, false)
	if err != nil {
		t.Fatalf("off-peak: %v", err)
	}
	if offPeak.TierSeq != 0 || offPeak.ProviderCharge != mustPriceAmount(t, "6") {
		t.Fatalf("off-peak settlement = %+v, want default charge 6", offPeak)
	}
}
