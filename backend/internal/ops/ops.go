// Package ops defines the operations metrics, anomaly and inspection
// contracts delivered by Feature #22. Amounts use nano-point integers on the
// wire divided by money.Scale into decimal point strings; empty sample
// aggregates stay null instead of being fabricated as zero.
package ops

import (
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// InspectionVersion pins the invariant set persisted with each inspection.
const InspectionVersion = "ops-v1"

// Window is a UTC [from, to) observation window. Both bounds are required so
// no caller silently relies on an implicit "now" that drifts per query.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func (window Window) Validate() bool {
	return !window.From.IsZero() && !window.To.IsZero() && window.From.Before(window.To)
}

// NegativeBalanceRisk expresses one negative user account without inventing
// debt maturity rules: the current balance, when it entered the current
// negative streak, its last financial activity, and inactivity buckets.
type NegativeBalanceRisk struct {
	AccountID                 string  `json:"account_id"`
	Username                  string  `json:"username"`
	PostedBalance             string  `json:"posted_balance"`
	NegativeSince             string  `json:"negative_since"`
	LastFinancialActivity     string  `json:"last_financial_activity"`
	InactiveDays              int64   `json:"inactive_days"`
	OverLimit                 bool    `json:"over_limit"`
	CreditLimit               string  `json:"credit_limit"`
}

// APIMetrics counts the call funnel inside the window. The success rate
// denominator is exactly the calls that reached upstream and reached a
// terminal state; empty samples keep a null rate.
type APIMetrics struct {
	PrecheckRejected    int64   `json:"precheck_rejected"`
	ReachedUpstream     int64   `json:"reached_upstream"`
	Succeeded           int64   `json:"succeeded"`
	AllFailed           int64   `json:"all_failed"`
	IncompleteAfterCommit int64 `json:"incomplete_after_commit"`
	Cancelled           int64   `json:"cancelled"`
	TerminalReached     int64   `json:"terminal_reached"`
	SuccessRate         *string `json:"success_rate"`
	AttemptCount        int64   `json:"attempt_count"`
	AttemptSucceeded    int64   `json:"attempt_succeeded"`
	AverageTTFTMillis   *int64  `json:"average_ttft_milliseconds"`
	AverageTPS          *string `json:"average_tokens_per_second"`
}

// ConsumptionMetrics separates consumer spend, sharer income (own vs other
// consumers' nominal income) and platform fee inside the window.
type ConsumptionMetrics struct {
	ConsumerSpendNano      int64  `json:"-"`
	ConsumerSpend          string `json:"consumer_spend"`
	ProviderIncomeNano     int64  `json:"-"`
	ProviderIncome         string `json:"provider_income"`
	OwnUsageIncomeNano     int64  `json:"-"`
	OwnUsageIncome         string `json:"own_usage_income"`
	OtherConsumerIncome    string `json:"other_consumer_income"`
	PlatformFee            string `json:"platform_fee"`
	PlatformFeeNano        int64  `json:"-"`
}

// C2COrderStatusCount groups parent orders by side and status.
type C2COrderStatusCount struct {
	Side   string `json:"side"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// C2CTradeStatusCount groups trades by status inside the window.
type C2CTradeStatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// C2CMarketQuote is the non-windowed order-book snapshot; any missing side
// keeps the whole spread null rather than fabricating a price.
type C2CMarketQuote struct {
	LastTradedPriceFen  *int64 `json:"last_traded_price_fen"`
	BestBidPriceFen     *int64 `json:"best_bid_price_fen"`
	BestAskPriceFen     *int64 `json:"best_ask_price_fen"`
	SpreadFen           *int64 `json:"spread_fen"`
}

// C2CMetrics combines windowed order/trade counts with the quote snapshot.
type C2CMetrics struct {
	Orders []C2COrderStatusCount `json:"orders"`
	Trades []C2CTradeStatusCount `json:"trades"`
	Quote  C2CMarketQuote        `json:"quote"`
}

// ConcentrationMetrics covers user positive balances only; system accounts
// are excluded and reported separately by the ledger section.
type ConcentrationMetrics struct {
	PositiveUserCount int64   `json:"positive_user_count"`
	TotalPositive     string  `json:"total_positive"`
	Top1Share         *string `json:"top1_share"`
	Top5Share         *string `json:"top5_share"`
	HHI               *string `json:"hhi"`
}

// LedgerMetricsView mirrors the existing admin ledger metrics contract so
// one frontend type serves both endpoints.
type LedgerMetricsView struct {
	TotalPostedBalance               string `json:"total_posted_balance"`
	PositivePostedBalance            string `json:"positive_posted_balance"`
	NegativePostedBalance            string `json:"negative_posted_balance"`
	PostedProjectionDifference       string `json:"posted_projection_difference"`
	PostedProjectionMismatchAccounts int64  `json:"posted_projection_mismatch_accounts"`
	AssetReservationDifference       string `json:"asset_reservation_difference"`
	SpendAuthorizationDifference     string `json:"spend_authorization_difference"`
	HoldProjectionMismatchAccounts   int64  `json:"hold_projection_mismatch_accounts"`
	ZeroSum                          bool   `json:"zero_sum"`
	LedgerConsistent                 bool   `json:"ledger_consistent"`
	TotalCreditLimit                 string `json:"total_credit_limit"`
	CreditCapacityUsed               string `json:"credit_capacity_used"`
	AssetReserved                    string `json:"asset_reserved"`
	SpendAuthorized                  string `json:"spend_authorized"`
	IncentivePostedBalance           string `json:"incentive_posted_balance"`
	LossPostedBalance                string `json:"loss_posted_balance"`
	OverLimitAccounts                int64  `json:"over_limit_accounts"`
	CreditFrozenAccounts             int64  `json:"credit_frozen_accounts"`
	LedgerAccountCount               int64  `json:"ledger_account_count"`
}

// NewLedgerMetricsView derives the consistency flags once, server-side.
func NewLedgerMetricsView(metrics ledger.Metrics) LedgerMetricsView {
	return LedgerMetricsView{
		TotalPostedBalance: metrics.TotalPostedBalance,
		PositivePostedBalance: metrics.PositivePostedBalance,
		NegativePostedBalance: metrics.NegativePostedBalance,
		PostedProjectionDifference: metrics.PostedProjectionDifference,
		PostedProjectionMismatchAccounts: metrics.PostedProjectionMismatchAccounts,
		AssetReservationDifference: metrics.AssetReservationDifference,
		SpendAuthorizationDifference: metrics.SpendAuthorizationDifference,
		HoldProjectionMismatchAccounts: metrics.HoldProjectionMismatchAccounts,
		ZeroSum: metrics.TotalPostedBalance == "0",
		LedgerConsistent: metrics.TotalPostedBalance == "0" && metrics.PostedProjectionDifference == "0" && metrics.AssetReservationDifference == "0" && metrics.SpendAuthorizationDifference == "0",
		TotalCreditLimit: metrics.TotalCreditLimit,
		CreditCapacityUsed: metrics.UsedCredit,
		AssetReserved: metrics.AssetReserved,
		SpendAuthorized: metrics.SpendAuthorized,
		IncentivePostedBalance: metrics.IncentivePostedBalance,
		LossPostedBalance: metrics.LossPostedBalance,
		OverLimitAccounts: metrics.OverLimitAccounts,
		CreditFrozenAccounts: metrics.CreditFrozenAccounts,
		LedgerAccountCount: metrics.AccountCount,
	}
}

// Metrics is the unified operations snapshot for one window.
type Metrics struct {
	Window
	Ledger              LedgerMetricsView     `json:"ledger"`
	EffectiveCredit     string             `json:"effective_credit"`
	NegativeBalances    []NegativeBalanceRisk `json:"negative_balances"`
	API                 APIMetrics         `json:"api"`
	Consumption         ConsumptionMetrics `json:"consumption"`
	C2C                 C2CMetrics         `json:"c2c"`
	Concentration       ConcentrationMetrics `json:"concentration"`
}

// ProviderIncomeRow is one sharer's windowed income projection. SuccessRate
// stays null when the window has no terminal upstream attempts.
type ProviderIncomeRow struct {
	AccountID           string  `json:"account_id"`
	DisplayName         string  `json:"display_name"`
	TotalIncome         string  `json:"total_income"`
	OtherConsumerIncome string  `json:"other_consumer_income"`
	OwnUsageIncome      string  `json:"own_usage_income"`
	SuccessRate         *string `json:"success_rate"`
}

// ProviderIncomeSnapshot is the admin sharer-income table for one window.
type ProviderIncomeSnapshot struct {
	Window
	TotalIncome         string              `json:"total_income"`
	OtherConsumerIncome string              `json:"other_consumer_income"`
	OwnUsageIncome      string              `json:"own_usage_income"`
	ActiveProviders     int64               `json:"active_providers"`
	Providers           []ProviderIncomeRow `json:"providers"`
}

// Anomaly is one hard invariant violation with a fixed drilldown target.
// Attention items carry Attention=true and never use invented thresholds.
type Anomaly struct {
	Kind        string `json:"kind"`
	Attention   bool   `json:"attention"`
	Count       int64  `json:"count"`
	Detail      string `json:"detail"`
	Drilldown   string `json:"drilldown"`
}

// Anomalies is the hard-anomaly and attention snapshot.
type Anomalies struct {
	Hard       []Anomaly `json:"hard_anomalies"`
	Attention  []Anomaly `json:"attention_items"`
	HardCount  int64     `json:"hard_count"`
	CheckedAt  time.Time `json:"checked_at"`
}

// InspectionRecord is one persisted inspection run.
type InspectionRecord struct {
	ID                             string    `json:"id"`
	InspectionVersion              string    `json:"inspection_version"`
	TriggeredBy                    string    `json:"triggered_by"`
	ZeroSumOK                      bool      `json:"zero_sum_ok"`
	ProjectionOK                   bool      `json:"projection_ok"`
	CallSettlementOK               bool      `json:"call_settlement_ok"`
	C2CConsistencyOK               bool      `json:"c2c_consistency_ok"`
	ZeroSumDifference              string    `json:"zero_sum_difference"`
	PostedProjectionDifference     string    `json:"posted_projection_difference"`
	AssetProjectionDifference      string    `json:"asset_projection_difference"`
	AuthorizationProjectionDiff    string    `json:"authorization_projection_difference"`
	SuccessfulCallsWithoutSettlement int64   `json:"successful_calls_without_settlement"`
	SettlementsWithoutLedgerTx     int64     `json:"settlements_without_ledger_transaction"`
	C2CQuantityViolations          int64     `json:"c2c_quantity_violations"`
	C2CHoldViolations              int64     `json:"c2c_hold_violations"`
	CheckedAt                      time.Time `json:"checked_at"`
}

// TrialSummary is the maintainer-facing aggregate evidence entry. It contains
// only counts, times and statuses, never raw errors or credentials, and never
// claims participants are real people or that external CNY arrived.
type TrialSummary struct {
	GeneratedAt                time.Time `json:"generated_at"`
	NonAdminAccounts           int64     `json:"non_admin_accounts"`
	PublishedChannels          int64     `json:"published_channels"`
	PassedOffers               int64     `json:"passed_offers"`
	ActiveAPIKeys              int64     `json:"active_api_keys"`
	CallsSucceeded             int64     `json:"calls_succeeded"`
	CallsFailed                int64     `json:"calls_failed"`
	CallsIncomplete            int64     `json:"calls_incomplete"`
	FirstCallAt                *time.Time `json:"first_call_at"`
	LastTerminalCallAt         *time.Time `json:"last_terminal_call_at"`
	C2COpenOrders              int64     `json:"c2c_open_orders"`
	C2CReleasedTrades          int64     `json:"c2c_released_trades"`
	C2CDisputedOpen            int64     `json:"c2c_disputed_open"`
	LedgerZeroSumOK            bool      `json:"ledger_zero_sum_ok"`
	LastInspectionOK           *bool     `json:"last_inspection_ok"`
	LastInspectionAt           *time.Time `json:"last_inspection_at"`
	InspectionPassCount        int64     `json:"inspection_pass_count"`
	InspectionTotalCount       int64     `json:"inspection_total_count"`
}

// NormalizeUsername keeps usernames trimmed for risk rows.
func NormalizeUsername(value string) string {
	return strings.TrimSpace(value)
}
