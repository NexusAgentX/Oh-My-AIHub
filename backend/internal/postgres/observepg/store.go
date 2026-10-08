// Package observepg is the PostgreSQL implementation of observe.Store: the
// read-only queries behind call lists, usage, channel statistics, points
// reports and the live ledger reconciliation, plus the administrator's repair
// of an unbooked call.
package observepg

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

var _ observe.Store = (*Store)(nil)

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int32Of(value *int) *int32 {
	if value == nil {
		return nil
	}
	converted := int32(*value)
	return &converted
}

func intOf(value *int32) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}

func nanoOf(value *money.Amount) *int64 {
	if value == nil {
		return nil
	}
	nano := value.Nano()
	return &nano
}

// millis converts a percentile result (negative means "no samples") to whole
// milliseconds.
func millis(value float64) *int {
	if value < 0 {
		return nil
	}
	rounded := int(math.Round(value))
	return &rounded
}

func nonNegative(value float64) *float64 {
	if value < 0 {
		return nil
	}
	return &value
}

// asTime reads a nullable timestamp that sqlc surfaced as an interface value.
func asTime(value any) *time.Time {
	if at, ok := value.(time.Time); ok {
		return &at
	}
	return nil
}

func parseAttempts(raw []byte) []observe.Attempt {
	var attempts []observe.Attempt
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &attempts)
	}
	return attempts
}

// snapshot runs fn against one repeatable-read, read-only transaction so that
// several queries see the same state.
func (s *Store) snapshot(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	return fn(s.q.WithTx(tx))
}

// ---- calls ----

func callListParams(filter observe.CallFilter) ListCallsParams {
	return ListCallsParams{
		AccountID: nonEmpty(filter.AccountID), ScopeChannelID: nonEmpty(filter.ScopeChannelID), ScopeChannelText: nonEmpty(filter.ScopeChannelID),
		FinalChannelID: nonEmpty(filter.FinalChannelID), ApiKeyID: nonEmpty(filter.APIKeyID), Model: nonEmpty(filter.Model),
		Format: nonEmpty(filter.Format), Outcome: nonEmpty(filter.Outcome), Tag: nonEmpty(filter.Tag), RequestID: nonEmpty(filter.RequestID),
		FromTime: filter.From, ToTime: filter.To, MinDuration: int32Of(filter.MinDurationMS), MaxDuration: int32Of(filter.MaxDurationMS),
		MinTokens: filter.MinTokens, MaxTokens: filter.MaxTokens, MinCharged: nanoOf(filter.MinCharged), MaxCharged: nanoOf(filter.MaxCharged),
	}
}

func (s *Store) ListCalls(ctx context.Context, filter observe.CallFilter, after *observe.CallCursor, limit int) ([]observe.CallRow, error) {
	params := callListParams(filter)
	params.RowLimit = int32(limit)
	if after != nil {
		params.BeforeAt, params.BeforeID = &after.At, &after.ID
	}
	rows, err := s.q.ListCalls(ctx, params)
	if err != nil {
		return nil, err
	}
	calls := make([]observe.CallRow, 0, len(rows))
	for _, row := range rows {
		call := observe.CallRow{
			ID: row.ID, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt,
			Account: observe.AccountRef{ID: row.AccountID, Username: row.AccountUsername, DisplayName: row.AccountDisplayName},
			ModelID: row.ModelID, RequestedModel: row.RequestedModel, Format: row.Format, Stream: row.Stream, Tag: row.Tag, Outcome: row.Outcome,
			Usage: observe.Usage{InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CacheWriteTokens: row.CacheWriteTokens, CacheReadTokens: row.CacheReadTokens},
			Cost:  row.CostNano, Fee: row.FeeNano, LedgerTxID: row.LedgerTxID, TTFTMS: intOf(row.TtftMs), DurationMS: intOf(row.DurationMs),
			AttemptCount: int(row.AttemptCount), ScopeAttempts: parseAttempts(row.ScopeAttempts),
		}
		if row.ApiKeyID != nil {
			name := ""
			if row.ApiKeyName != nil {
				name = *row.ApiKeyName
			}
			call.Key = &observe.KeyRef{ID: *row.ApiKeyID, Name: name}
		}
		if row.FinalChannelID != nil {
			name := ""
			if row.ChannelName != nil {
				name = *row.ChannelName
			}
			call.Channel = &observe.ChannelRef{ID: *row.FinalChannelID, Name: name}
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func (s *Store) CallStats(ctx context.Context, filter observe.CallFilter) (observe.CallStats, error) {
	list := callListParams(filter)
	row, err := s.q.CallStats(ctx, CallStatsParams{
		AccountID: list.AccountID, ScopeChannelID: list.ScopeChannelID, ScopeChannelText: list.ScopeChannelText, FinalChannelID: list.FinalChannelID,
		ApiKeyID: list.ApiKeyID, Model: list.Model, Format: list.Format, Outcome: list.Outcome, Tag: list.Tag, RequestID: list.RequestID,
		FromTime: list.FromTime, ToTime: list.ToTime, MinDuration: list.MinDuration, MaxDuration: list.MaxDuration,
		MinTokens: list.MinTokens, MaxTokens: list.MaxTokens, MinCharged: list.MinCharged, MaxCharged: list.MaxCharged,
	})
	if err != nil {
		return observe.CallStats{}, err
	}
	return observe.CallStats{
		Calls: row.Calls, Succeeded: row.Succeeded, Failed: row.Failed, Charged: money.FromNano(row.ChargedNano),
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CacheTokens: row.CacheTokens,
		TTFTP50MS: millis(row.TtftP50Ms), TTFTP95MS: millis(row.TtftP95Ms),
	}, nil
}

func (s *Store) GetCall(ctx context.Context, id string) (observe.CallRecord, error) {
	row, err := s.q.GetCall(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return observe.CallRecord{}, observe.ErrNotFound
	}
	if err != nil {
		return observe.CallRecord{}, err
	}
	call := row.Call
	record := observe.CallRecord{
		CallRow: observe.CallRow{
			ID: call.ID, CreatedAt: call.CreatedAt, CompletedAt: call.CompletedAt,
			Account: observe.AccountRef{ID: call.AccountID, Username: row.AccountUsername, DisplayName: row.AccountDisplayName},
			ModelID: call.ModelID, RequestedModel: call.RequestedModel, Format: call.Format, Stream: call.Stream, Tag: call.Tag, Outcome: call.Outcome,
			Usage: observe.Usage{InputTokens: call.InputTokens, OutputTokens: call.OutputTokens, CacheWriteTokens: call.CacheWriteTokens, CacheReadTokens: call.CacheReadTokens},
			Cost:  call.CostNano, Fee: call.FeeNano, LedgerTxID: call.LedgerTxID, TTFTMS: intOf(call.TtftMs), DurationMS: intOf(call.DurationMs),
		},
		RoutingMode: call.RoutingMode, RoutingSource: call.RoutingSource, ClientUserAgent: call.ClientUserAgent,
		Attempts: parseAttempts(call.Attempts), PriceSnapshot: json.RawMessage(call.PriceSnapshot), TokensPerSecond: call.OutputTokensPerSecond,
		IntervalP50MS: intOf(call.InterTokenP50Ms), IntervalP95MS: intOf(call.InterTokenP95Ms), ResponseBytes: call.ResponseBytes,
	}
	record.AttemptCount = len(record.Attempts)
	if call.ApiKeyID != nil {
		name := ""
		if row.ApiKeyName != nil {
			name = *row.ApiKeyName
		}
		record.Key = &observe.KeyRef{ID: *call.ApiKeyID, Name: name}
	}
	if call.FinalChannelID != nil {
		name := ""
		if row.ChannelName != nil {
			name = *row.ChannelName
		}
		record.Channel = &observe.ChannelRef{ID: *call.FinalChannelID, Name: name}
	}
	return record, nil
}

func (s *Store) ChannelOwners(ctx context.Context, ids []string) (map[string]observe.ChannelOwner, error) {
	if len(ids) == 0 {
		return map[string]observe.ChannelOwner{}, nil
	}
	rows, err := s.q.ChannelOwners(ctx, ids)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]observe.ChannelOwner, len(rows))
	for _, row := range rows {
		owners[row.ID] = observe.ChannelOwner{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name}
	}
	return owners, nil
}

// ---- usage ----

func (s *Store) Usage(ctx context.Context, query observe.UsageQuery) ([]observe.UsageRow, error) {
	type usageRow struct {
		key, label                                       string
		calls, succeeded                                 int64
		input, output, cacheWrite, cacheRead, amountNano int64
	}
	var rows []usageRow
	if query.Revenue {
		result, err := s.q.UsageRevenue(ctx, UsageRevenueParams{
			GroupBy: query.GroupBy, OwnerID: query.AccountID, FromTime: query.From, ToTime: query.To,
			ChannelID: nonEmpty(query.ChannelID), Model: nonEmpty(query.Model),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range result {
			rows = append(rows, usageRow{row.GroupKey, row.Label, row.Calls, row.Succeeded, row.InputTokens, row.OutputTokens, row.CacheWriteTokens, row.CacheReadTokens, row.AmountNano})
		}
	} else {
		result, err := s.q.UsageSpend(ctx, UsageSpendParams{
			GroupBy: query.GroupBy, AccountID: query.AccountID, FromTime: query.From, ToTime: query.To,
			ApiKeyID: nonEmpty(query.APIKeyID), Model: nonEmpty(query.Model), Tag: nonEmpty(query.Tag),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range result {
			rows = append(rows, usageRow{row.GroupKey, row.Label, row.Calls, row.Succeeded, row.InputTokens, row.OutputTokens, row.CacheWriteTokens, row.CacheReadTokens, row.AmountNano})
		}
	}
	items := make([]observe.UsageRow, 0, len(rows))
	for _, row := range rows {
		label := row.label
		if label == "" || (query.GroupBy != "key" && query.GroupBy != "channel") {
			label = row.key
		}
		items = append(items, observe.UsageRow{
			Key: row.key, Label: label, Calls: row.calls, Succeeded: row.succeeded, InputTokens: row.input, OutputTokens: row.output,
			CacheWriteTokens: row.cacheWrite, CacheReadTokens: row.cacheRead, Amount: money.FromNano(row.amountNano),
		})
	}
	return items, nil
}

// ---- channel statistics ----

func (s *Store) ChannelBrief(ctx context.Context, id string) (observe.ChannelBrief, error) {
	row, err := s.q.GetChannelBrief(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return observe.ChannelBrief{}, observe.ErrNotFound
	}
	if err != nil {
		return observe.ChannelBrief{}, err
	}
	return observe.ChannelBrief{ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, DailyRevenueCap: row.DailyRevenueCapNano}, nil
}

func (s *Store) attemptWindow(ctx context.Context, id string, from, to time.Time) (observe.AttemptWindow, error) {
	row, err := s.q.ChannelAttemptStats(ctx, ChannelAttemptStatsParams{FromTime: from, ToTime: to, ChannelText: id})
	if err != nil {
		return observe.AttemptWindow{}, err
	}
	return observe.AttemptWindow{Attempts: row.Attempts, Successes: row.Successes, TTFTP50MS: millis(row.TtftP50Ms), TTFTP95MS: millis(row.TtftP95Ms)}, nil
}

func (s *Store) ChannelStats(ctx context.Context, id string, from, to, now time.Time) (observe.ChannelStatsData, error) {
	var data observe.ChannelStatsData
	var err error
	if data.Window, err = s.attemptWindow(ctx, id, from, to); err != nil {
		return data, err
	}
	if data.Last24h, err = s.attemptWindow(ctx, id, now.Add(-24*time.Hour), now.Add(time.Second)); err != nil {
		return data, err
	}
	if data.Last7d, err = s.attemptWindow(ctx, id, now.Add(-7*24*time.Hour), now.Add(time.Second)); err != nil {
		return data, err
	}
	speed, err := s.q.ChannelSpeed(ctx, ChannelSpeedParams{ChannelID: id, FromTime: from, ToTime: to})
	if err != nil {
		return data, err
	}
	data.SpeedP50, data.SpeedP95 = nonNegative(speed.P50), nonNegative(speed.P95)

	hourly, err := s.q.ChannelHourly(ctx, ChannelHourlyParams{FromTime: now.Truncate(time.Hour).Add(-23 * time.Hour), ToTime: now.Add(time.Second), ChannelText: id})
	if err != nil {
		return data, err
	}
	for _, row := range hourly {
		data.Hourly = append(data.Hourly, observe.Bucket{At: row.Hour, Attempts: row.Attempts, Successes: row.Successes})
	}

	daily, err := s.q.ChannelDaily(ctx, ChannelDailyParams{FromTime: from, ToTime: to, ChannelText: id})
	if err != nil {
		return data, err
	}
	revenueByDay, err := s.q.ChannelRevenueByDay(ctx, ChannelRevenueByDayParams{ChannelID: id, FromTime: from, ToTime: to})
	if err != nil {
		return data, err
	}
	days := map[string]*observe.DayBucket{}
	for _, row := range daily {
		days[row.Day] = &observe.DayBucket{Day: row.Day, Attempts: row.Attempts, Successes: row.Successes}
	}
	for _, row := range revenueByDay {
		bucket := days[row.Day]
		if bucket == nil {
			bucket = &observe.DayBucket{Day: row.Day}
			days[row.Day] = bucket
		}
		bucket.Revenue = money.FromNano(row.RevenueNano)
		data.Revenue += bucket.Revenue
	}
	for _, bucket := range days {
		data.Daily = append(data.Daily, *bucket)
	}

	models, err := s.q.ChannelModels(ctx, ChannelModelsParams{FromTime: from, ToTime: to, ChannelText: id})
	if err != nil {
		return data, err
	}
	revenueByModel, err := s.q.ChannelRevenueByModel(ctx, ChannelRevenueByModelParams{ChannelID: id, FromTime: from, ToTime: to})
	if err != nil {
		return data, err
	}
	byModel := map[string]*observe.ModelBucket{}
	order := []string{}
	for _, row := range models {
		byModel[row.ModelID] = &observe.ModelBucket{ModelID: row.ModelID, Attempts: row.Attempts, Successes: row.Successes}
		order = append(order, row.ModelID)
	}
	for _, row := range revenueByModel {
		bucket := byModel[row.ModelID]
		if bucket == nil {
			bucket = &observe.ModelBucket{ModelID: row.ModelID}
			byModel[row.ModelID] = bucket
			order = append(order, row.ModelID)
		}
		bucket.Revenue = money.FromNano(row.RevenueNano)
	}
	for _, name := range order {
		data.Models = append(data.Models, *byModel[name])
	}

	codes, err := s.q.ChannelStatusCodes(ctx, ChannelStatusCodesParams{FromTime: from, ToTime: to, ChannelText: id})
	if err != nil {
		return data, err
	}
	for _, row := range codes {
		bucket := observe.StatusBucket{Count: row.Count}
		if row.StatusCode != 0 {
			code := int(row.StatusCode)
			bucket.StatusCode = &code
		}
		data.StatusCodes = append(data.StatusCodes, bucket)
	}

	failures, err := s.q.ChannelRecentFailures(ctx, ChannelRecentFailuresParams{FromTime: from, ToTime: to, ChannelText: id})
	if err != nil {
		return data, err
	}
	for _, row := range failures {
		var attempt observe.Attempt
		_ = json.Unmarshal(row.Attempt, &attempt)
		data.Failures = append(data.Failures, observe.Failure{CallID: row.ID, CreatedAt: row.CreatedAt, ModelID: row.ModelID, Attempt: attempt})
	}

	today, err := s.q.ChannelRevenueSince(ctx, ChannelRevenueSinceParams{ChannelID: id, Since: localtime.DayStart(now)})
	if err != nil {
		return data, err
	}
	data.TodayRevenue = money.FromNano(today)

	events, err := s.q.ListChannelEvents(ctx, id)
	if err != nil {
		return data, err
	}
	for _, row := range events {
		data.Events = append(data.Events, observe.ChannelEvent{ID: row.ID, Kind: row.Kind, Reason: row.Reason, CreatedAt: row.CreatedAt})
	}
	return data, nil
}

// ---- user points ----

func (s *Store) UserPeriod(ctx context.Context, accountID string, from, to time.Time) (opening, closing money.Amount, flows []observe.TypeFlow, err error) {
	err = s.snapshot(ctx, func(q *Queries) error {
		before, err := q.UserBalanceBefore(ctx, UserBalanceBeforeParams{AccountID: accountID, Before: from})
		if err != nil {
			return err
		}
		after, err := q.UserBalanceBefore(ctx, UserBalanceBeforeParams{AccountID: accountID, Before: to})
		if err != nil {
			return err
		}
		rows, err := q.UserPeriodByType(ctx, UserPeriodByTypeParams{AccountID: accountID, FromTime: from, ToTime: to})
		if err != nil {
			return err
		}
		opening, closing = money.FromNano(before), money.FromNano(after)
		for _, row := range rows {
			flows = append(flows, observe.TypeFlow{Type: row.Type, Inflow: row.Inflow, Amount: money.FromNano(row.AmountNano)})
		}
		return nil
	})
	return
}

func (s *Store) UserTrendInputs(ctx context.Context, accountID string, from, to time.Time) (opening money.Amount, days []observe.DayFlow, err error) {
	err = s.snapshot(ctx, func(q *Queries) error {
		before, err := q.UserBalanceBefore(ctx, UserBalanceBeforeParams{AccountID: accountID, Before: from})
		if err != nil {
			return err
		}
		rows, err := q.UserDailyNets(ctx, UserDailyNetsParams{AccountID: accountID, FromTime: from, ToTime: to})
		if err != nil {
			return err
		}
		opening = money.FromNano(before)
		for _, row := range rows {
			days = append(days, observe.DayFlow{Day: row.Day, Income: money.FromNano(row.IncomeNano), Spend: money.FromNano(row.SpendNano), Net: money.FromNano(row.NetNano)})
		}
		return nil
	})
	return
}

func (s *Store) EntrySummary(ctx context.Context, filter observe.EntryFilter, byDay, byKey bool) (observe.EntrySummary, error) {
	var summary observe.EntrySummary
	if byDay {
		rows, err := s.q.UserEntriesByDay(ctx, UserEntriesByDayParams{
			AccountID: filter.AccountID, Type: filter.Type, ApiKeyID: filter.APIKeyID, FromTime: filter.From, ToTime: filter.To,
		})
		if err != nil {
			return summary, err
		}
		for _, row := range rows {
			summary.ByDay = append(summary.ByDay, observe.DayFlow{Day: row.Day, Income: money.FromNano(row.IncomeNano), Spend: money.FromNano(row.SpendNano), Net: money.FromNano(row.NetNano)})
		}
	}
	if byKey {
		rows, err := s.q.UserEntriesByKey(ctx, UserEntriesByKeyParams{
			AccountID: filter.AccountID, Type: filter.Type, ApiKeyID: filter.APIKeyID, FromTime: filter.From, ToTime: filter.To,
		})
		if err != nil {
			return summary, err
		}
		for _, row := range rows {
			item := observe.KeySpend{Spend: money.FromNano(row.SpendNano), Entries: row.Entries}
			if row.ApiKeyID != nil {
				item.Key = &observe.KeyRef{ID: *row.ApiKeyID, Name: row.ApiKeyName}
			}
			summary.ByKey = append(summary.ByKey, item)
		}
	}
	return summary, nil
}

func (s *Store) ExportEntries(ctx context.Context, filter observe.EntryFilter, limit int) ([]observe.ExportEntry, error) {
	rows, err := s.q.ExportUserEntries(ctx, ExportUserEntriesParams{
		AccountID: filter.AccountID, Type: filter.Type, ApiKeyID: filter.APIKeyID, FromTime: filter.From, ToTime: filter.To, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]observe.ExportEntry, 0, len(rows))
	for _, row := range rows {
		entry := observe.ExportEntry{CreatedAt: row.CreatedAt, Type: row.Type, Reason: row.Reason, Amount: row.AmountNano, BalanceAfter: row.BalanceAfterNano}
		if row.RelatedType != nil && row.RelatedID != nil {
			entry.RelatedType, entry.RelatedID = *row.RelatedType, *row.RelatedID
		}
		if row.ApiKeyName != nil {
			entry.KeyName = *row.ApiKeyName
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ---- administrator snapshot ----

func accountRef(id, username, displayName *string) *observe.AccountRef {
	if id == nil || username == nil {
		return nil
	}
	ref := observe.AccountRef{ID: *id, Username: *username}
	if displayName != nil {
		ref.DisplayName = *displayName
	}
	return &ref
}

func (s *Store) Snapshot(ctx context.Context, request observe.SnapshotRequest) (observe.Snapshot, error) {
	var snapshot observe.Snapshot
	err := s.snapshot(ctx, func(q *Queries) error {
		balances, err := q.LedgerBalances(ctx)
		if err != nil {
			return err
		}
		limit, err := q.TotalCreditLimit(ctx)
		if err != nil {
			return err
		}
		writeOffs, err := q.CountWriteOffs(ctx)
		if err != nil {
			return err
		}
		escrow, err := q.EscrowCounts(ctx)
		if err != nil {
			return err
		}
		snapshot.Balances = observe.Balances{
			UserPositive: money.FromNano(balances.UserPositiveNano), UserNegative: money.FromNano(balances.UserNegativeNano),
			Escrow: money.FromNano(balances.EscrowNano), PlatformRevenue: money.FromNano(balances.PlatformRevenueNano),
			BadDebt: money.FromNano(balances.BadDebtNano), Total: money.FromNano(balances.TotalNano), TotalCreditLimit: money.FromNano(limit),
			WriteOffs: writeOffs, EscrowOrders: escrow.Orders, EscrowTradesInProgress: escrow.TradesInProgress,
		}
		if snapshot.Checks, err = checks(ctx, q, snapshot.Balances, request.Now); err != nil {
			return err
		}
		if !request.Trend && !request.Overview {
			return nil
		}
		negatives, err := q.NegativeAccounts(ctx)
		if err != nil {
			return err
		}
		for _, row := range negatives {
			snapshot.Negative = append(snapshot.Negative, observe.NegativeAccount{
				Account: observe.AccountRef{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName}, Balance: row.BalanceNano,
				CreditLimit: row.CreditLimitNano, NegativeSince: asTime(row.NegativeSince), LastEntryAt: asTime(row.LastEntryAt),
			})
		}
		holders, err := q.TopHolders(ctx)
		if err != nil {
			return err
		}
		for _, row := range holders {
			snapshot.Holders = append(snapshot.Holders, observe.Holder{
				Account: observe.AccountRef{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName}, Balance: row.BalanceNano,
			})
		}
		if request.Trend {
			if snapshot.Trend, err = trendInputs(ctx, q, request.TrendFrom, request.TrendTo); err != nil {
				return err
			}
		}
		if request.Overview {
			if snapshot.Overview, err = overviewInputs(ctx, q, request.Now); err != nil {
				return err
			}
		}
		return nil
	})
	return snapshot, err
}

func checks(ctx context.Context, q *Queries, balances observe.Balances, now time.Time) (observe.Checks, error) {
	result := observe.Checks{CheckedAt: now, ZeroSumTotal: balances.Total, ZeroSumOK: balances.Total == 0}
	mismatches, err := q.AccountBalanceMismatches(ctx)
	if err != nil {
		return result, err
	}
	for _, row := range mismatches {
		ref := observe.LedgerAccountRef{Kind: row.Kind, SystemCode: row.SystemCode, Account: accountRef(row.AccountID, row.Username, row.DisplayName)}
		result.Mismatches = append(result.Mismatches, observe.BalanceMismatch{Account: ref, Balance: row.BalanceNano, EntriesTotal: money.FromNano(row.EntriesNano)})
	}
	orders, err := q.EscrowOrdersTotal(ctx)
	if err != nil {
		return result, err
	}
	result.EscrowBalance, result.OrdersTotal = balances.Escrow, money.FromNano(orders)
	result.EscrowDifference = money.FromNano(balances.Escrow.Nano() - orders)

	if result.MissingCallCount, err = q.CountUnbilledCalls(ctx); err != nil {
		return result, err
	}
	calls, err := q.ListUnbilledCalls(ctx)
	if err != nil {
		return result, err
	}
	for _, row := range calls {
		result.MissingCalls = append(result.MissingCalls, observe.MissingCall{
			CallID: row.ID, CreatedAt: row.CreatedAt, Outcome: row.Outcome, Charged: money.FromNano(row.CostNano.Nano() + row.FeeNano.Nano()),
			Account: observe.AccountRef{ID: row.AccountID, Username: row.Username, DisplayName: row.DisplayName},
			Channel: observe.ChannelRef{ID: row.ChannelID, Name: row.ChannelName},
		})
	}
	if result.MissingTradeCount, err = q.CountUnbookedTrades(ctx); err != nil {
		return result, err
	}
	trades, err := q.ListUnbookedTrades(ctx)
	if err != nil {
		return result, err
	}
	for _, row := range trades {
		result.MissingTrades = append(result.MissingTrades, observe.MissingTrade{TradeID: row.ID, Status: row.Status, Amount: row.AmountNano, ResolvedAt: row.SettledAt})
	}
	return result, nil
}

func trendInputs(ctx context.Context, q *Queries, from, to time.Time) (observe.TrendInputs, error) {
	inputs := observe.TrendInputs{
		Nets:     map[string]map[string]money.Amount{},
		APIDaily: map[string]struct{ Volume, Fee money.Amount }{},
		C2CDaily: map[string]struct {
			Volume   money.Amount
			TotalFen int64
		}{},
	}
	openings, err := q.LedgerOpeningBalances(ctx, from)
	if err != nil {
		return inputs, err
	}
	for _, row := range openings {
		account := observe.AccountOpening{ID: row.ID, Kind: row.Kind, Balance: money.FromNano(row.BalanceNano)}
		if row.SystemCode != nil {
			account.SystemCode = *row.SystemCode
		}
		inputs.Accounts = append(inputs.Accounts, account)
	}
	nets, err := q.LedgerDailyNets(ctx, LedgerDailyNetsParams{FromTime: from, ToTime: to})
	if err != nil {
		return inputs, err
	}
	for _, row := range nets {
		if inputs.Nets[row.LedgerAccountID] == nil {
			inputs.Nets[row.LedgerAccountID] = map[string]money.Amount{}
		}
		inputs.Nets[row.LedgerAccountID][row.Day] = money.FromNano(row.NetNano)
	}
	api, err := q.LedgerAPIDaily(ctx, LedgerAPIDailyParams{FromTime: from, ToTime: to})
	if err != nil {
		return inputs, err
	}
	for _, row := range api {
		inputs.APIDaily[row.Day] = struct{ Volume, Fee money.Amount }{money.FromNano(row.VolumeNano), money.FromNano(row.FeeNano)}
	}
	c2c, err := q.C2CDaily(ctx, C2CDailyParams{FromTime: from, ToTime: to})
	if err != nil {
		return inputs, err
	}
	for _, row := range c2c {
		inputs.C2CDaily[row.Day] = struct {
			Volume   money.Amount
			TotalFen int64
		}{money.FromNano(row.VolumeNano), row.TotalFen}
	}
	return inputs, nil
}

func callWindow(row CallsSinceRow) observe.CallWindow {
	return observe.CallWindow{Calls: row.Calls, Succeeded: row.Succeeded, Unbilled: row.Unbilled, Spend: money.FromNano(row.SpendNano), Fee: money.FromNano(row.FeeNano)}
}

func overviewInputs(ctx context.Context, q *Queries, now time.Time) (observe.OverviewInputs, error) {
	var inputs observe.OverviewInputs
	today, err := q.CallsSince(ctx, localtime.DayStart(now))
	if err != nil {
		return inputs, err
	}
	day, err := q.CallsSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return inputs, err
	}
	inputs.Today, inputs.Last24h = callWindow(today), callWindow(day)
	failing, err := q.FailingChannels(ctx, now.Add(-time.Hour))
	if err != nil {
		return inputs, err
	}
	for _, row := range failing {
		inputs.Failing = append(inputs.Failing, observe.FailingChannel{ID: row.ID, Name: row.Name, Attempts: row.Attempts, Successes: row.Successes})
	}
	counts, err := q.C2COverview(ctx)
	if err != nil {
		return inputs, err
	}
	inputs.C2C = observe.C2CCounts{OpenOrders: counts.OpenOrders, AwaitingPayment: counts.AwaitingPayment, OpenDisputes: counts.OpenDisputes}
	trades, err := q.C2CSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return inputs, err
	}
	inputs.C2C24h = observe.C2CWindow{Trades: trades.Trades, Volume: money.FromNano(trades.VolumeNano), TotalFen: trades.TotalFen}
	return inputs, nil
}

// ---- ledger browsing ----

func (s *Store) transactions(ctx context.Context, q *Queries, params ListLedgerTransactionsParams) ([]observe.Transaction, error) {
	rows, err := q.ListLedgerTransactions(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(rows))
	transactions := make([]observe.Transaction, 0, len(rows))
	index := map[string]int{}
	for _, row := range rows {
		transaction := observe.Transaction{
			ID: row.ID, Type: row.Type, IdempotencyKey: row.IdempotencyKey, Reason: row.Reason, CreatedAt: row.CreatedAt, RelatedSummary: row.RelatedSummary,
		}
		if row.RelatedType != nil && row.RelatedID != nil {
			transaction.RelatedType, transaction.RelatedID = *row.RelatedType, *row.RelatedID
		}
		transaction.Actor = accountRef(row.ActorID, row.ActorUsername, row.ActorDisplayName)
		index[row.ID] = len(transactions)
		ids = append(ids, row.ID)
		transactions = append(transactions, transaction)
	}
	entries, err := q.ListEntriesByTransactions(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range entries {
		position := index[row.TransactionID]
		transactions[position].Entries = append(transactions[position].Entries, observe.TxEntry{
			Account:       observe.LedgerAccountRef{Kind: row.Kind, SystemCode: row.SystemCode, Account: accountRef(row.AccountID, row.Username, row.DisplayName)},
			Amount:        row.AmountNano,
			BalanceBefore: money.FromNano(row.BalanceAfterNano.Nano() - row.AmountNano.Nano()),
			BalanceAfter:  row.BalanceAfterNano,
		})
	}
	return transactions, nil
}

func (s *Store) ListTransactions(ctx context.Context, filter observe.TxFilter, after *observe.CallCursor, limit int) ([]observe.Transaction, error) {
	params := ListLedgerTransactionsParams{
		TxID: nonEmpty(filter.ID), Type: nonEmpty(filter.Type), AccountID: nonEmpty(filter.AccountID), RelatedType: nonEmpty(filter.RelatedType),
		RelatedID: nonEmpty(filter.RelatedID), FromTime: filter.From, ToTime: filter.To, RowLimit: int32(limit),
	}
	if after != nil {
		params.BeforeAt, params.BeforeID = &after.At, &after.ID
	}
	return s.transactions(ctx, s.q, params)
}

func (s *Store) GetTransaction(ctx context.Context, id string) (observe.Transaction, error) {
	items, err := s.transactions(ctx, s.q, ListLedgerTransactionsParams{TxID: &id, RowLimit: 1})
	if err != nil {
		return observe.Transaction{}, err
	}
	if len(items) == 0 {
		return observe.Transaction{}, observe.ErrNotFound
	}
	transaction := items[0]
	if transaction.RelatedType == "call" {
		snapshot, err := s.q.GetCallPriceSnapshot(ctx, transaction.RelatedID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return transaction, err
		}
		transaction.PriceSnapshot = json.RawMessage(snapshot)
	}
	return transaction, nil
}

// ---- repair ----

func (s *Store) Repair(ctx context.Context, repair observe.Repair) (observe.RepairResult, error) {
	var result observe.RepairResult
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		call, err := q.LockCallForRepair(ctx, repair.CallID)
		if errors.Is(err, pgx.ErrNoRows) {
			return observe.ErrNotFound
		}
		if err != nil {
			return err
		}
		billable := call.Outcome == "succeeded" || call.Outcome == "interrupted" || call.Outcome == "client_disconnected"
		// Own-channel calls never produce a ledger transaction (ADR-0027): there is nothing to repair.
		own := call.ChannelOwnerID == nil || *call.ChannelOwnerID == call.AccountID
		if call.LedgerTxID != nil || !billable || own || call.CostNano.Nano()+call.FeeNano.Nano() <= 0 {
			return observe.ErrNotRepairable
		}
		detail := map[string]any{"action": string(repair.Action), "cost": call.CostNano.String(), "fee": call.FeeNano.String()}
		switch repair.Action {
		case observe.RepairCharge:
			entries := []ledger.Line{
				{Account: ledger.User(call.AccountID), Amount: money.FromNano(-(call.CostNano.Nano() + call.FeeNano.Nano()))},
				{Account: ledger.User(*call.ChannelOwnerID), Amount: call.CostNano},
			}
			if call.FeeNano > 0 {
				entries = append(entries, ledger.Line{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: call.FeeNano})
			}
			posted, err := ledgerpg.Post(ctx, tx, ledger.Transaction{
				Type: ledger.TypeAPICall, IdempotencyKey: "call:" + repair.CallID, Related: &ledger.Related{Type: "call", ID: repair.CallID}, Entries: entries,
			})
			if err != nil {
				return err
			}
			if _, err := q.AttachCallLedgerTx(ctx, AttachCallLedgerTxParams{ID: repair.CallID, LedgerTxID: &posted.ID}); err != nil {
				return err
			}
			result.TransactionID = posted.ID
			detail["transaction_id"] = posted.ID
		case observe.RepairVoid:
			if _, err := q.VoidCall(ctx, repair.CallID); err != nil {
				return err
			}
		default:
			return observe.ErrInvalidInput
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: repair.ActorID, Action: audit.ActionLedgerRepairCall, TargetType: "call", TargetID: repair.CallID, Reason: repair.Reason, Detail: detail,
		})
	})
	return result, err
}

// ---- maintenance and metrics ----

func (s *Store) ScrubErrors(ctx context.Context, before time.Time, batch int) (int64, error) {
	return s.q.ScrubAttemptErrors(ctx, ScrubAttemptErrorsParams{Before: before, RowLimit: int32(batch)})
}

func (s *Store) LedgerTotals(ctx context.Context) ([]observe.LedgerTotals, error) {
	rows, err := s.q.LedgerTransactionTotals(ctx)
	if err != nil {
		return nil, err
	}
	totals := make([]observe.LedgerTotals, 0, len(rows))
	for _, row := range rows {
		totals = append(totals, observe.LedgerTotals{Type: row.Type, Transactions: row.Transactions, Amount: money.FromNano(row.AmountNano)})
	}
	return totals, nil
}

func (s *Store) Channels(ctx context.Context) ([]observe.ChannelState, error) {
	rows, err := s.q.ListChannelsForMetrics(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]observe.ChannelState, 0, len(rows))
	for _, row := range rows {
		states = append(states, observe.ChannelState{ID: row.ID, Listed: row.Status == "listed"})
	}
	return states, nil
}
