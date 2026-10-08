// Package gatewaypg is the PostgreSQL implementation of gateway.Store: key
// lookup, candidate selection, call records and the ledger posting that ends
// a billed call in the same transaction.
package gatewaypg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool   *pgxpool.Pool
	q      *Queries
	ledger *ledgerpg.Store
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool), ledger: ledgerpg.NewStore(pool)}
}

func (s *Store) LookupKey(ctx context.Context, hash []byte) (gateway.KeyAuth, error) {
	row, err := s.q.LookupKey(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.KeyAuth{}, gateway.ErrKeyNotFound
	}
	if err != nil {
		return gateway.KeyAuth{}, err
	}
	aliases := map[string]string{}
	_ = json.Unmarshal(row.ModelAliases, &aliases)
	return gateway.KeyAuth{
		KeyID: row.ID, OwnerID: row.OwnerID, Name: row.Name, Prefix: row.Prefix, Enabled: row.Status == "enabled",
		ExpiresAt: row.ExpiresAt, AllowedModels: row.AllowedModels, BudgetDaily: row.BudgetDailyNano,
		BudgetMonthly: row.BudgetMonthlyNano, BudgetTotal: row.BudgetTotalNano, Aliases: aliases,
		AccountActive: row.AccountStatus == "active",
	}, nil
}

func (s *Store) TouchKey(ctx context.Context, keyID string, at time.Time) error {
	return s.q.TouchKey(ctx, TouchKeyParams{ID: keyID, LastUsedAt: &at})
}

func (s *Store) Points(ctx context.Context, accountID string) (ledger.Points, error) {
	return s.ledger.Points(ctx, accountID)
}

func (s *Store) KeySpend(ctx context.Context, keyID string, now time.Time) (gateway.Spend, error) {
	row, err := s.q.KeySpend(ctx, KeySpendParams{DayStart: localtime.DayStart(now), MonthStart: localtime.MonthStart(now), KeyID: &keyID})
	if err != nil {
		return gateway.Spend{}, err
	}
	return gateway.Spend{Today: money.FromNano(row.TodayNano), Month: money.FromNano(row.MonthNano), Total: money.FromNano(row.TotalNano)}, nil
}

func (s *Store) ChannelRevenue(ctx context.Context, channelID string, since time.Time) (money.Amount, error) {
	nano, err := s.q.ChannelRevenue(ctx, ChannelRevenueParams{FinalChannelID: &channelID, CreatedAt: localtime.DayStart(since)})
	return money.FromNano(nano), err
}

func (s *Store) Candidates(ctx context.Context, modelID string, format channel.Format) ([]gateway.Candidate, error) {
	rows, err := s.q.ListCandidates(ctx, ListCandidatesParams{ModelID: modelID, Format: string(format)})
	if err != nil {
		return nil, err
	}
	candidates := make([]gateway.Candidate, 0, len(rows))
	for _, row := range rows {
		var rules struct {
			Set []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"set"`
			Remove []string `json:"remove"`
		}
		_ = json.Unmarshal(row.HeaderRules, &rules)
		advanced := channel.Advanced{
			UserAgent: row.UserAgent, ConcurrencyLimit: row.ConcurrencyLimit, RPMLimit: row.RpmLimit,
			DailyRevenueCap: row.DailyRevenueCapNano, TTFTTimeoutMS: row.TtftTimeoutMs, TotalTimeoutMS: row.TotalTimeoutMs,
			CooldownFailures: row.CooldownFailures, CooldownSeconds: row.CooldownSeconds,
			HeaderRules: channel.HeaderRules{Remove: rules.Remove},
		}
		for _, rule := range rules.Set {
			advanced.HeaderRules.Set = append(advanced.HeaderRules.Set, channel.HeaderSet{Name: rule.Name, Value: rule.Value})
		}
		candidates = append(candidates, gateway.Candidate{
			ChannelID: row.ID, OwnerID: row.OwnerID, Name: row.Name, BaseURL: row.BaseUrl, UpstreamModel: row.UpstreamModel,
			MultiplierNano: row.MultiplierNano, Advanced: advanced,
			Credential: channel.EncryptedCredential{Version: 1, KeyID: row.ApiKeyKeyID, Nonce: row.ApiKeyNonce, Ciphertext: row.ApiKeyCiphertext},
		})
	}
	return candidates, nil
}

func (s *Store) ChannelStats(ctx context.Context) (map[string]gateway.ChannelStats, error) {
	rows, err := s.q.ChannelStats24h(ctx)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]gateway.ChannelStats, len(rows))
	for _, row := range rows {
		entry := gateway.ChannelStats{Attempts: row.Attempts, Successes: row.Successes}
		if row.TtftP50Ms >= 0 {
			value := row.TtftP50Ms
			entry.TTFTP50MS = &value
		}
		stats[row.ChannelID] = entry
	}
	return stats, nil
}

func (s *Store) ResponseChannel(ctx context.Context, accountID, responseID string) (string, error) {
	id, err := s.q.ResponseChannel(ctx, ResponseChannelParams{UpstreamResponseID: &responseID, AccountID: accountID})
	if err != nil {
		return "", err
	}
	if id == nil {
		return "", pgx.ErrNoRows
	}
	return *id, nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *Store) InsertCall(ctx context.Context, call gateway.CallInsert) (string, error) {
	return s.q.InsertCall(ctx, InsertCallParams{
		AccountID: call.AccountID, ApiKeyID: optional(call.KeyID), RequestedModel: call.RequestedModel, Format: string(call.Format),
		Stream: call.Stream, Tag: optional(call.Tag), ClientUserAgent: optional(call.UserAgent), Outcome: call.Outcome,
	})
}

func int32Pointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	converted := int32(*value)
	return &converted
}

func (s *Store) FinishCall(ctx context.Context, finish gateway.CallFinish) (gateway.FinishResult, error) {
	attempts, err := json.Marshal(nonNilAttempts(finish.Attempts))
	if err != nil {
		return gateway.FinishResult{}, err
	}
	duration := int32(finish.DurationMS)
	bytesRead := finish.ResponseBytes
	var result gateway.FinishResult
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		affected, err := q.FinishCall(ctx, FinishCallParams{
			ID: finish.ID, Outcome: finish.Outcome, ModelID: optional(finish.ModelID), ChannelID: optional(finish.ChannelID),
			RoutingMode: optional(finish.RoutingMode), RoutingSource: optional(finish.RoutingSource), Attempts: attempts,
			InputTokens: finish.Usage.InputTokens, OutputTokens: finish.Usage.OutputTokens,
			CacheWriteTokens: finish.Usage.CacheWriteTokens, CacheReadTokens: finish.Usage.CacheReadTokens,
			PriceSnapshot: finish.PriceSnapshot, CostNano: finish.Cost, FeeNano: finish.Fee, TtftMs: int32Pointer(finish.TTFTMS),
			DurationMs: &duration, TokensPerSecond: finish.TokensPerSecond, InterTokenP50Ms: int32Pointer(finish.IntervalP50MS),
			InterTokenP95Ms: int32Pointer(finish.IntervalP95MS), ResponseBytes: &bytesRead, UpstreamResponseID: optional(finish.UpstreamResponseID),
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil // already closed: never book twice
		}
		result.Finished = true
		if finish.Bill == nil {
			return nil
		}
		entries := []ledger.Line{
			{Account: ledger.User(finish.Bill.ConsumerID), Amount: money.FromNano(-(finish.Bill.Cost + finish.Bill.Fee).Nano())},
			{Account: ledger.User(finish.Bill.ProviderID), Amount: finish.Bill.Cost},
		}
		if finish.Bill.Fee > 0 {
			entries = append(entries, ledger.Line{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: finish.Bill.Fee})
		}
		posted, err := ledgerpg.Post(ctx, tx, ledger.Transaction{
			Type: ledger.TypeAPICall, IdempotencyKey: "call:" + finish.ID, Related: &ledger.Related{Type: "call", ID: finish.ID},
			Entries: entries,
		})
		if err != nil {
			return err
		}
		result.TxID = posted.ID
		return q.SetCallLedgerTx(ctx, SetCallLedgerTxParams{ID: finish.ID, LedgerTxID: &posted.ID})
	})
	return result, err
}

func nonNilAttempts(attempts []gateway.Attempt) []gateway.Attempt {
	if attempts == nil {
		return []gateway.Attempt{}
	}
	return attempts
}

func (s *Store) SweepStaleCalls(ctx context.Context, before time.Time) (int64, error) {
	return s.q.SweepStaleCalls(ctx, before)
}

func (s *Store) RecordChannelEvent(ctx context.Context, channelID, kind, reason string) error {
	return channelpg.RecordEvent(ctx, s.pool, channelID, kind, reason)
}

func (s *Store) OnlineModels(ctx context.Context) ([]string, error) {
	return s.q.ListOnlineModels(ctx)
}

func (s *Store) OnlineChannels(ctx context.Context) ([]gateway.OnlineChannel, error) {
	rows, err := s.q.ListOnlineChannelModels(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]gateway.OnlineChannel, 0, len(rows))
	for _, row := range rows {
		online := gateway.OnlineChannel{
			ModelID: row.ModelID, ChannelID: row.ChannelID, ChannelName: row.ChannelName, OwnerID: row.OwnerID, OwnerName: row.OwnerName,
			MultiplierNano: row.MultiplierNano,
			Advanced: channel.Advanced{
				ConcurrencyLimit: row.ConcurrencyLimit, RPMLimit: row.RpmLimit, DailyRevenueCap: row.DailyRevenueCapNano,
				CooldownFailures: row.CooldownFailures, CooldownSeconds: row.CooldownSeconds,
			},
		}
		for _, format := range row.Formats {
			online.Formats = append(online.Formats, channel.Format(format))
		}
		result = append(result, online)
	}
	return result, nil
}

func (s *Store) Home(ctx context.Context, accountID string, dayStart time.Time) (gateway.HomeStats, error) {
	var stats gateway.HomeStats
	today, err := s.q.HomeToday(ctx, HomeTodayParams{AccountID: accountID, CreatedAt: dayStart})
	if err != nil {
		return stats, err
	}
	stats.TodaySpend, stats.TodayCalls, stats.TodaySucceeded = money.FromNano(today.SpendNano), today.Calls, today.Succeeded
	channels, err := s.q.HomeChannels(ctx, accountID)
	if err != nil {
		return stats, err
	}
	stats.ChannelsOnline, stats.ChannelsTotal = channels.Online, channels.Total
	revenue, err := s.q.HomeRevenue(ctx, HomeRevenueParams{OwnerID: accountID, CreatedAt: dayStart})
	if err != nil {
		return stats, err
	}
	stats.RevenueToday = money.FromNano(revenue)
	stats.PendingTrades, err = s.q.HomePendingTrades(ctx, accountID)
	return stats, err
}

func (s *Store) RecentCalls(ctx context.Context, accountID string, limit int) ([]gateway.CallSummary, error) {
	rows, err := s.q.ListRecentCalls(ctx, ListRecentCallsParams{AccountID: accountID, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	calls := make([]gateway.CallSummary, 0, len(rows))
	for _, row := range rows {
		calls = append(calls, gateway.CallSummary{
			ID: row.ID, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt, ModelID: row.ModelID, RequestedModel: row.RequestedModel,
			Format: channel.Format(row.Format), Stream: row.Stream, Tag: row.Tag, KeyID: row.ApiKeyID, KeyName: row.KeyName,
			Outcome: row.Outcome, ChannelID: row.FinalChannelID, ChannelName: row.ChannelName,
			Usage: ledger.Usage{InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CacheWriteTokens: row.CacheWriteTokens, CacheReadTokens: row.CacheReadTokens},
			Cost:  row.CostNano, Fee: row.FeeNano, TTFTMS: row.TtftMs, DurationMS: row.DurationMs,
		})
	}
	return calls, nil
}
