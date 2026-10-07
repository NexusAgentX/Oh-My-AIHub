// Package opspg is the PostgreSQL implementation of the operations read model
// and inspection history. SQL lives in queries.sql; the generated code is
// committed beside it. Queries read across ledger, call, channel and C2C tables
// directly (ADR-0017). Nullable aggregates are encoded in SQL as a sentinel plus
// a has_* flag because sqlc does not infer nullability for them; this package
// restores the NULL semantics.
package opspg

import (
	"context"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

type Store struct {
	q      *Queries
	ledger *ledgerpg.Store
}

// NewStore takes the ledger store because every ops view starts from the
// ledger metrics snapshot.
func NewStore(pool *pgxpool.Pool, ledger *ledgerpg.Store) *Store {
	return &Store{q: New(pool), ledger: ledger}
}

func pointsString(nano int64) string {
	return money.FromNano(nano).String()
}

func ratioString(numerator, denominator int64) *string {
	if denominator <= 0 {
		return nil
	}
	value := new(big.Float).SetPrec(128)
	value.Quo(new(big.Float).SetInt64(numerator), new(big.Float).SetInt64(denominator))
	text := value.Text('f', 6)
	return &text
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt(value int64, ok bool) *int64 {
	if !ok {
		return nil
	}
	return &value
}

func optionalTime(value time.Time, ok bool) *time.Time {
	if !ok {
		return nil
	}
	return &value
}

func subtractPoints(total, used string) (string, error) {
	totalNano, err := money.Parse(total)
	if err != nil {
		return "", err
	}
	usedNano, err := money.Parse(used)
	if err != nil {
		return "", err
	}
	return pointsString(int64(totalNano - usedNano)), nil
}

// OpsMetrics computes the unified operations snapshot for one window.
func (s *Store) OpsMetrics(ctx context.Context, window ops.Window) (ops.Metrics, error) {
	result := ops.Metrics{Window: window, NegativeBalances: []ops.NegativeBalanceRisk{}, C2C: ops.C2CMetrics{Orders: []ops.C2COrderStatusCount{}, Trades: []ops.C2CTradeStatusCount{}}}
	ledgerSnapshot, err := s.ledger.Metrics(ctx)
	if err != nil {
		return ops.Metrics{}, err
	}
	result.Ledger = ops.NewLedgerMetricsView(ledgerSnapshot)
	if effective, err := subtractPoints(ledgerSnapshot.TotalCreditLimit, ledgerSnapshot.UsedCredit); err == nil {
		result.EffectiveCredit = effective
	}

	if err := s.negativeBalances(ctx, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.apiWindow(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.consumptionWindow(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.c2c(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.concentration(ctx, &result); err != nil {
		return ops.Metrics{}, err
	}
	return result, nil
}

// OpsProviderIncome lists windowed sharer income without credentials or raw errors.
func (s *Store) OpsProviderIncome(ctx context.Context, window ops.Window) (ops.ProviderIncomeSnapshot, error) {
	result := ops.ProviderIncomeSnapshot{
		Window:    window,
		Providers: []ops.ProviderIncomeRow{},
	}
	rows, err := s.q.ListProviderIncome(ctx, ListProviderIncomeParams{FromAt: window.From, ToAt: window.To})
	if err != nil {
		return ops.ProviderIncomeSnapshot{}, err
	}
	var totalIncome, otherIncome, ownIncome int64
	for _, item := range rows {
		row := ops.ProviderIncomeRow{
			AccountID:           item.AccountID,
			DisplayName:         item.DisplayName,
			TotalIncome:         pointsString(item.TotalIncomeNano),
			OtherConsumerIncome: pointsString(item.OtherConsumerIncomeNano),
			OwnUsageIncome:      pointsString(item.OwnUsageIncomeNano),
		}
		if item.TerminalAttempts > 0 {
			row.SuccessRate = ratioString(item.SucceededAttempts, item.TerminalAttempts)
		}
		result.Providers = append(result.Providers, row)
		totalIncome += item.TotalIncomeNano
		otherIncome += item.OtherConsumerIncomeNano
		ownIncome += item.OwnUsageIncomeNano
		if item.TotalIncomeNano > 0 {
			result.ActiveProviders++
		}
	}
	result.TotalIncome = pointsString(totalIncome)
	result.OtherConsumerIncome = pointsString(otherIncome)
	result.OwnUsageIncome = pointsString(ownIncome)
	return result, nil
}

func (s *Store) negativeBalances(ctx context.Context, result *ops.Metrics) error {
	rows, err := s.q.ListNegativeBalances(ctx)
	if err != nil {
		return err
	}
	for _, item := range rows {
		row := ops.NegativeBalanceRisk{
			AccountID:     item.ID,
			Username:      item.Username,
			PostedBalance: pointsString(item.PostedBalanceNano),
			CreditLimit:   pointsString(item.CreditLimitNano),
			OverLimit:     item.CreditFrozen || -item.PostedBalanceNano > item.CreditLimitNano,
			InactiveDays:  item.InactiveDays,
		}
		if item.HasNegativeSince {
			row.NegativeSince = item.NegativeSince.UTC().Format(time.RFC3339)
		}
		if item.HasLastActivity {
			row.LastFinancialActivity = item.LastActivity.UTC().Format(time.RFC3339)
		}
		result.NegativeBalances = append(result.NegativeBalances, row)
	}
	return nil
}

func (s *Store) apiWindow(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	funnel, err := s.q.GetAPIFunnel(ctx, GetAPIFunnelParams{FromAt: window.From, ToAt: window.To})
	if err != nil {
		return err
	}
	api := &result.API
	api.PrecheckRejected = funnel.Rejected
	api.ReachedUpstream = funnel.Reached
	api.Succeeded = funnel.Succeeded
	api.AllFailed = funnel.Failed
	api.IncompleteAfterCommit = funnel.Incomplete
	api.Cancelled = funnel.Cancelled
	api.TerminalReached = funnel.Terminal
	api.SuccessRate = ratioString(funnel.Succeeded, funnel.Terminal)

	stats, err := s.q.GetAttemptStats(ctx, GetAttemptStatsParams{FromAt: window.From, ToAt: window.To})
	if err != nil {
		return err
	}
	api.AttemptCount = stats.Attempts
	api.AttemptSucceeded = stats.SucceededAttempts
	if stats.AvgTtft != "" {
		if parsed, err := strconv.ParseFloat(stats.AvgTtft, 64); err == nil {
			rounded := int64(math.Round(parsed))
			api.AverageTTFTMillis = &rounded
		}
	}
	if stats.AvgTps != "" {
		if parsed, err := strconv.ParseFloat(stats.AvgTps, 64); err == nil {
			text := strconv.FormatFloat(parsed, 'f', 3, 64)
			api.AverageTPS = &text
		}
	}
	return nil
}

func (s *Store) consumptionWindow(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	row, err := s.q.GetConsumptionWindow(ctx, GetConsumptionWindowParams{FromAt: window.From, ToAt: window.To})
	if err != nil {
		return err
	}
	consumption := &result.Consumption
	consumption.ConsumerSpendNano = row.CapturedCharge + row.CapturedFee
	consumption.ProviderIncomeNano = row.CapturedCharge + row.SelfCharge
	consumption.OwnUsageIncomeNano = row.SelfCharge
	consumption.PlatformFeeNano = row.CapturedFee
	consumption.ConsumerSpend = pointsString(consumption.ConsumerSpendNano)
	consumption.ProviderIncome = pointsString(consumption.ProviderIncomeNano)
	consumption.OwnUsageIncome = pointsString(consumption.OwnUsageIncomeNano)
	consumption.OtherConsumerIncome = pointsString(row.CapturedCharge)
	consumption.PlatformFee = pointsString(row.CapturedFee)
	return nil
}

func (s *Store) c2c(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	orders, err := s.q.ListC2COrderStatusCounts(ctx)
	if err != nil {
		return err
	}
	for _, row := range orders {
		result.C2C.Orders = append(result.C2C.Orders, ops.C2COrderStatusCount{Side: row.Side, Status: row.Status, Count: row.Count})
	}
	trades, err := s.q.ListC2CTradeStatusCounts(ctx, ListC2CTradeStatusCountsParams{FromAt: window.From, ToAt: window.To})
	if err != nil {
		return err
	}
	for _, row := range trades {
		result.C2C.Trades = append(result.C2C.Trades, ops.C2CTradeStatusCount{Status: row.Status, Count: row.Count})
	}

	quote, err := s.q.GetC2CQuote(ctx)
	if err != nil {
		return err
	}
	last := optionalInt(quote.LastPrice, quote.HasLastPrice)
	bid := optionalInt(quote.BestBid, quote.HasBestBid)
	ask := optionalInt(quote.BestAsk, quote.HasBestAsk)
	result.C2C.Quote = ops.C2CMarketQuote{LastTradedPriceFen: last, BestBidPriceFen: bid, BestAskPriceFen: ask}
	if bid != nil && ask != nil {
		spread := *ask - *bid
		result.C2C.Quote.SpreadFen = &spread
	}
	return nil
}

func (s *Store) concentration(ctx context.Context, result *ops.Metrics) error {
	row, err := s.q.GetConcentration(ctx)
	if err != nil {
		return err
	}
	result.Concentration = ops.ConcentrationMetrics{
		PositiveUserCount: row.PositiveCount,
		TotalPositive:     ledgerpg.NanoIntegerToPoints(row.TotalPositive),
		Top1Share:         optionalText(row.Top1Share),
		Top5Share:         optionalText(row.Top5Share),
		HHI:               optionalText(row.Hhi),
	}
	return nil
}

// OpsAnomalies returns hard invariant violations plus attention items.
func (s *Store) OpsAnomalies(ctx context.Context) (ops.Anomalies, error) {
	result := ops.Anomalies{CheckedAt: time.Now().UTC(), Hard: []ops.Anomaly{}, Attention: []ops.Anomaly{}}

	ledgerSnapshot, err := s.ledger.Metrics(ctx)
	if err != nil {
		return ops.Anomalies{}, err
	}
	zeroSum, err := money.Parse(ledgerSnapshot.TotalPostedBalance)
	if err != nil {
		return ops.Anomalies{}, err
	}
	if zeroSum != 0 {
		result.Hard = append(result.Hard, ops.Anomaly{
			Kind: "zero_sum_difference", Count: 1,
			Detail:    "全账户余额和为 " + ledgerSnapshot.TotalPostedBalance + " 积分，期望恒为 0",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}
	if ledgerSnapshot.PostedProjectionMismatchAccounts > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{
			Kind: "posted_projection_difference", Count: ledgerSnapshot.PostedProjectionMismatchAccounts,
			Detail:    "入账投影与分录合计差异 " + ledgerSnapshot.PostedProjectionDifference + " 积分",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}
	if ledgerSnapshot.HoldProjectionMismatchAccounts > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{
			Kind: "hold_projection_difference", Count: ledgerSnapshot.HoldProjectionMismatchAccounts,
			Detail:    "资产/授权投影与持有合计差异（资产 " + ledgerSnapshot.AssetReservationDifference + "，授权 " + ledgerSnapshot.SpendAuthorizationDifference + "）",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}

	violations, err := s.q.CountHardViolations(ctx)
	if err != nil {
		return ops.Anomalies{}, err
	}
	if violations.WithoutSettlement > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "succeeded_call_without_settlement", Count: violations.WithoutSettlement, Detail: "成功调用缺少结算事实", Drilldown: "/admin/ops?drilldown=api-calls"})
	}
	if violations.WithoutLedgerTx > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "settlement_without_ledger", Count: violations.WithoutLedgerTx, Detail: "结算缺少账本交易", Drilldown: "/admin/ops?drilldown=api-calls"})
	}
	if violations.QuantityViolations > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "c2c_quantity_invariant", Count: violations.QuantityViolations, Detail: "C2C 挂单数量恒等式被违反", Drilldown: "/admin/ops?drilldown=c2c-orders"})
	}
	if violations.HoldViolations > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "c2c_hold_invariant", Count: violations.HoldViolations, Detail: "C2C 父持有与可成交/已分配数量不一致", Drilldown: "/admin/ops?drilldown=c2c-orders"})
	}
	for _, item := range result.Hard {
		result.HardCount += item.Count
	}

	openDisputes, err := s.q.CountDisputedTrades(ctx)
	if err != nil {
		return ops.Anomalies{}, err
	}
	result.Attention = append(result.Attention,
		ops.Anomaly{Kind: "open_disputes", Attention: true, Count: openDisputes, Detail: "处于争议中的交易数量", Drilldown: "/admin/c2c/disputes"},
		ops.Anomaly{Kind: "over_limit_accounts", Attention: true, Count: ledgerSnapshot.OverLimitAccounts, Detail: "超出可用信用的账户数量（无既定阈值，仅呈现事实）", Drilldown: "/admin/ops?drilldown=ledger-accounts"},
		ops.Anomaly{Kind: "credit_frozen_accounts", Attention: true, Count: ledgerSnapshot.CreditFrozenAccounts, Detail: "信用被冻结的账户数量", Drilldown: "/admin/accounts"},
	)
	return result, nil
}

func toInspection(row InsertInspectionRow) ops.InspectionRecord {
	return ops.InspectionRecord{
		ID: row.ID, InspectionVersion: row.InspectionVersion, TriggeredBy: row.TriggeredBy,
		ZeroSumOK: row.ZeroSumOk, ProjectionOK: row.ProjectionOk, CallSettlementOK: row.CallSettlementOk, C2CConsistencyOK: row.C2cConsistencyOk,
		ZeroSumDifference: row.ZeroSumDifference, PostedProjectionDifference: row.PostedProjectionDifference,
		AssetProjectionDifference: row.AssetProjectionDifference, AuthorizationProjectionDiff: row.AuthorizationProjectionDifference,
		SuccessfulCallsWithoutSettlement: row.SuccessfulCallsWithoutSettlement, SettlementsWithoutLedgerTx: row.SettlementsWithoutLedgerTransaction,
		C2CQuantityViolations: row.C2cQuantityViolations, C2CHoldViolations: row.C2cHoldViolations, CheckedAt: row.CheckedAt,
	}
}

// OpsRunInspection executes the fixed invariant set and persists one row.
func (s *Store) OpsRunInspection(ctx context.Context, triggeredBy string) (ops.InspectionRecord, error) {
	ledgerSnapshot, err := s.ledger.Metrics(ctx)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	zeroSum, err := money.Parse(ledgerSnapshot.TotalPostedBalance)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	postedDifference, err := money.Parse(ledgerSnapshot.PostedProjectionDifference)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	assetDifference, err := money.Parse(ledgerSnapshot.AssetReservationDifference)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	authorizationDifference, err := money.Parse(ledgerSnapshot.SpendAuthorizationDifference)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	violations, err := s.q.CountHardViolations(ctx)
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	row, err := s.q.InsertInspection(ctx, InsertInspectionParams{
		InspectionVersion:                     ops.InspectionVersion,
		TriggeredBy:                           triggeredBy,
		ZeroSumOk:                             zeroSum == 0,
		ProjectionOk:                          postedDifference == 0 && assetDifference == 0 && authorizationDifference == 0,
		CallSettlementOk:                      violations.WithoutSettlement == 0 && violations.WithoutLedgerTx == 0,
		C2cConsistencyOk:                      violations.QuantityViolations == 0 && violations.HoldViolations == 0,
		ZeroSumDifferenceNano:                 zeroSum,
		PostedProjectionDifferenceNano:        postedDifference,
		AssetProjectionDifferenceNano:         assetDifference,
		AuthorizationProjectionDifferenceNano: authorizationDifference,
		SuccessfulCallsWithoutSettlement:      violations.WithoutSettlement,
		SettlementsWithoutLedgerTransaction:   violations.WithoutLedgerTx,
		C2cQuantityViolations:                 violations.QuantityViolations,
		C2cHoldViolations:                     violations.HoldViolations,
	})
	if err != nil {
		return ops.InspectionRecord{}, err
	}
	return toInspection(row), nil
}

// OpsListInspections returns the most recent persisted inspections.
func (s *Store) OpsListInspections(ctx context.Context, limit int64) ([]ops.InspectionRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.q.ListInspections(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	records := make([]ops.InspectionRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toInspection(InsertInspectionRow(row)))
	}
	return records, nil
}

// OpsTrialSummary aggregates non-sensitive trial evidence counts.
func (s *Store) OpsTrialSummary(ctx context.Context) (ops.TrialSummary, error) {
	summary := ops.TrialSummary{GeneratedAt: time.Now().UTC()}
	ledgerSnapshot, err := s.ledger.Metrics(ctx)
	if err != nil {
		return ops.TrialSummary{}, err
	}
	summary.LedgerZeroSumOK = ledgerSnapshot.TotalPostedBalance == "0"

	counts, err := s.q.GetTrialCounts(ctx)
	if err != nil {
		return ops.TrialSummary{}, err
	}
	summary.NonAdminAccounts = counts.NonAdminAccounts
	summary.PublishedChannels = counts.PublishedChannels
	summary.PassedOffers = counts.PassedOffers
	summary.ActiveAPIKeys = counts.ActiveApiKeys
	summary.CallsSucceeded = counts.CallsSucceeded
	summary.CallsFailed = counts.CallsFailed
	summary.CallsIncomplete = counts.CallsIncomplete
	summary.FirstCallAt = optionalTime(counts.FirstCallAt, counts.HasFirstCallAt)
	summary.LastTerminalCallAt = optionalTime(counts.LastTerminalCallAt, counts.HasLastTerminalCallAt)
	summary.C2COpenOrders = counts.C2cOpenOrders
	summary.C2CReleasedTrades = counts.C2cReleasedTrades
	summary.C2CDisputedOpen = counts.C2cDisputedOpen

	inspections, err := s.q.GetInspectionSummary(ctx)
	if err != nil {
		return ops.TrialSummary{}, err
	}
	summary.InspectionPassCount = inspections.PassCount
	summary.InspectionTotalCount = inspections.TotalCount
	if inspections.TotalCount > 0 {
		last := inspections.LastAt
		ok := inspections.LastOk
		summary.LastInspectionAt = &last
		summary.LastInspectionOK = &ok
	}
	return summary, nil
}
