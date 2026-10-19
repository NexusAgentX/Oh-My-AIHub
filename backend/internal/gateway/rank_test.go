package gateway

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

func candidate(id, owner string, multiplier float64) Candidate {
	return Candidate{ChannelID: id, OwnerID: owner, GroupID: "group", MultiplierNano: int64(multiplier * 1e9), TypicalCost: money.FromNano(int64(multiplier * 1e9)), TypicalCostKnown: true, TypicalUnit: "tokens", TypicalQuantity: 1_000_000}
}

func ids(list []Candidate) string {
	out := ""
	for _, c := range list {
		out += c.ChannelID
	}
	return out
}

func TestRankCheapestPutsOwnChannelFirstAndBreaksTiesBySuccessRate(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1.2), candidate("b", "x", 0.8), candidate("c", "me", 3), candidate("d", "x", 0.8)}
	stats := map[string]ChannelStats{"b": {Attempts: 40, Successes: 20}, "d": {Attempts: 40, Successes: 39}}
	got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates, Stats: stats, ConsumerID: "me"}))
	if got != "cdba" {
		t.Fatalf("order = %s, want cdba", got)
	}
}

func TestRankCheapestUsesComparableTypicalCostInsteadOfDiscountOrCurrency(t *testing.T) {
	a, b := candidate("a", "seller", 9), candidate("b", "seller", 0.1)
	a.TypicalCost, b.TypicalCost = 13, 68
	a.RateNano, b.RateNano = 7_000_000_000, 1_000_000_000
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: []Candidate{b, a}, ConsumerID: "me"})); got != "ab" {
		t.Fatalf("order = %s, want ab", got)
	}
}

func TestRankReliableUsesMedianForThinSamples(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 1), candidate("c", "x", 1), candidate("d", "x", 1)}
	stats := map[string]ChannelStats{
		"a": {Attempts: 100, Successes: 99}, "b": {Attempts: 100, Successes: 50},
		"c": {Attempts: 100, Successes: 90}, "d": {Attempts: 3, Successes: 0},
	}
	// median of (0.99, 0.5, 0.9) is 0.9, which d inherits and ties with c; the tie falls to the id.
	got := ids(Rank(RankInput{Mode: routing.ModeReliable, Candidates: candidates, Stats: stats, ConsumerID: "me"}))
	if got != "acdb" {
		t.Fatalf("order = %s, want acdb", got)
	}
}

func TestRankFastestUsesEffectiveOutputAndLeavesUnknownBehindMeasuredLatency(t *testing.T) {
	fast, slow := 200.0, 900.0
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 1), candidate("c", "x", 1)}
	stats := map[string]ChannelStats{"a": {Attempts: 30, Successes: 30, TTFTP50MS: &fast, FirstOutputP50MS: &slow, FirstOutputSamples: 30}, "b": {Attempts: 30, Successes: 30, TTFTP50MS: &slow, FirstOutputP50MS: &fast, FirstOutputSamples: 30}}
	got := ids(Rank(RankInput{Mode: routing.ModeFastest, Candidates: candidates, Stats: stats, ConsumerID: "me"}))
	if got != "bac" {
		t.Fatalf("first byte or unknown metric displaced effective output: %s", got)
	}
}

func TestRankSpeedIsIndependentOfLatencyAndDoesNotUseThinSamples(t *testing.T) {
	fast, slow, high, low := 200.0, 900.0, 160.0, 40.0
	candidates := []Candidate{candidate("a", "provider", 1), candidate("b", "provider", 1), candidate("c", "provider", 1)}
	stats := map[string]ChannelStats{
		"a": {FirstOutputP50MS: &fast, FirstOutputSamples: 30, OutputTPSP50: &low, OutputSpeedSamples: 30},
		"b": {FirstOutputP50MS: &slow, FirstOutputSamples: 30, OutputTPSP50: &high, OutputSpeedSamples: 30},
		"c": {OutputTPSP50: &high, OutputSpeedSamples: 1},
	}
	if got := ids(Rank(RankInput{Mode: routing.ModeSpeed, Candidates: candidates, Stats: stats})); got != "bac" {
		t.Fatal("speed borrowed latency/thin sample", got)
	}
	if got := ids(Rank(RankInput{Mode: routing.ModeFastest, Candidates: candidates, Stats: stats})); got != "abc" {
		t.Fatal("latency borrowed output speed", got)
	}
}

func TestRankGroupPricesUseExactTypicalCostAndKeepUnknownDistinctFromFree(t *testing.T) {
	candidates := []Candidate{
		{ChannelID: "a", GroupID: "g", OwnerID: "provider", MultiplierNano: 1, TypicalCost: 100, TypicalCostKnown: true},
		{ChannelID: "b", GroupID: "g", OwnerID: "provider", MultiplierNano: 9_000_000_000, TypicalCost: 25, TypicalCostKnown: true},
		{ChannelID: "c", GroupID: "g", OwnerID: "provider", TypicalCostKnown: true},
		{ChannelID: "d", GroupID: "g", OwnerID: "consumer"},
		{ChannelID: "e", GroupID: "g", OwnerID: "provider"},
	}
	for i := range candidates {
		candidates[i].TypicalUnit = "tokens"
		candidates[i].TypicalQuantity = 1_000_000
	}
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates, ConsumerID: "consumer"})); got != "cdbae" {
		t.Fatal("typical/zero/unknown cost ordering failed", got)
	}
	candidates = candidates[:2]
	candidates[0].TypicalCost = 9_007_199_254_740_994
	candidates[1].TypicalCost = 9_007_199_254_740_993
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates})); got != "ba" {
		t.Fatal("price comparison lost fixed-point precision", got)
	}
}

func TestRankManualFollowsOrderAndDropsUnlistedAndUnticked(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 1), candidate("c", "x", 1), candidate("new", "x", 1)}
	got := ids(Rank(RankInput{Mode: routing.ModeManual, Order: []string{"c", "a", "b"}, Excluded: []string{"a"}, Candidates: candidates}))
	if got != "cb" {
		t.Fatalf("order = %s, want cb", got)
	}
}

func TestRankStickyMovesPreviousChannelToFront(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2), candidate("c", "x", 3)}
	got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates, Sticky: "c"}))
	if got != "cab" {
		t.Fatalf("order = %s, want cab", got)
	}
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates, Sticky: "gone"})); got != "abc" {
		t.Fatalf("order = %s, want abc", got)
	}
}

func TestRankDropsUnavailableAndExcluded(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2), candidate("c", "x", 3)}
	got := ids(Rank(RankInput{
		Mode: routing.ModeCheapest, Candidates: candidates, Excluded: []string{"c"},
		Available: func(c Candidate) bool { return c.ChannelID != "a" },
	}))
	if got != "b" {
		t.Fatalf("order = %s, want b", got)
	}
}

func TestRuntimeLimitsAndCooldown(t *testing.T) {
	var events []string
	rt := NewRuntime(func(id, kind, reason string) { events = append(events, id+":"+kind) })
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	limits := Limits{Concurrency: 1, RPM: 2, CooldownAfter: 2, CooldownFor: time.Minute}

	release, ok, _ := rt.Begin("c", limits, now)
	if !ok {
		t.Fatal("first request refused")
	}
	if _, ok, reason := rt.Begin("c", limits, now); ok || reason != "concurrency" {
		t.Fatalf("second concurrent request = %v %q", ok, reason)
	}
	release()
	release() // releasing twice must not free a slot twice
	release2, ok, _ := rt.Begin("c", limits, now.Add(time.Second))
	if !ok {
		t.Fatal("request after release refused")
	}
	release2()
	if _, ok, reason := rt.Begin("c", limits, now.Add(2*time.Second)); ok || reason != "rpm" {
		t.Fatalf("third request in a minute = %v %q", ok, reason)
	}
	release3, ok, _ := rt.Begin("c", limits, now.Add(61*time.Second))
	if !ok {
		t.Fatal("request after the minute refused")
	}
	release3()

	rt.Failure("c", limits, now, "boom")
	if started := rt.Failure("c", limits, now, "boom"); !started {
		t.Fatal("cooldown did not start at the threshold")
	}
	if state, _ := rt.Check("c", limits, now.Add(30*time.Second)); state != StateCooldown {
		t.Fatalf("state during cooldown = %s", state)
	}
	if state, _ := rt.Check("c", limits, now.Add(2*time.Minute)); state != StateAvailable {
		t.Fatalf("state after cooldown = %s", state)
	}
	rt.Success("c")

	want := []string{"c:" + channel.EventLimitReached, "c:" + channel.EventCooldownStarted, "c:" + channel.EventCooldownEnded}
	for _, expected := range want {
		found := false
		for _, event := range events {
			found = found || event == expected
		}
		if !found {
			t.Fatalf("event %s missing from %v", expected, events)
		}
	}
}

func TestSpendCacheLoadsOncePerWindowAndAccumulates(t *testing.T) {
	cache := NewSpendCache()
	now := time.Date(2026, 3, 10, 5, 0, 0, 0, time.UTC)
	loads := 0
	load := func() (Spend, error) { loads++; return Spend{Today: 5, Month: 50, Total: 500}, nil }
	_, _ = cache.Get("k", now, load)
	cache.Add("k", 7, now)
	spend, _ := cache.Get("k", now, load)
	if loads != 1 || spend != (Spend{Today: 12, Month: 57, Total: 507}) {
		t.Fatalf("loads=%d spend=%#v", loads, spend)
	}
	if _, _ = cache.Get("k", now.Add(24*time.Hour), load); loads != 2 {
		t.Fatalf("a new day must reload, loads = %d", loads)
	}
}

func TestPricingModelRequiresMatchingFrozenGroupOffer(t *testing.T) {
	model := catalog.Model{ID: "main", PriceCurrency: "USD", InputPrice: 999_000_000_000}
	priced := quotedCandidate(t, Candidate{}, catalog.Model{ID: "main", PriceCurrency: "CNY", InputPrice: 2_000_000_000}).PricingModel(model)
	if priced.ID != model.ID || priced.InputPrice != 2_000_000_000 || priced.PriceCurrency != "CNY" {
		t.Fatalf("group pricing model = %+v", priced)
	}
	for _, c := range []Candidate{{}, {GroupID: "group"}, {GroupID: "different", GroupModelID: "group-main", Quotes: groupBook(t, "USD", 1)}} {
		if c.ValidOffering(model.ID) || c.PricingModel(model).InputPrice != 0 {
			t.Fatal("missing or mismatched group offer borrowed directory prices")
		}
	}
}

func TestLateSettlementUsesCallStartBudgetWindow(t *testing.T) {
	cache := NewSpendCache()
	previous := time.Date(2026, 3, 31, 15, 59, 0, 0, time.UTC) // Asia/Shanghai March 31
	now := previous.Add(2 * time.Minute)
	load := func() (Spend, error) { return Spend{Today: 5, Month: 50, Total: 500}, nil }
	_, _ = cache.Get("key", now, load)
	cache.Add("key", 7, previous)
	got, _ := cache.Get("key", now, load)
	if got != (Spend{Today: 5, Month: 50, Total: 507}) {
		t.Fatalf("late charge leaked into current window: %+v", got)
	}
}
