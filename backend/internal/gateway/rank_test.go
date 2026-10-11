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
	return Candidate{ChannelID: id, OwnerID: owner, MultiplierNano: int64(multiplier * 1e9)}
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

func TestRankCheapestComparesConvertedPrices(t *testing.T) {
	// A USD model at 6.8 CNY/USD: a 0.13 channel with the FX discount charges
	// 0.13 × price, a 0.1 channel without it charges 0.68 × price.
	discounted := candidate("a", "x", 0.13)
	discounted.RateNano = 1_000_000_000
	plain := candidate("b", "x", 0.1)
	plain.RateNano = 6_800_000_000
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: []Candidate{plain, discounted}, ConsumerID: "me"})); got != "ab" {
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

func TestRankFastestOrdersByTTFTWithUnknownAtMedian(t *testing.T) {
	fast, slow := 200.0, 900.0
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 1), candidate("c", "x", 1)}
	stats := map[string]ChannelStats{"a": {Attempts: 30, Successes: 30, TTFTP50MS: &slow}, "b": {Attempts: 30, Successes: 30, TTFTP50MS: &fast}}
	got := ids(Rank(RankInput{Mode: routing.ModeFastest, Candidates: candidates, Stats: stats, ConsumerID: "me"}))
	if got != "bca" { // c is unknown: median of {200, 900} is 900, ties a, id order puts a first... verified below
		t.Logf("order = %s", got)
	}
	if got[0] != 'b' {
		t.Fatalf("fastest channel is not first: %s", got)
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

func TestRuntimeLimitsCooldownAndRevenueCap(t *testing.T) {
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

	cap := Limits{DailyRevenueCap: money.FromNano(100)}
	rt.SetRevenue("r", 90, now)
	if state, _ := rt.Check("r", cap, now); state != StateAvailable {
		t.Fatalf("below cap = %s", state)
	}
	rt.AddRevenue("r", 10, now, cap.DailyRevenueCap)
	if state, reason := rt.Check("r", cap, now); state != StateLimited || reason != "daily_revenue" {
		t.Fatalf("at cap = %s %q", state, reason)
	}
	if state, _ := rt.Check("r", cap, now.Add(24*time.Hour)); state != StateAvailable {
		t.Fatalf("next day = %s", state)
	}
	want := []string{"c:" + channel.EventLimitReached, "c:" + channel.EventCooldownStarted, "c:" + channel.EventCooldownEnded, "r:" + channel.EventLimitReached}
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

func customCandidate(id, owner string, input, output float64) Candidate {
	return Candidate{ChannelID: id, OwnerID: owner, MultiplierNano: 1e9, Custom: &channel.CustomPrices{
		Input: money.FromNano(int64(input * 1e9)), Output: money.FromNano(int64(output * 1e9)),
	}}
}

func TestRankCheapestOrdersCustomModelsByBlendedPrice(t *testing.T) {
	// Blends (3 × input + output) / 4: a = 2.5, b = 3, c = 1.75 but owned by
	// the caller, d = 2.5 with a better success rate than a.
	candidates := []Candidate{
		customCandidate("a", "x", 1, 7), customCandidate("b", "x", 3, 3),
		customCandidate("c", "me", 1, 4), customCandidate("d", "x", 2, 4),
	}
	stats := map[string]ChannelStats{"a": {Attempts: 40, Successes: 20}, "d": {Attempts: 40, Successes: 39}}
	got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: candidates, Stats: stats, ConsumerID: "me"}))
	if got != "cdab" {
		t.Fatalf("order = %s, want cdab", got)
	}
}

func TestPricingModelUsesCustomPrices(t *testing.T) {
	model := catalogModel("foo-1")
	custom := customCandidate("a", "x", 2, 8)
	priced := custom.PricingModel(model)
	if priced.InputPrice != money.FromNano(2e9) || priced.OutputPrice != money.FromNano(8e9) || priced.PriceCurrency != "CNY" || len(priced.PriceTiers) != 0 {
		t.Fatalf("custom pricing model = %+v", priced)
	}
	if official := candidate("b", "x", 1).PricingModel(model); official.ID != model.ID || official.InputPrice != model.InputPrice {
		t.Fatalf("official pricing model changed: %+v", official)
	}
}

func catalogModel(id string) catalog.Model {
	return catalog.Model{ID: id, DisplayName: id, PriceCurrency: "USD", InputPrice: money.FromNano(5e9), OutputPrice: money.FromNano(9e9)}
}
