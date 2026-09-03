package postgres

import (
	"context"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
)

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

// OpsMetrics computes the unified operations snapshot for one window.
func (s *Store) OpsMetrics(ctx context.Context, window ops.Window) (ops.Metrics, error) {
	result := ops.Metrics{Window: window, NegativeBalances: []ops.NegativeBalanceRisk{}, C2C: ops.C2CMetrics{Orders: []ops.C2COrderStatusCount{}, Trades: []ops.C2CTradeStatusCount{}}}
	ledgerSnapshot, err := s.Metrics(ctx)
	if err != nil {
		return ops.Metrics{}, err
	}
	result.Ledger = ops.NewLedgerMetricsView(ledgerSnapshot)
	if effective, err := subtractPoints(ledgerSnapshot.TotalCreditLimit, ledgerSnapshot.UsedCredit); err == nil {
		result.EffectiveCredit = effective
	}

	if err := s.opsNegativeBalances(ctx, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.opsAPIWindow(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.opsConsumptionWindow(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.opsC2C(ctx, window, &result); err != nil {
		return ops.Metrics{}, err
	}
	if err := s.opsConcentration(ctx, &result); err != nil {
		return ops.Metrics{}, err
	}
	return result, nil
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

func (s *Store) opsNegativeBalances(ctx context.Context, result *ops.Metrics) error {
	rows, err := s.pool.Query(ctx, `
		WITH neg AS (
			SELECT la.id, la.identity_account_id, la.posted_balance_nano, a.username, a.credit_limit_nano, a.credit_frozen
			FROM ledger_accounts la
			JOIN accounts a ON a.id = la.identity_account_id
			WHERE la.kind = 'user' AND la.posted_balance_nano < 0
		), entries AS (
			SELECT e.ledger_account_id, e.created_at, e.amount_nano,
				sum(e.amount_nano) OVER (PARTITION BY e.ledger_account_id ORDER BY e.created_at, e.id
					ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS suffix_sum
			FROM ledger_entries e
			JOIN neg n ON n.id = e.ledger_account_id
		), streak AS (
			SELECT n.id, n.posted_balance_nano, n.username, n.credit_limit_nano, n.credit_frozen,
				min(e.created_at) FILTER (WHERE e.suffix_sum < 0) AS negative_since,
				max(e.created_at) AS last_activity
			FROM neg n
			LEFT JOIN entries e ON e.ledger_account_id = n.id
			GROUP BY n.id, n.posted_balance_nano, n.username, n.credit_limit_nano, n.credit_frozen
		)
		SELECT id::text, username, posted_balance_nano, negative_since, last_activity, credit_limit_nano, credit_frozen
		FROM streak ORDER BY posted_balance_nano ASC LIMIT 200`)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var row ops.NegativeBalanceRisk
		var posted, creditLimit int64
		var negativeSince, lastActivity *time.Time
		var frozen bool
		if err := rows.Scan(&row.AccountID, &row.Username, &posted, &negativeSince, &lastActivity, &creditLimit, &frozen); err != nil {
			return err
		}
		row.PostedBalance = pointsString(posted)
		row.CreditLimit = pointsString(creditLimit)
		row.OverLimit = frozen || -posted > creditLimit
		if negativeSince != nil {
			row.NegativeSince = negativeSince.UTC().Format(time.RFC3339)
		}
		if lastActivity != nil {
			row.LastFinancialActivity = lastActivity.UTC().Format(time.RFC3339)
			row.InactiveDays = int64(math.Floor(now.Sub(*lastActivity).Hours() / 24))
		}
		result.NegativeBalances = append(result.NegativeBalances, row)
	}
	return rows.Err()
}

func (s *Store) opsAPIWindow(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	var funnel struct{ rejected, reached, succeeded, failed, incomplete, cancelled, terminal int64 }
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'rejected'),
			count(*) FILTER (WHERE upstream_attempt_count > 0),
			count(*) FILTER (WHERE status = 'succeeded'),
			count(*) FILTER (WHERE status = 'failed'),
			count(*) FILTER (WHERE status = 'incomplete'),
			count(*) FILTER (WHERE status = 'cancelled'),
			count(*) FILTER (WHERE upstream_attempt_count > 0 AND status IN ('succeeded', 'failed', 'incomplete', 'cancelled'))
		FROM api_calls WHERE created_at >= $1 AND created_at < $2`, window.From, window.To).
		Scan(&funnel.rejected, &funnel.reached, &funnel.succeeded, &funnel.failed, &funnel.incomplete, &funnel.cancelled, &funnel.terminal); err != nil {
		return err
	}
	api := &result.API
	api.PrecheckRejected = funnel.rejected
	api.ReachedUpstream = funnel.reached
	api.Succeeded = funnel.succeeded
	api.AllFailed = funnel.failed
	api.IncompleteAfterCommit = funnel.incomplete
	api.Cancelled = funnel.cancelled
	api.TerminalReached = funnel.terminal
	api.SuccessRate = ratioString(funnel.succeeded, funnel.terminal)

	var attempts int64
	var succeededAttempts int64
	var avgTTFT, avgTPS *string
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE a.status = 'succeeded'),
			(avg(a.ttft_milliseconds) FILTER (WHERE a.status = 'succeeded' AND a.ttft_milliseconds IS NOT NULL))::text,
			(avg(a.tokens_per_second_nano) FILTER (WHERE a.status = 'succeeded' AND a.tokens_per_second_nano IS NOT NULL))::text
		FROM api_call_attempts a
		JOIN api_calls c ON c.id = a.call_id
		WHERE c.created_at >= $1 AND c.created_at < $2`, window.From, window.To).
		Scan(&attempts, &succeededAttempts, &avgTTFT, &avgTPS); err != nil {
		return err
	}
	api.AttemptCount = attempts
	api.AttemptSucceeded = succeededAttempts
	if avgTTFT != nil {
		if parsed, err := strconv.ParseFloat(*avgTTFT, 64); err == nil {
			rounded := int64(math.Round(parsed))
			api.AverageTTFTMillis = &rounded
		}
	}
	if avgTPS != nil {
		if parsed, err := strconv.ParseFloat(*avgTPS, 64); err == nil {
			text := strconv.FormatFloat(parsed, 'f', 3, 64)
			api.AverageTPS = &text
		}
	}
	return nil
}

func (s *Store) opsConsumptionWindow(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	var capturedCharge, capturedFee, selfCharge int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'captured'), 0),
			COALESCE(sum(s.platform_fee_nano) FILTER (WHERE s.kind = 'captured'), 0),
			COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'self_usage'), 0)
		FROM api_call_settlements s
		JOIN api_calls c ON c.id = s.call_id
		WHERE c.created_at >= $1 AND c.created_at < $2`, window.From, window.To).
		Scan(&capturedCharge, &capturedFee, &selfCharge); err != nil {
		return err
	}
	consumption := &result.Consumption
	consumption.ConsumerSpendNano = capturedCharge + capturedFee
	consumption.ProviderIncomeNano = capturedCharge + selfCharge
	consumption.OwnUsageIncomeNano = selfCharge
	consumption.PlatformFeeNano = capturedFee
	consumption.ConsumerSpend = pointsString(consumption.ConsumerSpendNano)
	consumption.ProviderIncome = pointsString(consumption.ProviderIncomeNano)
	consumption.OwnUsageIncome = pointsString(consumption.OwnUsageIncomeNano)
	consumption.OtherConsumerIncome = pointsString(capturedCharge)
	consumption.PlatformFee = pointsString(capturedFee)
	return nil
}

func (s *Store) opsC2C(ctx context.Context, window ops.Window, result *ops.Metrics) error {
	orderRows, err := s.pool.Query(ctx, `SELECT side, status, count(*) FROM c2c_orders GROUP BY side, status ORDER BY side, status`)
	if err != nil {
		return err
	}
	for orderRows.Next() {
		var row ops.C2COrderStatusCount
		if err := orderRows.Scan(&row.Side, &row.Status, &row.Count); err != nil {
			orderRows.Close()
			return err
		}
		result.C2C.Orders = append(result.C2C.Orders, row)
	}
	orderRows.Close()
	if err := orderRows.Err(); err != nil {
		return err
	}

	tradeRows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM c2c_trades WHERE created_at >= $1 AND created_at < $2 GROUP BY status ORDER BY status`, window.From, window.To)
	if err != nil {
		return err
	}
	for tradeRows.Next() {
		var row ops.C2CTradeStatusCount
		if err := tradeRows.Scan(&row.Status, &row.Count); err != nil {
			tradeRows.Close()
			return err
		}
		result.C2C.Trades = append(result.C2C.Trades, row)
	}
	tradeRows.Close()
	if err := tradeRows.Err(); err != nil {
		return err
	}

	var last, bid, ask *int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT unit_price_fen FROM c2c_trades WHERE status = 'released_to_buyer' ORDER BY resolved_at DESC, id DESC LIMIT 1),
			(SELECT max(unit_price_fen) FROM c2c_orders WHERE side = 'buy' AND status IN ('open', 'allocated') AND available_nano > 0),
			(SELECT min(unit_price_fen) FROM c2c_orders WHERE side = 'sell' AND status IN ('open', 'allocated') AND available_nano > 0)`).
		Scan(&last, &bid, &ask); err != nil {
		return err
	}
	result.C2C.Quote = ops.C2CMarketQuote{LastTradedPriceFen: last, BestBidPriceFen: bid, BestAskPriceFen: ask}
	if bid != nil && ask != nil {
		spread := *ask - *bid
		result.C2C.Quote.SpreadFen = &spread
	}
	return nil
}

func (s *Store) opsConcentration(ctx context.Context, result *ops.Metrics) error {
	var count int64
	var total string
	var top1, top5, hhi *string
	if err := s.pool.QueryRow(ctx, `
		WITH pos AS (
			SELECT posted_balance_nano FROM ledger_accounts WHERE kind = 'user' AND posted_balance_nano > 0
		)
		SELECT
			(SELECT count(*) FROM pos),
			(SELECT COALESCE(sum(posted_balance_nano), 0)::text FROM pos),
			(WITH agg AS (SELECT max(posted_balance_nano) AS top, COALESCE(sum(posted_balance_nano), 0)::numeric AS total FROM pos)
				SELECT (top::numeric / total)::text FROM agg WHERE total > 0),
			(WITH top5 AS (SELECT posted_balance_nano FROM pos ORDER BY posted_balance_nano DESC LIMIT 5),
				agg AS (SELECT COALESCE(sum(posted_balance_nano), 0)::numeric AS top_sum, COALESCE((SELECT sum(posted_balance_nano) FROM pos), 0)::numeric AS total FROM top5)
				SELECT (top_sum / total)::text FROM agg WHERE total > 0),
			(WITH agg AS (SELECT posted_balance_nano::numeric AS balance, COALESCE((SELECT sum(posted_balance_nano) FROM pos), 0)::numeric AS total FROM pos)
				SELECT sum((balance / total) * (balance / total))::text FROM agg WHERE total > 0)`).
		Scan(&count, &total, &top1, &top5, &hhi); err != nil {
		return err
	}
	result.Concentration = ops.ConcentrationMetrics{PositiveUserCount: count, TotalPositive: nanoIntegerToPoints(total), Top1Share: top1, Top5Share: top5, HHI: hhi}
	return nil
}

// OpsAnomalies returns hard invariant violations plus attention items.
func (s *Store) OpsAnomalies(ctx context.Context) (ops.Anomalies, error) {
	result := ops.Anomalies{CheckedAt: time.Now().UTC(), Hard: []ops.Anomaly{}, Attention: []ops.Anomaly{}}

	ledgerSnapshot, err := s.Metrics(ctx)
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
			Detail: "全账户余额和为 " + ledgerSnapshot.TotalPostedBalance + " 积分，期望恒为 0",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}
	if ledgerSnapshot.PostedProjectionMismatchAccounts > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{
			Kind: "posted_projection_difference", Count: ledgerSnapshot.PostedProjectionMismatchAccounts,
			Detail: "入账投影与分录合计差异 " + ledgerSnapshot.PostedProjectionDifference + " 积分",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}
	if ledgerSnapshot.HoldProjectionMismatchAccounts > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{
			Kind: "hold_projection_difference", Count: ledgerSnapshot.HoldProjectionMismatchAccounts,
			Detail: "资产/授权投影与持有合计差异（资产 " + ledgerSnapshot.AssetReservationDifference + "，授权 " + ledgerSnapshot.SpendAuthorizationDifference + "）",
			Drilldown: "/admin/ops?drilldown=ledger-accounts",
		})
	}

	var withoutSettlement, withoutLedgerTx, quantityViolations, holdViolations int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM api_calls c LEFT JOIN api_call_settlements s ON s.call_id = c.id
				WHERE c.status = 'succeeded' AND s.call_id IS NULL),
			(SELECT count(*) FROM api_call_settlements s
				WHERE (s.kind = 'captured' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.capture_transaction_id))
					OR (s.kind = 'self_usage' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.self_transaction_id))),
			(SELECT count(*) FROM c2c_orders WHERE total_nano <> available_nano + allocated_nano + settled_nano + closed_nano),
			(SELECT count(*) FROM c2c_orders o LEFT JOIN ledger_holds h ON h.id = o.parent_hold_id
				WHERE o.side = 'sell' AND o.status IN ('open', 'allocated')
					AND (o.parent_hold_id IS NULL OR h.remaining_nano <> o.available_nano + o.allocated_nano))`).
		Scan(&withoutSettlement, &withoutLedgerTx, &quantityViolations, &holdViolations); err != nil {
		return ops.Anomalies{}, err
	}
	if withoutSettlement > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "succeeded_call_without_settlement", Count: withoutSettlement, Detail: "成功调用缺少结算事实", Drilldown: "/admin/ops?drilldown=api-calls"})
	}
	if withoutLedgerTx > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "settlement_without_ledger", Count: withoutLedgerTx, Detail: "结算缺少账本交易", Drilldown: "/admin/ops?drilldown=api-calls"})
	}
	if quantityViolations > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "c2c_quantity_invariant", Count: quantityViolations, Detail: "C2C 挂单数量恒等式被违反", Drilldown: "/admin/ops?drilldown=c2c-orders"})
	}
	if holdViolations > 0 {
		result.Hard = append(result.Hard, ops.Anomaly{Kind: "c2c_hold_invariant", Count: holdViolations, Detail: "C2C 父持有与可成交/已分配数量不一致", Drilldown: "/admin/ops?drilldown=c2c-orders"})
	}
	for _, item := range result.Hard {
		result.HardCount += item.Count
	}

	var openDisputes int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM c2c_trades WHERE status = 'disputed'`).Scan(&openDisputes); err != nil {
		return ops.Anomalies{}, err
	}
	result.Attention = append(result.Attention,
		ops.Anomaly{Kind: "open_disputes", Attention: true, Count: openDisputes, Detail: "处于争议中的交易数量", Drilldown: "/admin/c2c/disputes"},
		ops.Anomaly{Kind: "over_limit_accounts", Attention: true, Count: ledgerSnapshot.OverLimitAccounts, Detail: "超出可用信用的账户数量（无既定阈值，仅呈现事实）", Drilldown: "/admin/ops?drilldown=ledger-accounts"},
		ops.Anomaly{Kind: "credit_frozen_accounts", Attention: true, Count: ledgerSnapshot.CreditFrozenAccounts, Detail: "信用被冻结的账户数量", Drilldown: "/admin/accounts"},
	)
	return result, nil
}

// OpsRunInspection executes the fixed invariant set and persists one row.
func (s *Store) OpsRunInspection(ctx context.Context, triggeredBy string) (ops.InspectionRecord, error) {
	ledgerSnapshot, err := s.Metrics(ctx)
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

	var withoutSettlement, withoutLedgerTx, quantityViolations, holdViolations int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM api_calls c LEFT JOIN api_call_settlements s ON s.call_id = c.id
				WHERE c.status = 'succeeded' AND s.call_id IS NULL),
			(SELECT count(*) FROM api_call_settlements s
				WHERE (s.kind = 'captured' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.capture_transaction_id))
					OR (s.kind = 'self_usage' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.self_transaction_id))),
			(SELECT count(*) FROM c2c_orders WHERE total_nano <> available_nano + allocated_nano + settled_nano + closed_nano),
			(SELECT count(*) FROM c2c_orders o LEFT JOIN ledger_holds h ON h.id = o.parent_hold_id
				WHERE o.side = 'sell' AND o.status IN ('open', 'allocated')
					AND (o.parent_hold_id IS NULL OR h.remaining_nano <> o.available_nano + o.allocated_nano))`).
		Scan(&withoutSettlement, &withoutLedgerTx, &quantityViolations, &holdViolations); err != nil {
		return ops.InspectionRecord{}, err
	}

	var record ops.InspectionRecord
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO ops_inspections (
			inspection_version, triggered_by,
			zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
			zero_sum_difference_nano, posted_projection_difference_nano, asset_projection_difference_nano, authorization_projection_difference_nano,
			successful_calls_without_settlement, settlements_without_ledger_transaction,
			c2c_quantity_violations, c2c_hold_violations, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id::text, inspection_version, triggered_by, zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
			zero_sum_difference_nano, posted_projection_difference_nano, asset_projection_difference_nano, authorization_projection_difference_nano,
			successful_calls_without_settlement, settlements_without_ledger_transaction, c2c_quantity_violations, c2c_hold_violations, checked_at`,
		ops.InspectionVersion, triggeredBy,
		zeroSum == 0,
		postedDifference == 0 && assetDifference == 0 && authorizationDifference == 0,
		withoutSettlement == 0 && withoutLedgerTx == 0,
		quantityViolations == 0 && holdViolations == 0,
		int64(zeroSum), int64(postedDifference), int64(assetDifference), int64(authorizationDifference),
		withoutSettlement, withoutLedgerTx, quantityViolations, holdViolations,
		"{}").
		Scan(&record.ID, &record.InspectionVersion, &record.TriggeredBy, &record.ZeroSumOK, &record.ProjectionOK, &record.CallSettlementOK, &record.C2CConsistencyOK,
			&record.ZeroSumDifference, &record.PostedProjectionDifference, &record.AssetProjectionDifference, &record.AuthorizationProjectionDiff,
			&record.SuccessfulCallsWithoutSettlement, &record.SettlementsWithoutLedgerTx, &record.C2CQuantityViolations, &record.C2CHoldViolations, &record.CheckedAt); err != nil {
		return ops.InspectionRecord{}, err
	}
	return record, nil
}

// OpsListInspections returns the most recent persisted inspections.
func (s *Store) OpsListInspections(ctx context.Context, limit int64) ([]ops.InspectionRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, inspection_version, triggered_by, zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
			zero_sum_difference_nano::text, posted_projection_difference_nano::text, asset_projection_difference_nano::text, authorization_projection_difference_nano::text,
			successful_calls_without_settlement, settlements_without_ledger_transaction, c2c_quantity_violations, c2c_hold_violations, checked_at
		FROM ops_inspections ORDER BY checked_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]ops.InspectionRecord, 0, limit)
	for rows.Next() {
		var record ops.InspectionRecord
		if err := rows.Scan(&record.ID, &record.InspectionVersion, &record.TriggeredBy, &record.ZeroSumOK, &record.ProjectionOK, &record.CallSettlementOK, &record.C2CConsistencyOK,
			&record.ZeroSumDifference, &record.PostedProjectionDifference, &record.AssetProjectionDifference, &record.AuthorizationProjectionDiff,
			&record.SuccessfulCallsWithoutSettlement, &record.SettlementsWithoutLedgerTx, &record.C2CQuantityViolations, &record.C2CHoldViolations, &record.CheckedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// OpsTrialSummary aggregates non-sensitive trial evidence counts.
func (s *Store) OpsTrialSummary(ctx context.Context) (ops.TrialSummary, error) {
	summary := ops.TrialSummary{GeneratedAt: time.Now().UTC()}
	ledgerSnapshot, err := s.Metrics(ctx)
	if err != nil {
		return ops.TrialSummary{}, err
	}
	summary.LedgerZeroSumOK = ledgerSnapshot.TotalPostedBalance == "0"

	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM accounts WHERE is_admin = false),
			(SELECT count(*) FROM channels WHERE status = 'published'),
			(SELECT count(DISTINCT offer_id) FROM channel_validation_attempts WHERE status = 'passed'),
			(SELECT count(*) FROM api_keys WHERE status = 'active'),
			(SELECT count(*) FROM api_calls WHERE status = 'succeeded'),
			(SELECT count(*) FROM api_calls WHERE status = 'failed'),
			(SELECT count(*) FROM api_calls WHERE status = 'incomplete'),
			(SELECT min(created_at) FROM api_calls),
			(SELECT max(completed_at) FROM api_calls WHERE status IN ('succeeded', 'failed', 'incomplete', 'cancelled')),
			(SELECT count(*) FROM c2c_orders WHERE status IN ('open', 'allocated')),
			(SELECT count(*) FROM c2c_trades WHERE status = 'released_to_buyer'),
			(SELECT count(*) FROM c2c_trades WHERE status = 'disputed')`).
		Scan(&summary.NonAdminAccounts, &summary.PublishedChannels, &summary.PassedOffers, &summary.ActiveAPIKeys,
			&summary.CallsSucceeded, &summary.CallsFailed, &summary.CallsIncomplete,
			&summary.FirstCallAt, &summary.LastTerminalCallAt,
			&summary.C2COpenOrders, &summary.C2CReleasedTrades, &summary.C2CDisputedOpen); err != nil {
		return ops.TrialSummary{}, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE zero_sum_ok AND projection_ok AND call_settlement_ok AND c2c_consistency_ok), count(*),
			max(checked_at), bool_and(zero_sum_ok AND projection_ok AND call_settlement_ok AND c2c_consistency_ok) FILTER (WHERE checked_at = (SELECT max(checked_at) FROM ops_inspections))
		FROM ops_inspections`).
		Scan(&summary.InspectionPassCount, &summary.InspectionTotalCount, &summary.LastInspectionAt, &summary.LastInspectionOK); err != nil {
		return ops.TrialSummary{}, err
	}
	return summary, nil
}
