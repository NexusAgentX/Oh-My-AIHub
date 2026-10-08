package observe

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	// MaxExportRows bounds the CSV export of ledger entries.
	MaxExportRows = 50000
	// ErrorRetention is how long raw upstream error text is kept (Epic #170, decision 11).
	ErrorRetention = 30 * 24 * time.Hour

	maxUsageWindow  = 366 * 24 * time.Hour
	maxStatsWindow  = 90 * 24 * time.Hour
	negativeLongDay = 30
)

// NegativeAccount is a user whose balance is below zero.
type NegativeAccount struct {
	Account       AccountRef
	Balance       money.Amount
	CreditLimit   money.Amount
	NegativeSince *time.Time
	LastEntryAt   *time.Time
}

// AccountOpening is a ledger account's balance at the start of a window.
type AccountOpening struct {
	ID         string
	Kind       string
	SystemCode string
	Balance    money.Amount
}

// TrendInputs are the raw sums the daily trend is accumulated from.
type TrendInputs struct {
	Accounts []AccountOpening
	// Nets maps ledger account ID -> day -> net change.
	Nets     map[string]map[string]money.Amount
	APIDaily map[string]struct{ Volume, Fee money.Amount }
	C2CDaily map[string]struct {
		Volume   money.Amount
		TotalFen int64
	}
}

// OverviewInputs are the call and C2C windows of the overview.
type OverviewInputs struct {
	Today, Last24h CallWindow
	Failing        []FailingChannel
	C2C            C2CCounts
	C2C24h         C2CWindow
}

// SnapshotRequest says which parts to read, all inside one consistent snapshot.
type SnapshotRequest struct {
	Now       time.Time
	TrendFrom time.Time
	TrendTo   time.Time
	Trend     bool
	Overview  bool
}

// Snapshot is read inside a single repeatable-read transaction so that sums,
// balances and checks compare the same state.
type Snapshot struct {
	Balances Balances
	Checks   Checks
	Negative []NegativeAccount
	Holders  []Holder
	Trend    TrendInputs
	Overview OverviewInputs
}

// Store is the persistence of the observation queries (observepg).
type Store interface {
	ListCalls(ctx context.Context, filter CallFilter, after *CallCursor, limit int) ([]CallRow, error)
	CallStats(ctx context.Context, filter CallFilter) (CallStats, error)
	GetCall(ctx context.Context, id string) (CallRecord, error)
	ChannelOwners(ctx context.Context, ids []string) (map[string]ChannelOwner, error)
	Usage(ctx context.Context, query UsageQuery) ([]UsageRow, error)
	ChannelBrief(ctx context.Context, id string) (ChannelBrief, error)
	ChannelStats(ctx context.Context, id string, from, to, now time.Time) (ChannelStatsData, error)
	UserPeriod(ctx context.Context, accountID string, from, to time.Time) (opening, closing money.Amount, flows []TypeFlow, err error)
	UserTrendInputs(ctx context.Context, accountID string, from, to time.Time) (opening money.Amount, days []DayFlow, err error)
	EntrySummary(ctx context.Context, filter EntryFilter, byDay, byKey bool) (EntrySummary, error)
	ExportEntries(ctx context.Context, filter EntryFilter, limit int) ([]ExportEntry, error)
	Snapshot(ctx context.Context, request SnapshotRequest) (Snapshot, error)
	ListTransactions(ctx context.Context, filter TxFilter, after *CallCursor, limit int) ([]Transaction, error)
	GetTransaction(ctx context.Context, id string) (Transaction, error)
	Repair(ctx context.Context, repair Repair) (RepairResult, error)
	ScrubErrors(ctx context.Context, before time.Time, batch int) (int64, error)
	LedgerTotals(ctx context.Context) ([]LedgerTotals, error)
	Channels(ctx context.Context) ([]ChannelState, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// SetClock replaces the clock; tests use it.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// ---- calls ----

var (
	formats  = []string{"openai_chat", "openai_responses", "anthropic", "gemini"}
	outcomes = []string{
		"rejected_balance", "rejected_budget", "rejected_key", "rejected_model", "rejected_format", "rejected_no_channel",
		"upstream_failed", "interrupted", "client_disconnected", "succeeded", "succeeded_unbilled", "in_progress",
	}
)

// ValidFormat and ValidOutcome let handlers reject bad filters with 400.
func ValidFormat(value string) bool  { return slices.Contains(formats, value) }
func ValidOutcome(value string) bool { return slices.Contains(outcomes, value) }

// CallPage is one page of calls with the cursor of the next and, optionally,
// the summary of everything the filter matches.
type CallPage struct {
	Items []CallRow
	Next  *CallCursor
	Stats *CallStats
}

// Calls lists calls newest first. limit is 1..100.
func (s *Service) Calls(ctx context.Context, filter CallFilter, after *CallCursor, limit int, withStats bool) (CallPage, error) {
	if limit < 1 || limit > 100 {
		return CallPage{}, ErrInvalidInput
	}
	rows, err := s.store.ListCalls(ctx, filter, after, limit+1)
	if err != nil {
		return CallPage{}, err
	}
	page := CallPage{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.Next = &CallCursor{At: last.CreatedAt, ID: last.ID}
	}
	page.Items = rows
	if withStats {
		stats, err := s.store.CallStats(ctx, filter)
		if err != nil {
			return CallPage{}, err
		}
		page.Stats = &stats
	}
	return page, nil
}

// Call returns one call as the viewer may see it, or ErrNotFound.
func (s *Service) Call(ctx context.Context, id string, viewer Viewer) (CallDetail, error) {
	record, err := s.store.GetCall(ctx, id)
	if err != nil {
		return CallDetail{}, err
	}
	if viewer.Admin || record.Account.ID == viewer.AccountID {
		return CallDetail{CallRecord: record}, nil
	}
	ids := map[string]bool{}
	if record.Channel != nil {
		ids[record.Channel.ID] = true
	}
	for _, attempt := range record.Attempts {
		if attempt.Channel != nil {
			ids[attempt.Channel.ID] = true
		}
	}
	list := make([]string, 0, len(ids))
	for channelID := range ids {
		list = append(list, channelID)
	}
	owners, err := s.store.ChannelOwners(ctx, list)
	if err != nil {
		return CallDetail{}, err
	}
	mine := func(channelID string) bool {
		owner, ok := owners[channelID]
		return ok && owner.OwnerID == viewer.AccountID
	}
	var attempts []Attempt
	for _, attempt := range record.Attempts {
		if attempt.Channel != nil && mine(attempt.Channel.ID) {
			attempts = append(attempts, attempt)
		}
	}
	servedByMe := record.Channel != nil && mine(record.Channel.ID)
	if len(attempts) == 0 && !servedByMe {
		return CallDetail{}, ErrNotFound
	}
	// A channel owner sees their own side of the call only: no consumer, key,
	// tag, user agent, routing, other channels or ledger transaction.
	restricted := record
	restricted.Account = AccountRef{}
	restricted.Key, restricted.Tag = nil, nil
	restricted.ClientUserAgent, restricted.RoutingMode, restricted.RoutingSource = nil, nil, nil
	restricted.LedgerTxID = nil
	restricted.Attempts = attempts
	restricted.AttemptCount = len(attempts)
	if !servedByMe {
		restricted.Channel = nil
	}
	return CallDetail{CallRecord: restricted, ChannelOwnerView: true}, nil
}

// ---- usage ----

// UsageReport is the aggregated usage of a window.
type UsageReport struct {
	Revenue  bool
	GroupBy  string
	From, To time.Time
	Items    []UsageRow
	Total    UsageRow
}

// Usage aggregates the account's calls (or, with Revenue, the calls served by
// its channels) over a window of Asia/Shanghai days; the default is the last
// 30 days.
func (s *Service) Usage(ctx context.Context, query UsageQuery) (UsageReport, error) {
	now := s.now()
	if query.GroupBy == "" {
		query.GroupBy = "day"
	}
	allowed := []string{"day", "model", "key", "tag"}
	if query.Revenue {
		allowed = []string{"day", "model", "channel"}
	}
	if !slices.Contains(allowed, query.GroupBy) || query.AccountID == "" {
		return UsageReport{}, ErrInvalidInput
	}
	if query.To.IsZero() {
		query.To = now
	}
	if query.From.IsZero() {
		query.From = localtime.DayStart(query.To).AddDate(0, 0, -29)
	}
	if !query.From.Before(query.To) || query.To.Sub(query.From) > maxUsageWindow {
		return UsageReport{}, ErrInvalidInput
	}
	rows, err := s.store.Usage(ctx, query)
	if err != nil {
		return UsageReport{}, err
	}
	if query.GroupBy == "day" {
		rows = fillDays(rows, query.From, query.To)
	}
	report := UsageReport{Revenue: query.Revenue, GroupBy: query.GroupBy, From: query.From, To: query.To, Items: rows}
	for _, row := range rows {
		report.Total.Calls += row.Calls
		report.Total.Succeeded += row.Succeeded
		report.Total.InputTokens += row.InputTokens
		report.Total.OutputTokens += row.OutputTokens
		report.Total.CacheWriteTokens += row.CacheWriteTokens
		report.Total.CacheReadTokens += row.CacheReadTokens
		report.Total.Amount += row.Amount
	}
	report.Total.Key, report.Total.Label = "total", "合计"
	return report, nil
}

func fillDays(rows []UsageRow, from, to time.Time) []UsageRow {
	byDay := make(map[string]UsageRow, len(rows))
	for _, row := range rows {
		byDay[row.Key] = row
	}
	result := make([]UsageRow, 0, len(rows))
	for day := localtime.DayStart(from); day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.In(localtime.Zone).Format("2006-01-02")
		row, ok := byDay[key]
		if !ok {
			row = UsageRow{Key: key, Label: key}
		}
		result = append(result, row)
	}
	return result
}

// ---- channel statistics ----

// Window is calls, successes and the derived rate of one time window.
type Window struct {
	Calls, Succeeded int64
	SuccessRate      *float64
}

func windowOf(w AttemptWindow) Window {
	return Window{Calls: w.Attempts, Succeeded: w.Successes, SuccessRate: rate(w.Successes, w.Attempts)}
}

// ChannelReport is the statistics page of one channel.
type ChannelReport struct {
	From, To  time.Time
	Window    Window
	Last24h   Window
	Last7d    Window
	TTFTP50MS *int
	TTFTP95MS *int
	SpeedP50  *float64
	SpeedP95  *float64
	Revenue   money.Amount
	Hourly    []Bucket
	Daily     []DayBucket
	Models    []ModelBucket
	Statuses  []StatusBucket
	Failures  []Failure
	Events    []ChannelEvent
	// Today's revenue and the daily cap.
	TodayRevenue money.Amount
	DailyCap     *money.Amount
	Progress     *float64
}

// ChannelStats reports a channel to its owner or an administrator. The window
// defaults to the last 7 days.
func (s *Service) ChannelStats(ctx context.Context, viewer Viewer, channelID string, from, to *time.Time) (ChannelReport, error) {
	brief, err := s.store.ChannelBrief(ctx, channelID)
	if err != nil {
		return ChannelReport{}, err
	}
	if !viewer.Admin && brief.OwnerID != viewer.AccountID {
		return ChannelReport{}, ErrNotFound
	}
	now := s.now()
	end := now
	if to != nil {
		end = *to
	}
	start := end.Add(-7 * 24 * time.Hour)
	if from != nil {
		start = *from
	}
	if !start.Before(end) || end.Sub(start) > maxStatsWindow {
		return ChannelReport{}, ErrInvalidInput
	}
	data, err := s.store.ChannelStats(ctx, channelID, start, end, now)
	if err != nil {
		return ChannelReport{}, err
	}
	report := ChannelReport{
		From: start, To: end, Window: windowOf(data.Window), Last24h: windowOf(data.Last24h), Last7d: windowOf(data.Last7d),
		TTFTP50MS: data.Window.TTFTP50MS, TTFTP95MS: data.Window.TTFTP95MS, SpeedP50: data.SpeedP50, SpeedP95: data.SpeedP95,
		Revenue: data.Revenue, Models: data.Models, Statuses: data.StatusCodes, Failures: data.Failures, Events: data.Events,
		TodayRevenue: data.TodayRevenue, DailyCap: brief.DailyRevenueCap,
	}
	report.Hourly = fillHours(data.Hourly, now)
	report.Daily = fillBuckets(data.Daily, start, end)
	if brief.DailyRevenueCap != nil && *brief.DailyRevenueCap > 0 {
		progress := float64(data.TodayRevenue.Nano()) / float64(brief.DailyRevenueCap.Nano())
		if progress > 1 {
			progress = 1
		}
		if progress < 0 {
			progress = 0
		}
		report.Progress = &progress
	}
	return report, nil
}

func fillHours(buckets []Bucket, now time.Time) []Bucket {
	byHour := make(map[int64]Bucket, len(buckets))
	for _, bucket := range buckets {
		byHour[bucket.At.Truncate(time.Hour).Unix()] = bucket
	}
	end := now.Truncate(time.Hour)
	result := make([]Bucket, 0, 24)
	for hour := end.Add(-23 * time.Hour); !hour.After(end); hour = hour.Add(time.Hour) {
		bucket := byHour[hour.Unix()]
		bucket.At = hour
		result = append(result, bucket)
	}
	return result
}

func fillBuckets(buckets []DayBucket, from, to time.Time) []DayBucket {
	byDay := make(map[string]DayBucket, len(buckets))
	for _, bucket := range buckets {
		byDay[bucket.Day] = bucket
	}
	result := make([]DayBucket, 0, len(buckets))
	for day := localtime.DayStart(from); day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.In(localtime.Zone).Format("2006-01-02")
		bucket, ok := byDay[key]
		bucket.Day = key
		_ = ok
		result = append(result, bucket)
	}
	return result
}

// ---- user points ----

// PointsReport adds the 30-day trend and the period reconciliation to a
// user's points.
type PointsReport struct {
	Trend  []TrendPoint
	Period Period
}

// Points builds the trend (the last 30 Asia/Shanghai days) and the
// reconciliation of [from, to), by default this month so far.
func (s *Service) Points(ctx context.Context, accountID string, from, to *time.Time) (PointsReport, error) {
	now := s.now()
	start, end := localtime.MonthStart(now), now
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	if accountID == "" || !start.Before(end) {
		return PointsReport{}, ErrInvalidInput
	}
	trendFrom := localtime.DayStart(now).AddDate(0, 0, -29)
	opening, days, err := s.store.UserTrendInputs(ctx, accountID, trendFrom, now.Add(time.Second))
	if err != nil {
		return PointsReport{}, err
	}
	byDay := make(map[string]DayFlow, len(days))
	for _, day := range days {
		byDay[day.Day] = day
	}
	trend := make([]TrendPoint, 0, 30)
	balance := opening
	for day := trendFrom; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.In(localtime.Zone).Format("2006-01-02")
		flow := byDay[key]
		balance += flow.Net
		trend = append(trend, TrendPoint{Day: key, Balance: balance, Income: flow.Income, Spend: flow.Spend})
	}
	periodOpening, periodClosing, flows, err := s.store.UserPeriod(ctx, accountID, start, end)
	if err != nil {
		return PointsReport{}, err
	}
	return PointsReport{Trend: trend, Period: reconcile(start, end, periodOpening, periodClosing, flows)}, nil
}

// reconcile sorts a user's entry sums into the categories of the period
// statement. Anything uncategorized would show up as a non-zero difference.
func reconcile(from, to time.Time, opening, closing money.Amount, flows []TypeFlow) Period {
	period := Period{From: from, To: to, Opening: opening, Closing: closing}
	var categorized int64
	for _, flow := range flows {
		amount := flow.Amount
		if flow.Inflow {
			period.Income += amount
		} else {
			period.Spend += -amount
		}
		switch {
		case flow.Type == "api_call" && !flow.Inflow:
			period.CallSpend += amount
		case flow.Type == "api_call":
			period.ChannelIncome += amount
		case flow.Type == "c2c_list":
			period.C2CSell += amount
		case flow.Type == "c2c_release":
			period.C2CBuy += amount
		case flow.Type == "c2c_return":
			period.C2CReturn += amount
		case flow.Type == "admin_adjust":
			period.Adjustments += amount
		case flow.Type == "bad_debt_writeoff":
			period.WriteOffs += amount
		default:
			continue
		}
		categorized += amount.Nano()
	}
	period.Difference = money.FromNano(closing.Nano() - opening.Nano() - categorized)
	return period
}

// EntriesSummary groups the matching entries by day and/or key.
func (s *Service) EntriesSummary(ctx context.Context, filter EntryFilter, group string) (EntrySummary, error) {
	switch group {
	case "day":
		return s.store.EntrySummary(ctx, filter, true, false)
	case "key":
		return s.store.EntrySummary(ctx, filter, false, true)
	}
	return EntrySummary{}, ErrInvalidInput
}

// ExportEntries returns up to MaxExportRows entries, newest first.
func (s *Service) ExportEntries(ctx context.Context, filter EntryFilter) ([]ExportEntry, error) {
	if filter.AccountID == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ExportEntries(ctx, filter, MaxExportRows)
}

// ---- administrator ----

// AdminPoints builds the global points report for a window of 7, 30 or 90 days.
func (s *Service) AdminPoints(ctx context.Context, days int) (AdminPointsReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return AdminPointsReport{}, ErrInvalidInput
	}
	now := s.now()
	first := localtime.DayStart(now).AddDate(0, 0, -(days - 1))
	snapshot, err := s.store.Snapshot(ctx, SnapshotRequest{Now: now, Trend: true, TrendFrom: first, TrendTo: now.Add(time.Second)})
	if err != nil {
		return AdminPointsReport{}, err
	}
	risks, concentration := risksOf(snapshot, now)
	return AdminPointsReport{
		Balances: snapshot.Balances, Checks: snapshot.Checks, Trend: buildTrend(snapshot.Trend, first, now),
		Risks: risks, Concentration: concentration,
	}, nil
}

func buildTrend(inputs TrendInputs, first, now time.Time) []DayStat {
	balances := make(map[string]money.Amount, len(inputs.Accounts))
	for _, account := range inputs.Accounts {
		balances[account.ID] = account.Balance
	}
	stats := make([]DayStat, 0)
	for day := first; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.In(localtime.Zone).Format("2006-01-02")
		var stat DayStat
		stat.Day = key
		for _, account := range inputs.Accounts {
			balance := balances[account.ID] + inputs.Nets[account.ID][key]
			balances[account.ID] = balance
			switch {
			case account.Kind == "user" && balance > 0:
				stat.Circulation += balance
			case account.Kind == "user" && balance < 0:
				stat.CreditIssued += -balance
			case account.SystemCode == "c2c_escrow":
				stat.Escrow = balance
			case account.SystemCode == "platform_revenue":
				stat.PlatformRevenue = balance
			case account.SystemCode == "bad_debt":
				stat.BadDebt = balance
			}
		}
		stat.APIVolume, stat.APIFee = inputs.APIDaily[key].Volume, inputs.APIDaily[key].Fee
		c2c := inputs.C2CDaily[key]
		stat.C2CVolume = c2c.Volume
		if c2c.Volume > 0 {
			price := float64(c2c.TotalFen) / (float64(c2c.Volume.Nano()) / 1e9)
			stat.C2CAvgPriceFen = &price
		}
		stats = append(stats, stat)
	}
	return stats
}

// risksOf derives the risk table and the holding concentration.
func risksOf(snapshot Snapshot, now time.Time) ([]Risk, Concentration) {
	var risks []Risk
	for _, account := range snapshot.Negative {
		since := account.NegativeSince
		if since == nil {
			since = account.LastEntryAt
		}
		var negativeDays *int
		if since != nil {
			days := int(now.Sub(*since).Hours() / 24)
			negativeDays = &days
		}
		base := Risk{
			Account: account.Account, Balance: account.Balance, CreditLimit: account.CreditLimit,
			NegativeDays: negativeDays, LastActivityAt: account.LastEntryAt,
		}
		if account.Balance.Nano()+account.CreditLimit.Nano() < 0 {
			risk := base
			risk.Kind = "over_limit"
			risks = append(risks, risk)
		}
		if negativeDays != nil && *negativeDays >= negativeLongDay {
			risk := base
			risk.Kind = "negative_long"
			risks = append(risks, risk)
		}
	}
	circulation := snapshot.Balances.UserPositive.Nano()
	concentration := Concentration{Top: []Holder{}}
	var topSum int64
	for _, holder := range snapshot.Holders {
		if circulation > 0 {
			holder.Share = float64(holder.Balance.Nano()) / float64(circulation)
		}
		topSum += holder.Balance.Nano()
		concentration.Top = append(concentration.Top, holder)
	}
	if circulation > 0 {
		share := float64(topSum) / float64(circulation)
		concentration.Top5Share = &share
	}
	return risks, concentration
}

// Overview is the landing page of the administrator: what needs attention
// plus the headline numbers.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	snapshot, err := s.store.Snapshot(ctx, SnapshotRequest{Now: now, Overview: true})
	if err != nil {
		return Overview{}, err
	}
	risks, concentration := risksOf(snapshot, now)
	data := OverviewData{
		Today: snapshot.Overview.Today, Last24h: snapshot.Overview.Last24h, Failing: snapshot.Overview.Failing,
		C2C: snapshot.Overview.C2C, C2C24h: snapshot.Overview.C2C24h, Balances: snapshot.Balances, Checks: snapshot.Checks,
		Risks: risks, Concentration: concentration,
	}
	return Overview{Attention: attention(data), OverviewData: data}, nil
}

// attention applies the rules of the administrator's "needs attention" list.
func attention(data OverviewData) []AttentionItem {
	var items []AttentionItem
	if data.C2C.OpenDisputes > 0 {
		items = append(items, AttentionItem{AttentionDispute, "warning", fmt.Sprintf("%d 笔 C2C 申诉待仲裁", data.C2C.OpenDisputes), int(data.C2C.OpenDisputes), "/admin/disputes"})
	}
	var over, long int
	for _, risk := range data.Risks {
		switch risk.Kind {
		case "over_limit":
			over++
		case "negative_long":
			long++
		}
	}
	if over > 0 {
		items = append(items, AttentionItem{AttentionOverLimit, "critical", fmt.Sprintf("%d 个账户超出信用额度", over), over, "/admin/users"})
	}
	if long > 0 {
		items = append(items, AttentionItem{AttentionNegative, "warning", fmt.Sprintf("%d 个账户负余额超过 %d 天", long, negativeLongDay), long, "/admin/users"})
	}
	if len(data.Concentration.Top) > 0 && data.Balances.UserPositive > 0 && data.Concentration.Top[0].Share > 0.5 {
		top := data.Concentration.Top[0]
		items = append(items, AttentionItem{AttentionConcentration, "warning",
			fmt.Sprintf("%s 持有 %.0f%% 的流通积分", top.Account.DisplayName, top.Share*100), 1, "/admin/points"})
	}
	for _, channel := range data.Failing {
		percent := 100 * float64(channel.Successes) / float64(channel.Attempts)
		items = append(items, AttentionItem{AttentionChannelFail, "warning",
			fmt.Sprintf("渠道「%s」1 小时成功率 %.0f%%（%d 次尝试）", channel.Name, percent, channel.Attempts), 1, "/admin/channels/" + channel.ID})
	}
	if unbilledAbnormal(data.Last24h) {
		items = append(items, AttentionItem{AttentionUnbilled, "warning",
			fmt.Sprintf("24 小时内 %d 次成功调用读不到用量，未计费", data.Last24h.Unbilled), int(data.Last24h.Unbilled), "/admin/calls?outcome=succeeded_unbilled"})
	}
	if !data.Checks.ZeroSumOK {
		items = append(items, AttentionItem{AttentionUnbalanced, "critical", "账户余额合计不为 0", 1, "/admin/points"})
	}
	var failed []string
	for _, name := range data.Checks.FailedChecks() {
		if name != "zero_sum" {
			failed = append(failed, checkLabels[name])
		}
	}
	if len(failed) > 0 {
		items = append(items, AttentionItem{AttentionReconcile, "critical", "记账失败或核对不通过：" + strings.Join(failed, "、"), len(failed), "/admin/points"})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Severity == "critical" && items[j].Severity != "critical" })
	return items
}

// checkLabels names the reconciliation checks for administrators; the keys
// stay the Prometheus label values.
var checkLabels = map[string]string{
	"account_balances": "账户余额与分录不一致",
	"escrow":           "C2C 托管余额不符",
	"billing_calls":    "成功调用漏记账",
	"released_trades":  "已放行交易漏记账",
}

// unbilledAbnormal flags too many successful calls whose usage was not read:
// more than 20 in 24 hours, or more than 5% once there are at least 20 successes.
func unbilledAbnormal(window CallWindow) bool {
	if window.Unbilled > 20 {
		return true
	}
	return window.Succeeded >= 20 && window.Unbilled*20 > window.Succeeded
}

// Transactions browses the ledger transactions, newest first.
func (s *Service) Transactions(ctx context.Context, filter TxFilter, after *CallCursor, limit int) ([]Transaction, *CallCursor, error) {
	if limit < 1 || limit > 100 || (filter.RelatedType != "") != (filter.RelatedID != "") {
		return nil, nil, ErrInvalidInput
	}
	items, err := s.store.ListTransactions(ctx, filter, after, limit+1)
	if err != nil {
		return nil, nil, err
	}
	var next *CallCursor
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = &CallCursor{At: last.CreatedAt, ID: last.ID}
	}
	return items, next, nil
}

// Transaction returns one transaction with its entries and price snapshot.
func (s *Service) Transaction(ctx context.Context, id string) (Transaction, error) {
	return s.store.GetTransaction(ctx, id)
}

// UserIDs lists the user accounts a transaction touches.
func (t Transaction) UserIDs() []string {
	var ids []string
	for _, entry := range t.Entries {
		if entry.Account.Account != nil && !slices.Contains(ids, entry.Account.Account.ID) {
			ids = append(ids, entry.Account.Account.ID)
		}
	}
	return ids
}

// RepairCall books or voids a call whose ledger posting failed.
func (s *Service) RepairCall(ctx context.Context, repair Repair) (RepairResult, error) {
	repair.Reason = strings.TrimSpace(repair.Reason)
	if repair.ActorID == "" || repair.CallID == "" || repair.Reason == "" || len([]rune(repair.Reason)) > 500 ||
		(repair.Action != RepairCharge && repair.Action != RepairVoid) {
		return RepairResult{}, ErrInvalidInput
	}
	return s.store.Repair(ctx, repair)
}

// ScrubRawErrors clears the raw upstream error text of attempts older than
// ErrorRetention and returns how many calls it changed.
func (s *Service) ScrubRawErrors(ctx context.Context) (int64, error) {
	const batch = 1000
	cutoff := s.now().Add(-ErrorRetention)
	var total int64
	for {
		changed, err := s.store.ScrubErrors(ctx, cutoff, batch)
		total += changed
		if err != nil || changed < batch {
			return total, err
		}
	}
}

// ---- for metrics ----

// Checks runs the five reconciliation checks now.
func (s *Service) Checks(ctx context.Context) (Checks, Balances, error) {
	snapshot, err := s.store.Snapshot(ctx, SnapshotRequest{Now: s.now()})
	return snapshot.Checks, snapshot.Balances, err
}

func (s *Service) LedgerTotals(ctx context.Context) ([]LedgerTotals, error) {
	return s.store.LedgerTotals(ctx)
}

func (s *Service) Channels(ctx context.Context) ([]ChannelState, error) { return s.store.Channels(ctx) }

// Record reads one stored call, unrestricted, for the live feed and metrics.
func (s *Service) Record(ctx context.Context, id string) (CallRecord, error) {
	return s.store.GetCall(ctx, id)
}

// ChannelAccess returns the channel when the viewer owns it or is an
// administrator; anyone else gets ErrNotFound.
func (s *Service) ChannelAccess(ctx context.Context, viewer Viewer, channelID string) (ChannelBrief, error) {
	brief, err := s.store.ChannelBrief(ctx, channelID)
	if err != nil {
		return ChannelBrief{}, err
	}
	if !viewer.Admin && brief.OwnerID != viewer.AccountID {
		return ChannelBrief{}, ErrNotFound
	}
	return brief, nil
}
