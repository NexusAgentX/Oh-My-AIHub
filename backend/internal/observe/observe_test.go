package observe

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func amount(nano int64) money.Amount { return money.FromNano(nano) }

func TestReconcileSortsEntriesIntoCategoriesAndEquates(t *testing.T) {
	flows := []TypeFlow{
		{Type: "api_call", Inflow: false, Amount: amount(-4_000)},
		{Type: "api_call", Inflow: true, Amount: amount(1_500)},
		{Type: "c2c_list", Inflow: false, Amount: amount(-3_000)},
		{Type: "c2c_release", Inflow: true, Amount: amount(2_000)},
		{Type: "c2c_return", Inflow: true, Amount: amount(500)},
		{Type: "admin_adjust", Inflow: true, Amount: amount(700)},
		{Type: "admin_adjust", Inflow: false, Amount: amount(-200)},
		{Type: "bad_debt_writeoff", Inflow: true, Amount: amount(900)},
	}
	period := reconcile(time.Unix(0, 0), time.Unix(100, 0), amount(1_000), amount(1_000-4_000+1_500-3_000+2_000+500+700-200+900), flows)
	if period.CallSpend != amount(-4_000) || period.ChannelIncome != amount(1_500) || period.C2CSell != amount(-3_000) || period.C2CBuy != amount(2_000) ||
		period.C2CReturn != amount(500) || period.Adjustments != amount(500) || period.WriteOffs != amount(900) {
		t.Fatalf("categories = %+v", period)
	}
	if period.Income != amount(1_500+2_000+500+700+900) || period.Spend != amount(4_000+3_000+200) || period.Difference != 0 {
		t.Fatalf("totals = %+v", period)
	}
	// A type the statement does not know shows up as a difference instead of vanishing.
	unknown := reconcile(time.Unix(0, 0), time.Unix(100, 0), 0, amount(10), []TypeFlow{{Type: "mystery", Inflow: true, Amount: amount(10)}})
	if unknown.Difference != amount(10) {
		t.Fatalf("unknown type difference = %v", unknown.Difference)
	}
}

func TestUnbilledAbnormalThresholds(t *testing.T) {
	cases := []struct {
		window CallWindow
		want   bool
	}{
		{CallWindow{Succeeded: 5, Unbilled: 5}, false},     // too few calls for a share
		{CallWindow{Succeeded: 100, Unbilled: 5}, false},   // exactly 5%
		{CallWindow{Succeeded: 100, Unbilled: 6}, true},    // over 5%
		{CallWindow{Succeeded: 1000, Unbilled: 20}, false}, // 2% and not over 20
		{CallWindow{Succeeded: 1000, Unbilled: 21}, true},  // over 20 calls
		{CallWindow{Succeeded: 19, Unbilled: 19}, false},   // 100% of a handful
	}
	for _, tc := range cases {
		if got := unbilledAbnormal(tc.window); got != tc.want {
			t.Errorf("%+v = %v, want %v", tc.window, got, tc.want)
		}
	}
}

func TestAttentionListsCriticalItemsFirst(t *testing.T) {
	data := OverviewData{
		C2C:           C2CCounts{OpenDisputes: 2},
		Checks:        Checks{ZeroSumOK: true, MissingCallCount: 1},
		Risks:         []Risk{{Kind: "over_limit"}, {Kind: "negative_long"}, {Kind: "negative_long"}},
		Failing:       []FailingChannel{{ID: "c1", Name: "shaky", Attempts: 20, Successes: 10}},
		Balances:      Balances{UserPositive: amount(100)},
		Concentration: Concentration{Top: []Holder{{Share: 0.6, Account: AccountRef{DisplayName: "老陈"}}}},
		Last24h:       CallWindow{Succeeded: 30, Unbilled: 21},
	}
	items := attention(data)
	kinds := make([]string, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.Kind)
	}
	want := []string{AttentionOverLimit, AttentionReconcile, AttentionDispute, AttentionNegative, AttentionConcentration, AttentionChannelFail, AttentionUnbilled}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v", kinds)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if items[0].Severity != "critical" || items[1].Severity != "critical" || items[2].Severity != "warning" {
		t.Fatalf("severities = %+v", items)
	}
	var quiet []AttentionItem = attention(OverviewData{Checks: Checks{ZeroSumOK: true}})
	if len(quiet) != 0 {
		t.Fatalf("quiet = %+v", quiet)
	}
}

func TestRisksAndConcentration(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	long, recent := now.Add(-45*24*time.Hour), now.Add(-2*24*time.Hour)
	snapshot := Snapshot{
		Balances: Balances{UserPositive: amount(1_000)},
		Negative: []NegativeAccount{
			{Account: AccountRef{Username: "deep"}, Balance: amount(-150), CreditLimit: amount(100), NegativeSince: &long},
			{Account: AccountRef{Username: "fresh"}, Balance: amount(-50), CreditLimit: amount(100), NegativeSince: &recent},
		},
		Holders: []Holder{{Balance: amount(600)}, {Balance: amount(200)}},
	}
	risks, concentration := risksOf(snapshot, now)
	if len(risks) != 2 || risks[0].Kind != "over_limit" || risks[1].Kind != "negative_long" || *risks[1].NegativeDays != 45 || risks[0].Account.Username != "deep" {
		t.Fatalf("risks = %+v", risks)
	}
	if concentration.Top5Share == nil || *concentration.Top5Share != 0.8 || concentration.Top[0].Share != 0.6 {
		t.Fatalf("concentration = %+v", concentration)
	}
	_, empty := risksOf(Snapshot{}, now)
	if empty.Top5Share != nil {
		t.Fatalf("no circulation must have no share: %+v", empty)
	}
}

func TestBuildTrendAccumulatesBalancesPerDay(t *testing.T) {
	first := time.Date(2026, 10, 6, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	now := first.AddDate(0, 0, 2).Add(time.Hour)
	inputs := TrendInputs{
		Accounts: []AccountOpening{
			{ID: "u1", Kind: "user", Balance: amount(100)}, {ID: "u2", Kind: "user", Balance: amount(0)},
			{ID: "e", Kind: "system", SystemCode: "c2c_escrow"}, {ID: "p", Kind: "system", SystemCode: "platform_revenue"}, {ID: "b", Kind: "system", SystemCode: "bad_debt"},
		},
		Nets: map[string]map[string]money.Amount{
			"u1": {"2026-10-06": amount(-30), "2026-10-08": amount(-100)},
			"u2": {"2026-10-06": amount(-10), "2026-10-07": amount(10)},
			"e":  {"2026-10-06": amount(40)},
		},
		APIDaily: map[string]struct{ Volume, Fee money.Amount }{"2026-10-07": {amount(30), amount(3)}},
		C2CDaily: map[string]struct {
			Volume   money.Amount
			TotalFen int64
		}{"2026-10-08": {amount(2_000_000_000), 184}},
	}
	trend := buildTrend(inputs, first, now)
	if len(trend) != 3 {
		t.Fatalf("days = %d", len(trend))
	}
	day1, day2, day3 := trend[0], trend[1], trend[2]
	if day1.Day != "2026-10-06" || day1.Circulation != amount(70) || day1.CreditIssued != amount(10) || day1.Escrow != amount(40) {
		t.Fatalf("day1 = %+v", day1)
	}
	if day2.Circulation != amount(70) || day2.CreditIssued != 0 || day2.APIVolume != amount(30) || day2.APIFee != amount(3) || day2.C2CAvgPriceFen != nil {
		t.Fatalf("day2 = %+v", day2)
	}
	if day3.Circulation != 0 || day3.CreditIssued != amount(30) || day3.C2CVolume != amount(2_000_000_000) || day3.C2CAvgPriceFen == nil || *day3.C2CAvgPriceFen != 92 {
		t.Fatalf("day3 = %+v", day3)
	}
}

func TestFillDaysAddsEmptyDays(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	from := time.Date(2026, 10, 6, 5, 0, 0, 0, zone)
	rows := fillDays([]UsageRow{{Key: "2026-10-07", Label: "2026-10-07", Calls: 3}}, from, from.AddDate(0, 0, 3))
	if len(rows) != 4 || rows[0].Calls != 0 || rows[1].Calls != 3 || rows[3].Key != "2026-10-09" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestFillHoursCoversTheLast24Hours(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)
	hours := fillHours([]Bucket{{At: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Attempts: 4, Successes: 3}}, now)
	if len(hours) != 24 || hours[23].Attempts != 4 || hours[0].At != time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC) || hours[5].Attempts != 0 {
		t.Fatalf("hours = %+v", hours)
	}
}

func TestCallRowChargedOnlyOnceBooked(t *testing.T) {
	row := CallRow{Cost: amount(40), Fee: amount(4)}
	if row.Charged() != 0 || row.Revenue() != 0 {
		t.Fatalf("unbooked call charged %v / revenue %v", row.Charged(), row.Revenue())
	}
	booked := "tx"
	row.LedgerTxID = &booked
	if row.Charged() != amount(44) || row.Revenue() != amount(40) {
		t.Fatalf("booked call charged %v / revenue %v", row.Charged(), row.Revenue())
	}
}

// ---- the live feed never blocks the gateway ----

type scriptedEvents struct{ channel chan gateway.Event }

func (s scriptedEvents) Subscribe(int) (<-chan gateway.Event, func()) { return s.channel, func() {} }

type recordStore struct{ Store }

func (recordStore) GetCall(_ context.Context, id string) (CallRecord, error) {
	return CallRecord{CallRow: CallRow{ID: id, Outcome: OutcomeSucceeded}}, nil
}

type countingObserver struct{ events int }

func (o *countingObserver) Observe(gateway.Event, *CallRecord) { o.events++ }

func TestFeedDropsForSlowSubscribersAndKeepsServingOthers(t *testing.T) {
	source := scriptedEvents{channel: make(chan gateway.Event, 64)}
	feed := NewFeed(NewService(recordStore{}), source, slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer := &countingObserver{}
	feed.AddObserver(observer)
	slow, cancelSlow := feed.Subscribe(1)
	defer cancelSlow()
	fast, cancelFast := feed.Subscribe(64)
	defer cancelFast()

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { feed.Run(ctx); close(done) }()
	const total = 20
	for index := 0; index < total; index++ {
		source.channel <- gateway.Event{Kind: gateway.EventCallFinished, CallID: "c"}
	}
	deadline := time.After(5 * time.Second)
	for received := 0; received < total; received++ {
		select {
		case message := <-fast:
			if message.Kind != FeedFinished || message.Call.ID != "c" {
				t.Fatalf("message = %+v", message)
			}
		case <-deadline:
			t.Fatalf("fast subscriber got only %d of %d", received, total)
		}
	}
	// The slow subscriber holds one message at most; the rest were dropped without blocking.
	if len(slow) != 1 || feed.Dropped() != total-1 {
		t.Fatalf("slow buffer %d, dropped %d", len(slow), feed.Dropped())
	}
	stop()
	<-done
	if observer.events != total {
		t.Fatalf("observer saw %d events", observer.events)
	}
}
