package api

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// fakeObserveStore answers the observation queries handler tests reach from
// canned fixtures; every other query panics through the nil embedded
// interface, which the PostgreSQL integration tests cover instead.
type fakeObserveStore struct {
	observe.Store
	exported []observe.ExportEntry

	// Optional fixtures for the platform flow tests.
	channels     *fakeChannelStore
	calls        []observe.CallRecord
	transactions []observe.Transaction
	people       observe.AccountRef // the account the report fixtures talk about
	repaired     string             // transaction booked by a "charge" repair
}

func (*fakeObserveStore) UserPeriod(context.Context, string, time.Time, time.Time) (money.Amount, money.Amount, []observe.TypeFlow, error) {
	return 0, 0, nil, nil
}

func (*fakeObserveStore) UserTrendInputs(context.Context, string, time.Time, time.Time) (money.Amount, []observe.DayFlow, error) {
	return 0, nil, nil
}

func (*fakeObserveStore) EntrySummary(context.Context, observe.EntryFilter, bool, bool) (observe.EntrySummary, error) {
	return observe.EntrySummary{}, nil
}

func (s *fakeObserveStore) ExportEntries(context.Context, observe.EntryFilter, int) ([]observe.ExportEntry, error) {
	return s.exported, nil
}

// ---- calls ----

func (s *fakeObserveStore) matching(filter observe.CallFilter) []observe.CallRecord {
	var records []observe.CallRecord
	for _, record := range s.calls {
		if (filter.AccountID != "" && record.Account.ID != filter.AccountID) ||
			(filter.ScopeChannelID != "" && !touches(record, filter.ScopeChannelID)) ||
			(filter.Outcome != "" && record.Outcome != filter.Outcome) {
			continue
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records
}

func (s *fakeObserveStore) ListCalls(_ context.Context, filter observe.CallFilter, after *observe.CallCursor, limit int) ([]observe.CallRow, error) {
	var rows []observe.CallRow
	for _, record := range s.matching(filter) {
		if after != nil && !record.CreatedAt.Before(after.At) {
			continue
		}
		row := record.CallRow
		if filter.ScopeChannelID != "" {
			for _, attempt := range record.Attempts {
				if attempt.Channel != nil && attempt.Channel.ID == filter.ScopeChannelID {
					row.ScopeAttempts = append(row.ScopeAttempts, attempt)
				}
			}
		}
		rows = append(rows, row)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *fakeObserveStore) CallStats(_ context.Context, filter observe.CallFilter) (observe.CallStats, error) {
	var stats observe.CallStats
	p50, p95 := 300, 900
	stats.TTFTP50MS, stats.TTFTP95MS = &p50, &p95
	for _, record := range s.matching(filter) {
		stats.Calls++
		if observe.IsSuccess(record.Outcome) {
			stats.Succeeded++
		} else {
			stats.Failed++
		}
		stats.InputTokens += record.Usage.InputTokens
		stats.OutputTokens += record.Usage.OutputTokens
		stats.CacheTokens += record.Usage.CacheWriteTokens + record.Usage.CacheReadTokens
		stats.Charged += record.Charged()
	}
	return stats, nil
}

func (s *fakeObserveStore) GetCall(_ context.Context, id string) (observe.CallRecord, error) {
	for _, record := range s.calls {
		if record.ID == id {
			return record, nil
		}
	}
	return observe.CallRecord{}, observe.ErrNotFound
}

func (s *fakeObserveStore) ChannelOwners(_ context.Context, ids []string) (map[string]observe.ChannelOwner, error) {
	s.channels.mu.Lock()
	defer s.channels.mu.Unlock()
	owners := map[string]observe.ChannelOwner{}
	for _, item := range s.channels.channels {
		if slices.Contains(ids, item.ID) {
			owners[item.ID] = observe.ChannelOwner{ID: item.ID, OwnerID: item.Owner.ID, Name: item.Name}
		}
	}
	return owners, nil
}

func (s *fakeObserveStore) Usage(_ context.Context, query observe.UsageQuery) ([]observe.UsageRow, error) {
	key := time.Now().In(localtime.Zone).Format("2006-01-02")
	if query.GroupBy != "day" {
		key = "deepseek-chat"
	}
	return []observe.UsageRow{{
		Key: key, Label: key, Calls: 3, Succeeded: 2, InputTokens: 1200, OutputTokens: 300, CacheWriteTokens: 10, CacheReadTokens: 20,
		Amount: money.FromNano(4_500_000_000),
	}}, nil
}

func (s *fakeObserveStore) ChannelBrief(_ context.Context, id string) (observe.ChannelBrief, error) {
	s.channels.mu.Lock()
	defer s.channels.mu.Unlock()
	item, err := s.channels.find(id)
	if err != nil {
		return observe.ChannelBrief{}, observe.ErrNotFound
	}
	return observe.ChannelBrief{ID: item.ID, OwnerID: item.Owner.ID, Name: item.Name, DailyRevenueCap: item.Advanced.DailyRevenueCap}, nil
}

func (s *fakeObserveStore) ChannelStats(_ context.Context, id string, _, _, now time.Time) (observe.ChannelStatsData, error) {
	p50, p95, speed50, speed95 := 300, 900, 35.5, 60.25
	missing := (*int)(nil)
	status := 200
	model := "deepseek-chat"
	code, message := "upstream_error", "bad gateway"
	return observe.ChannelStatsData{
		Window:   observe.AttemptWindow{Attempts: 10, Successes: 9, TTFTP50MS: &p50, TTFTP95MS: &p95},
		Last24h:  observe.AttemptWindow{Attempts: 4, Successes: 4},
		Last7d:   observe.AttemptWindow{Attempts: 10, Successes: 9},
		SpeedP50: &speed50, SpeedP95: &speed95,
		Hourly:      []observe.Bucket{{At: now.Truncate(time.Hour), Attempts: 4, Successes: 4}},
		Daily:       []observe.DayBucket{{Day: now.In(localtime.Zone).Format("2006-01-02"), Attempts: 4, Successes: 4, Revenue: money.FromNano(2_000_000_000)}},
		Models:      []observe.ModelBucket{{ModelID: model, Attempts: 10, Successes: 9, Revenue: money.FromNano(2_000_000_000)}},
		StatusCodes: []observe.StatusBucket{{StatusCode: &status, Count: 9}, {StatusCode: missing, Count: 1}},
		Failures: []observe.Failure{{
			CallID: "30000000-0000-4000-8000-000000000009", CreatedAt: now.Add(-time.Hour), ModelID: &model,
			Attempt: observe.Attempt{StatusCode: &status, ErrorCode: &code, ErrorMessage: &message, EndReason: "upstream_error"},
		}},
		Revenue: money.FromNano(2_000_000_000), TodayRevenue: money.FromNano(1_000_000_000),
		Events: []observe.ChannelEvent{{ID: 7, Kind: "listed", Reason: "", CreatedAt: now.Add(-24 * time.Hour)}},
	}, nil
}

// ---- administrator reports ----

func (s *fakeObserveStore) Snapshot(_ context.Context, request observe.SnapshotRequest) (observe.Snapshot, error) {
	now := request.Now
	longAgo := now.Add(-40 * 24 * time.Hour)
	system := "platform_revenue"
	snapshot := observe.Snapshot{
		Balances: observe.Balances{
			UserPositive: money.FromNano(50_000_000_000), UserNegative: money.FromNano(-200_000_000_000), Escrow: money.FromNano(10_000_000_000),
			PlatformRevenue: money.FromNano(5_000_000_000), BadDebt: money.FromNano(1_000_000_000), TotalCreditLimit: money.FromNano(100_000_000_000),
			WriteOffs: 1, EscrowOrders: 2, EscrowTradesInProgress: 1,
		},
		Checks: observe.Checks{
			CheckedAt: now, ZeroSumOK: true,
			Mismatches: []observe.BalanceMismatch{{
				Account: observe.LedgerAccountRef{Kind: "user", Account: &s.people}, Balance: money.FromNano(2_000_000_000), EntriesTotal: money.FromNano(1_000_000_000),
			}, {
				Account: observe.LedgerAccountRef{Kind: "system", SystemCode: &system}, Balance: money.FromNano(1_000_000_000), EntriesTotal: money.FromNano(1_000_000_000),
			}},
			EscrowBalance: money.FromNano(10_000_000_000), OrdersTotal: money.FromNano(9_000_000_000), EscrowDifference: money.FromNano(1_000_000_000),
			MissingCalls: []observe.MissingCall{{
				CallID: "30000000-0000-4000-8000-000000000001", CreatedAt: now.Add(-time.Hour), Account: s.people,
				Channel: observe.ChannelRef{ID: "30000000-0000-4000-8000-0000000000c1", Name: "渠道"}, Outcome: "succeeded", Charged: money.FromNano(1_000_000_000),
			}},
			MissingCallCount: 1,
			MissingTrades: []observe.MissingTrade{{
				TradeID: "30000000-0000-4000-8000-0000000000d1", Status: "released", Amount: money.FromNano(5_000_000_000), ResolvedAt: &now,
			}},
			MissingTradeCount: 1,
		},
		Negative: []observe.NegativeAccount{{Account: s.people, Balance: money.FromNano(-200_000_000_000), CreditLimit: money.FromNano(100_000_000_000), NegativeSince: &longAgo, LastEntryAt: &now}},
		Holders:  []observe.Holder{{Account: s.people, Balance: money.FromNano(40_000_000_000)}},
	}
	if request.Trend {
		day := now.In(localtime.Zone).Format("2006-01-02")
		snapshot.Trend = observe.TrendInputs{
			Accounts: []observe.AccountOpening{
				{ID: "u1", Kind: "user", Balance: money.FromNano(10_000_000_000)},
				{ID: "u2", Kind: "user", Balance: money.FromNano(-4_000_000_000)},
				{ID: "e", Kind: "system", SystemCode: "c2c_escrow", Balance: money.FromNano(3_000_000_000)},
				{ID: "r", Kind: "system", SystemCode: "platform_revenue", Balance: money.FromNano(2_000_000_000)},
				{ID: "b", Kind: "system", SystemCode: "bad_debt", Balance: money.FromNano(1_000_000_000)},
			},
			Nets:     map[string]map[string]money.Amount{"u1": {day: money.FromNano(1_000_000_000)}},
			APIDaily: map[string]struct{ Volume, Fee money.Amount }{day: {Volume: money.FromNano(8_000_000_000), Fee: money.FromNano(80_000_000)}},
			C2CDaily: map[string]struct {
				Volume   money.Amount
				TotalFen int64
			}{day: {Volume: money.FromNano(20_000_000_000), TotalFen: 1840}},
		}
	}
	if request.Overview {
		snapshot.Overview = observe.OverviewInputs{
			Today:   observe.CallWindow{Calls: 12, Succeeded: 10, Unbilled: 25, Spend: money.FromNano(6_000_000_000), Fee: money.FromNano(60_000_000)},
			Last24h: observe.CallWindow{Calls: 20, Succeeded: 18},
			Failing: []observe.FailingChannel{{ID: "30000000-0000-4000-8000-0000000000c1", Name: "渠道", Attempts: 10, Successes: 2}},
			C2C:     observe.C2CCounts{OpenOrders: 2, AwaitingPayment: 1, OpenDisputes: 1},
			C2C24h:  observe.C2CWindow{Trades: 3, Volume: money.FromNano(30_000_000_000), TotalFen: 2760},
		}
	}
	return snapshot, nil
}

func (s *fakeObserveStore) ListTransactions(_ context.Context, _ observe.TxFilter, _ *observe.CallCursor, limit int) ([]observe.Transaction, error) {
	if len(s.transactions) > limit {
		return s.transactions[:limit], nil
	}
	return s.transactions, nil
}

func (s *fakeObserveStore) GetTransaction(_ context.Context, id string) (observe.Transaction, error) {
	for _, transaction := range s.transactions {
		if transaction.ID == id {
			return transaction, nil
		}
	}
	return observe.Transaction{}, observe.ErrNotFound
}

func (s *fakeObserveStore) Repair(_ context.Context, repair observe.Repair) (observe.RepairResult, error) {
	for index := range s.calls {
		if s.calls[index].ID != repair.CallID {
			continue
		}
		if s.calls[index].LedgerTxID != nil {
			return observe.RepairResult{}, observe.ErrNotRepairable
		}
		if repair.Action == observe.RepairCharge {
			tx := s.repaired
			s.calls[index].LedgerTxID = &tx
			return observe.RepairResult{TransactionID: tx}, nil
		}
		return observe.RepairResult{}, nil
	}
	return observe.RepairResult{}, observe.ErrNotFound
}
