package gateway

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
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

func TestRankDefaultNeverChoosesAChannel(t *testing.T) {
	if got := Rank(RankInput{Candidates: []Candidate{candidate("a", "owner", 1)}}); len(got) != 0 {
		t.Fatalf("unconfigured route selected %v", got)
	}
}

func TestRankManualDoesNotReorderByPriceHealthOrOwnership(t *testing.T) {
	got := Rank(RankInput{Order: []string{"c", "b", "a"}, Candidates: []Candidate{candidate("a", "me", 0), candidate("b", "owner", 1), candidate("c", "owner", 10)}})
	if ids(got) != "cba" {
		t.Fatalf("manual order changed: %s", ids(got))
	}
}

func TestRankManualFollowsOrderAndDropsUnlistedAndUnticked(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 1), candidate("c", "x", 1), candidate("new", "x", 1)}
	got := ids(Rank(RankInput{Order: []string{"c", "a", "b"}, Excluded: []string{"a"}, Candidates: candidates}))
	if got != "cb" {
		t.Fatalf("order = %s, want cb", got)
	}
}

func TestRankStickyMovesPreviousChannelToFront(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2), candidate("c", "x", 3)}
	got := ids(Rank(RankInput{Order: []string{"a", "b", "c"}, Candidates: candidates, Sticky: "c"}))
	if got != "cab" {
		t.Fatalf("order = %s, want cab", got)
	}
	if got := ids(Rank(RankInput{Order: []string{"a", "b", "c"}, Candidates: candidates, Sticky: "gone"})); got != "abc" {
		t.Fatalf("order = %s, want abc", got)
	}
}

func TestRankDropsUnavailableAndExcluded(t *testing.T) {
	candidates := []Candidate{candidate("a", "x", 1), candidate("b", "x", 2), candidate("c", "x", 3)}
	got := ids(Rank(RankInput{
		Order: []string{"a", "b", "c"}, Candidates: candidates, Excluded: []string{"c"},
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
	runtime := NewRuntime(nil)
	runtime.SetRevenue("channel", 10, now)
	runtime.AddRevenueAt("channel", 7, previous, now, 11)
	if state, _ := runtime.Check("channel", Limits{DailyRevenueCap: 11}, now); state != StateAvailable {
		t.Fatalf("previous-day revenue exhausted current day: %s", state)
	}
}
