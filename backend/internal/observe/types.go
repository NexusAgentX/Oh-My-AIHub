// Package observe is the read side of the platform's observability (Feature G):
// call lists and details, usage aggregation, channel statistics, points
// reports, the live ledger reconciliation and the administrator overview. It
// never writes business state, with one exception: the administrator's repair
// of a call whose booking failed.
//
// Everything is computed on demand from calls, ledger entries and trades; no
// snapshot or history tables exist (Epic #170, decision 12).
package observe

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrNotFound     = errors.New("observed resource not found")
	ErrInvalidInput = errors.New("invalid observation query")
	// ErrNotRepairable reports a call that is not eligible for repair
	// (already booked, not billable, or served by the caller's own channel).
	ErrNotRepairable = errors.New("call is not repairable")
)

// ---- calls ----

type AccountRef struct {
	ID, Username, DisplayName string
}

type KeyRef struct{ ID, Name string }

type ChannelRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Usage struct {
	InputTokens, OutputTokens, CacheWriteTokens, CacheReadTokens int64
}

// Total is every token kind added up.
func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheWriteTokens + u.CacheReadTokens
}

// Attempt mirrors one element of calls.attempts as the gateway writes it.
type Attempt struct {
	Channel      *ChannelRef `json:"channel"`
	StatusCode   *int        `json:"status_code"`
	ErrorCode    *string     `json:"error_code"`
	ErrorMessage *string     `json:"error_message"`
	ConnectMS    *int        `json:"connect_ms"`
	TTFTMS       *int        `json:"ttft_ms"`
	DurationMS   *int        `json:"duration_ms"`
	ResponseByte *int64      `json:"response_bytes"`
	EndReason    string      `json:"end_reason"`
}

// Succeeded reports whether the attempt delivered the response.
func (a Attempt) Succeeded() bool { return a.EndReason == "completed" }

const (
	OutcomeSucceeded = "succeeded"
	OutcomeUnbilled  = "succeeded_unbilled"
	OutcomeProgress  = "in_progress"
)

// IsSuccess reports whether an outcome counts as a successful call.
func IsSuccess(outcome string) bool { return outcome == OutcomeSucceeded || outcome == OutcomeUnbilled }

// CallFilter selects calls. Empty strings and nil pointers mean "no filter".
type CallFilter struct {
	AccountID string
	// ScopeChannelID selects calls that served or attempted this channel
	// (the channel owner's view).
	ScopeChannelID string
	// FinalChannelID selects calls served by this channel.
	FinalChannelID string
	APIKeyID       string
	Model          string
	Format         string
	Outcome        string
	Tag            string
	RequestID      string
	From, To       *time.Time
	MinDurationMS  *int
	MaxDurationMS  *int
	MinTokens      *int64
	MaxTokens      *int64
	MinCharged     *money.Amount
	MaxCharged     *money.Amount
}

// CallCursor is the keyset position after the last returned call.
type CallCursor struct {
	At time.Time
	ID string
}

// CallRow is one call in a list or a live-stream message.
type CallRow struct {
	ID             string
	CreatedAt      time.Time
	CompletedAt    *time.Time
	Account        AccountRef
	Key            *KeyRef
	ModelID        *string
	RequestedModel string
	Format         string
	Stream         bool
	Tag            *string
	Outcome        string
	Channel        *ChannelRef
	Usage          Usage
	Cost, Fee      money.Amount
	LedgerTxID     *string
	TTFTMS         *int
	DurationMS     *int
	AttemptCount   int
	// ScopeAttempts are the attempts on the filter's scope channel.
	ScopeAttempts []Attempt
}

// Charged is what the caller was actually debited: cost plus fee once the
// call is booked, zero otherwise (including the owner's own channel).
func (r CallRow) Charged() money.Amount {
	if r.LedgerTxID == nil {
		return 0
	}
	return money.FromNano(r.Cost.Nano() + r.Fee.Nano())
}

// Revenue is what the channel that served the call received.
func (r CallRow) Revenue() money.Amount {
	if r.LedgerTxID == nil {
		return 0
	}
	return r.Cost
}

// CallStats summarizes the calls matching a filter.
type CallStats struct {
	Calls, Succeeded, Failed int64
	Charged                  money.Amount
	InputTokens              int64
	OutputTokens             int64
	CacheTokens              int64
	TTFTP50MS, TTFTP95MS     *int
}

func (s CallStats) TotalTokens() int64 { return s.InputTokens + s.OutputTokens + s.CacheTokens }

// SuccessRate is succeeded / (succeeded + failed); nil when nothing finished.
func (s CallStats) SuccessRate() *float64 { return rate(s.Succeeded, s.Succeeded+s.Failed) }

func rate(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	value := float64(part) / float64(whole)
	return &value
}

// CallRecord is the complete stored call.
type CallRecord struct {
	CallRow
	RoutingMode     *string
	RoutingSource   *string
	ClientUserAgent *string
	Attempts        []Attempt
	PriceSnapshot   json.RawMessage
	TokensPerSecond *float64
	IntervalP50MS   *int
	IntervalP95MS   *int
	ResponseBytes   *int64
}

// Viewer identifies who asks for a call detail.
type Viewer struct {
	AccountID string
	Admin     bool
}

// CallDetail is a call as one viewer may see it. Owner and Admin views carry
// everything; a channel owner sees only the attempts on their own channels and
// no consumer identity, key, tag or routing.
type CallDetail struct {
	CallRecord
	// ChannelOwnerView is true when the viewer sees the call as a channel owner.
	ChannelOwnerView bool
}

// ChannelOwner is a channel's owner, for permission checks.
type ChannelOwner struct {
	ID, OwnerID, Name string
}

// ---- usage ----

type UsageQuery struct {
	AccountID string
	// Revenue selects the channel-income view of the account's channels.
	Revenue   bool
	GroupBy   string
	From, To  time.Time
	APIKeyID  string
	ChannelID string
	Model     string
	Tag       string
}

type UsageRow struct {
	Key, Label                                                   string
	Calls, Succeeded                                             int64
	InputTokens, OutputTokens, CacheWriteTokens, CacheReadTokens int64
	Amount                                                       money.Amount
}

// ---- channel statistics ----

type ChannelBrief struct {
	ID, OwnerID, Name string
	DailyRevenueCap   *money.Amount
}

type AttemptWindow struct {
	Attempts, Successes  int64
	TTFTP50MS, TTFTP95MS *int
}

type Bucket struct {
	At                  time.Time
	Attempts, Successes int64
}

type DayBucket struct {
	Day                 string
	Attempts, Successes int64
	Revenue             money.Amount
}

type ModelBucket struct {
	ModelID             string
	Attempts, Successes int64
	Revenue             money.Amount
}

type StatusBucket struct {
	// StatusCode is nil when no response was received.
	StatusCode *int
	Count      int64
}

type Failure struct {
	CallID    string
	CreatedAt time.Time
	ModelID   *string
	Attempt   Attempt
}

type ChannelEvent struct {
	ID        int64
	Kind      string
	Reason    string
	CreatedAt time.Time
}

// ChannelStatsData is everything the channel statistics page shows, as read
// from the database; derived percentages are computed by the service.
type ChannelStatsData struct {
	Window          AttemptWindow
	Last24h, Last7d AttemptWindow
	SpeedP50        *float64
	SpeedP95        *float64
	Hourly          []Bucket
	Daily           []DayBucket
	Models          []ModelBucket
	StatusCodes     []StatusBucket
	Failures        []Failure
	Revenue         money.Amount
	TodayRevenue    money.Amount
	Events          []ChannelEvent
}

// ---- user points ----

type DayFlow struct {
	Day                string
	Income, Spend, Net money.Amount
}

// TrendPoint is one day of a user's balance history.
type TrendPoint struct {
	Day                    string
	Balance, Income, Spend money.Amount
}

// Period reconciles a user's balance over a period.
type Period struct {
	From, To      time.Time
	Opening       money.Amount
	Closing       money.Amount
	Income, Spend money.Amount
	CallSpend     money.Amount
	ChannelIncome money.Amount
	C2CBuy        money.Amount
	C2CSell       money.Amount
	C2CReturn     money.Amount
	Adjustments   money.Amount
	WriteOffs     money.Amount
	Difference    money.Amount
}

// TypeFlow is the sum of one user's entries of one transaction type and direction.
type TypeFlow struct {
	Type   string
	Inflow bool
	Amount money.Amount
}

type KeySpend struct {
	Key     *KeyRef
	Spend   money.Amount
	Entries int64
}

type EntrySummary struct {
	ByDay []DayFlow
	ByKey []KeySpend
}

// EntryFilter filters a user's ledger entries.
type EntryFilter struct {
	AccountID string
	Type      string
	APIKeyID  string
	From, To  *time.Time
}

// ExportEntry is one ledger entry of a user for the CSV export.
type ExportEntry struct {
	CreatedAt    time.Time
	Type         string
	Reason       string
	RelatedType  string
	RelatedID    string
	Amount       money.Amount
	BalanceAfter money.Amount
	KeyName      string
}

// ---- administrator ----

type LedgerAccountRef struct {
	Kind       string
	Account    *AccountRef
	SystemCode *string
}

type Balances struct {
	UserPositive, UserNegative, Escrow, PlatformRevenue, BadDebt, Total money.Amount
	TotalCreditLimit                                                    money.Amount
	WriteOffs, EscrowOrders, EscrowTradesInProgress                     int64
}

// CreditIssued is the absolute value of the users' negative balances.
func (b Balances) CreditIssued() money.Amount { return money.FromNano(-b.UserNegative.Nano()) }

type DayStat struct {
	Day                              string
	Circulation, CreditIssued        money.Amount
	PlatformRevenue, BadDebt, Escrow money.Amount
	APIVolume, APIFee, C2CVolume     money.Amount
	C2CAvgPriceFen                   *float64
}

type Risk struct {
	Account        AccountRef
	Balance        money.Amount
	CreditLimit    money.Amount
	Kind           string
	NegativeDays   *int
	LastActivityAt *time.Time
}

type Holder struct {
	Account AccountRef
	Balance money.Amount
	Share   float64
}

type Concentration struct {
	Top5Share *float64
	Top       []Holder
}

type BalanceMismatch struct {
	Account      LedgerAccountRef
	Balance      money.Amount
	EntriesTotal money.Amount
}

type MissingCall struct {
	CallID    string
	CreatedAt time.Time
	Account   AccountRef
	Channel   ChannelRef
	Outcome   string
	Charged   money.Amount
}

type MissingTrade struct {
	TradeID    string
	Status     string
	Amount     money.Amount
	ResolvedAt *time.Time
}

// Checks are the five live reconciliation checks.
type Checks struct {
	CheckedAt time.Time
	// ZeroSum is check ①.
	ZeroSumTotal money.Amount
	ZeroSumOK    bool
	// ② per-account balance equals entries.
	Mismatches []BalanceMismatch
	// ③ escrow equals open orders.
	EscrowBalance, OrdersTotal, EscrowDifference money.Amount
	// ④ billable calls without a ledger transaction.
	MissingCalls     []MissingCall
	MissingCallCount int64
	// ⑤ released trades without a ledger transaction.
	MissingTrades     []MissingTrade
	MissingTradeCount int64
}

func (c Checks) AccountsOK() bool { return len(c.Mismatches) == 0 }
func (c Checks) EscrowOK() bool   { return c.EscrowDifference == 0 }
func (c Checks) CallsOK() bool    { return c.MissingCallCount == 0 }
func (c Checks) TradesOK() bool   { return c.MissingTradeCount == 0 }
func (c Checks) AllPassed() bool {
	return c.ZeroSumOK && c.AccountsOK() && c.EscrowOK() && c.CallsOK() && c.TradesOK()
}

// FailedChecks names the failed checks for metrics and the attention list.
func (c Checks) FailedChecks() []string {
	var failed []string
	if !c.ZeroSumOK {
		failed = append(failed, "zero_sum")
	}
	if !c.AccountsOK() {
		failed = append(failed, "account_balances")
	}
	if !c.EscrowOK() {
		failed = append(failed, "escrow")
	}
	if !c.CallsOK() {
		failed = append(failed, "billing_calls")
	}
	if !c.TradesOK() {
		failed = append(failed, "released_trades")
	}
	return failed
}

type AdminPointsReport struct {
	Balances      Balances
	Checks        Checks
	Trend         []DayStat
	Risks         []Risk
	Concentration Concentration
}

// ---- overview ----

type CallWindow struct {
	Calls, Succeeded, Unbilled int64
	Spend, Fee                 money.Amount
}

type FailingChannel struct {
	ID, Name            string
	Attempts, Successes int64
}

type C2CCounts struct {
	OpenOrders, AwaitingPayment, OpenDisputes int64
}

type C2CWindow struct {
	Trades   int64
	Volume   money.Amount
	TotalFen int64
}

// AvgPriceFen is the quantity-weighted average price in fen per point.
func (w C2CWindow) AvgPriceFen() *float64 {
	if w.Volume <= 0 {
		return nil
	}
	value := float64(w.TotalFen) / (float64(w.Volume.Nano()) / 1e9)
	return &value
}

type OverviewData struct {
	Today, Last24h CallWindow
	Failing        []FailingChannel
	C2C            C2CCounts
	C2C24h         C2CWindow
	Balances       Balances
	Checks         Checks
	Risks          []Risk
	Concentration  Concentration
}

// Attention kinds of the administrator overview.
const (
	AttentionDispute       = "dispute"
	AttentionOverLimit     = "over_limit"
	AttentionNegative      = "negative_balance"
	AttentionConcentration = "credit_concentration"
	AttentionChannelFail   = "channel_failing"
	AttentionUnbilled      = "unbilled_usage"
	AttentionUnbalanced    = "ledger_unbalanced"
	AttentionReconcile     = "reconciliation_failed"
)

type AttentionItem struct {
	Kind     string
	Severity string
	Title    string
	Count    int
	Link     string
}

// Overview is the administrator's landing page.
type Overview struct {
	Attention []AttentionItem
	OverviewData
}

// ---- ledger browsing ----

type TxFilter struct {
	ID          string
	Type        string
	AccountID   string
	RelatedType string
	RelatedID   string
	From, To    *time.Time
}

type TxEntry struct {
	Account                             LedgerAccountRef
	Amount, BalanceBefore, BalanceAfter money.Amount
}

type Transaction struct {
	ID, Type, IdempotencyKey string
	RelatedType, RelatedID   string
	Actor                    *AccountRef
	Reason                   string
	CreatedAt                time.Time
	RelatedSummary           string
	Entries                  []TxEntry
	PriceSnapshot            json.RawMessage
}

// RepairAction is what the administrator decides for an unbooked call.
type RepairAction string

const (
	RepairCharge RepairAction = "charge"
	RepairVoid   RepairAction = "void"
)

type Repair struct {
	ActorID string
	CallID  string
	Action  RepairAction
	Reason  string
}

// RepairResult is the outcome of a repair: the booked transaction (charge) or empty (void).
type RepairResult struct {
	TransactionID string
}

// ---- metrics ----

// LedgerTotals are cumulative per transaction type.
type LedgerTotals struct {
	Type         string
	Transactions int64
	Amount       money.Amount
}

// ChannelState is a channel as the metrics see it.
type ChannelState struct {
	ID     string
	Listed bool
}
