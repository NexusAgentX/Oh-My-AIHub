package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// ---- filters and cursors ----

func encodeCallCursor(cursor *observe.CallCursor) *string {
	if cursor == nil {
		return nil
	}
	encoded := encodeTextCursor(strconv.FormatInt(cursor.At.UnixNano(), 10) + "|" + cursor.ID)
	return &encoded
}

func decodeCallCursor(r *http.Request) (*observe.CallCursor, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return nil, true
	}
	text, ok := decodeTextCursor(raw)
	if !ok {
		return nil, false
	}
	nanos, id, found := strings.Cut(text, "|")
	value, err := strconv.ParseInt(nanos, 10, 64)
	if !found || err != nil || !uuidPattern.MatchString(id) {
		return nil, false
	}
	return &observe.CallCursor{At: time.Unix(0, value).UTC(), ID: id}, true
}

func optionalInt(query map[string][]string, name string) (*int, bool) {
	values := query[name]
	if len(values) == 0 || values[0] == "" {
		return nil, true
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < 0 {
		return nil, false
	}
	return &value, true
}

func optionalAmount(query map[string][]string, name string) (*money.Amount, bool) {
	values := query[name]
	if len(values) == 0 || values[0] == "" {
		return nil, true
	}
	value, err := money.Parse(values[0])
	if err != nil {
		return nil, false
	}
	return &value, true
}

// parseCallFilter reads the call filters shared by the three list endpoints
// and the three live streams. Parameters a route does not support are ignored
// by the caller overwriting the fields it owns (account, channel scope).
func parseCallFilter(r *http.Request) (observe.CallFilter, bool) {
	query := r.URL.Query()
	filter := observe.CallFilter{
		APIKeyID: query.Get("api_key_id"), Model: query.Get("model"), Format: query.Get("format"), Outcome: query.Get("outcome"),
		Tag: query.Get("tag"), RequestID: query.Get("request_id"), AccountID: query.Get("account_id"), FinalChannelID: query.Get("channel_id"),
	}
	ok := true
	for _, id := range []string{filter.APIKeyID, filter.RequestID, filter.AccountID, filter.FinalChannelID} {
		if id != "" && !uuidPattern.MatchString(id) {
			ok = false
		}
	}
	if (filter.Format != "" && !observe.ValidFormat(filter.Format)) || (filter.Outcome != "" && !observe.ValidOutcome(filter.Outcome)) {
		ok = false
	}
	var fromOK, toOK, durationOK, tokensOK, costOK bool
	filter.From, fromOK = parseTimeParam(r, "from")
	filter.To, toOK = parseTimeParam(r, "to")
	var minDurationOK, maxDurationOK, minTokensOK, maxTokensOK, minCostOK, maxCostOK bool
	filter.MinDurationMS, minDurationOK = optionalInt(query, "min_duration_ms")
	filter.MaxDurationMS, maxDurationOK = optionalInt(query, "max_duration_ms")
	durationOK = minDurationOK && maxDurationOK
	var minTokens, maxTokens *int
	minTokens, minTokensOK = optionalInt(query, "min_tokens")
	maxTokens, maxTokensOK = optionalInt(query, "max_tokens")
	tokensOK = minTokensOK && maxTokensOK
	if minTokens != nil {
		value := int64(*minTokens)
		filter.MinTokens = &value
	}
	if maxTokens != nil {
		value := int64(*maxTokens)
		filter.MaxTokens = &value
	}
	filter.MinCharged, minCostOK = optionalAmount(query, "min_cost")
	filter.MaxCharged, maxCostOK = optionalAmount(query, "max_cost")
	costOK = minCostOK && maxCostOK
	return filter, ok && fromOK && toOK && durationOK && tokensOK && costOK
}

func writeInvalidQuery(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
}

func writeObserveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, observe.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, observe.ErrNotRepairable):
		writeError(w, http.StatusConflict, "not_repairable", "该调用已记账或不属于可补记的范围")
	case errors.Is(err, observe.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
	default:
		writeDomainError(w, err)
	}
}

func viewerOf(r *http.Request) observe.Viewer {
	account := accountFromContext(r.Context())
	return observe.Viewer{AccountID: account.ID, Admin: account.IsAdmin}
}

// ---- JSON ----

// usageJSON is the OpenAPI Usage schema.
type usageJSON struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
}

// channelRefJSON is the OpenAPI ChannelRef schema.
type channelRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// accountRefJSON is the OpenAPI AccountRef schema: an account as an administrator sees it.
type accountRefJSON struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// callSummaryJSON is the OpenAPI CallSummary schema.
type callSummaryJSON struct {
	ID             string          `json:"id"`
	CreatedAt      time.Time       `json:"created_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	ModelID        *string         `json:"model_id"`
	RequestedModel string          `json:"requested_model"`
	Format         string          `json:"format"`
	Stream         bool            `json:"stream"`
	Tag            *string         `json:"tag"`
	APIKey         *keyRefJSON     `json:"api_key"`
	Outcome        string          `json:"outcome"`
	Channel        *channelRefJSON `json:"channel"`
	AttemptCount   int             `json:"attempt_count"`
	Usage          usageJSON       `json:"usage"`
	Cost           string          `json:"cost"`
	Fee            string          `json:"fee"`
	Charged        string          `json:"charged"`
	TTFTMS         *int            `json:"ttft_ms"`
	DurationMS     *int            `json:"duration_ms"`
}

// adminCallJSON is the OpenAPI AdminCall schema: a CallSummary plus the calling account.
type adminCallJSON struct {
	callSummaryJSON
	Account accountRefJSON `json:"account"`
}

// callAttemptJSON is the OpenAPI CallAttempt schema.
type callAttemptJSON struct {
	Channel       *channelRefJSON `json:"channel"`
	StatusCode    *int            `json:"status_code"`
	ErrorCode     *string         `json:"error_code"`
	ErrorMessage  *string         `json:"error_message"`
	ConnectMS     *int            `json:"connect_ms"`
	TTFTMS        *int            `json:"ttft_ms"`
	DurationMS    *int            `json:"duration_ms"`
	ResponseBytes *int64          `json:"response_bytes"`
	EndReason     string          `json:"end_reason"`
}

// callDetailJSON is the OpenAPI CallDetail schema: a CallSummary plus routing, attempts and speed.
// price_snapshot is the jsonb the gateway stored with the call, passed through unchanged (null when absent).
type callDetailJSON struct {
	callSummaryJSON
	RoutingMode           *string           `json:"routing_mode"`
	RoutingSource         *string           `json:"routing_source"`
	ClientUserAgent       *string           `json:"client_user_agent"`
	Attempts              []callAttemptJSON `json:"attempts"`
	PriceSnapshot         json.RawMessage   `json:"price_snapshot"`
	OutputTokensPerSecond *float64          `json:"output_tokens_per_second"`
	InterTokenP50MS       *int              `json:"inter_token_p50_ms"`
	InterTokenP95MS       *int              `json:"inter_token_p95_ms"`
	ResponseBytes         *int64            `json:"response_bytes"`
	LedgerTransactionID   *string           `json:"ledger_transaction_id"`
}

// callDetailEnvelopeJSON is the OpenAPI CallDetailEnvelope schema.
type callDetailEnvelopeJSON struct {
	Call callDetailJSON `json:"call"`
}

// callStatsJSON is the OpenAPI CallStats schema.
type callStatsJSON struct {
	Calls        int64   `json:"calls"`
	Succeeded    int64   `json:"succeeded"`
	Failed       int64   `json:"failed"`
	SuccessRate  *string `json:"success_rate"`
	Charged      string  `json:"charged"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	TTFTP50MS    *int    `json:"ttft_p50_ms"`
	TTFTP95MS    *int    `json:"ttft_p95_ms"`
}

// callPageJSON is the OpenAPI CallPage schema.
type callPageJSON struct {
	Items      []callSummaryJSON `json:"items"`
	NextCursor *string           `json:"next_cursor"`
	Summary    callStatsJSON     `json:"summary"`
}

// adminCallPageJSON is the OpenAPI AdminCallPage schema.
type adminCallPageJSON struct {
	Items      []adminCallJSON `json:"items"`
	NextCursor *string         `json:"next_cursor"`
	Summary    callStatsJSON   `json:"summary"`
}

// channelCallErrorJSON is the error object of the OpenAPI ChannelCall schema.
type channelCallErrorJSON struct {
	StatusCode   *int    `json:"status_code"`
	ErrorCode    *string `json:"error_code"`
	ErrorMessage *string `json:"error_message"`
}

// channelCallJSON is the OpenAPI ChannelCall schema: a call as the channel's owner sees it.
type channelCallJSON struct {
	ID         string                `json:"id"`
	CreatedAt  time.Time             `json:"created_at"`
	ModelID    *string               `json:"model_id"`
	Format     string                `json:"format"`
	Stream     bool                  `json:"stream"`
	Outcome    string                `json:"outcome"`
	Usage      usageJSON             `json:"usage"`
	Revenue    string                `json:"revenue"`
	Served     bool                  `json:"served"`
	TTFTMS     *int                  `json:"ttft_ms"`
	DurationMS *int                  `json:"duration_ms"`
	Error      *channelCallErrorJSON `json:"error"`
}

// usageRowJSON is the OpenAPI UsageRow schema.
type usageRowJSON struct {
	Key              string `json:"key"`
	Label            string `json:"label"`
	Calls            int64  `json:"calls"`
	Succeeded        int64  `json:"succeeded"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	Charged          string `json:"charged"`
}

// usageReportJSON is the OpenAPI UsageReport schema.
type usageReportJSON struct {
	View    string         `json:"view"`
	GroupBy string         `json:"group_by"`
	From    time.Time      `json:"from"`
	To      time.Time      `json:"to"`
	Items   []usageRowJSON `json:"items"`
	Total   usageRowJSON   `json:"total"`
}

// channelWindowJSON is the OpenAPI ChannelWindow schema.
type channelWindowJSON struct {
	Calls       int64   `json:"calls"`
	Succeeded   int64   `json:"succeeded"`
	SuccessRate *string `json:"success_rate"`
}

// channelFailureJSON is the OpenAPI ChannelFailure schema.
type channelFailureJSON struct {
	CallID       string    `json:"call_id"`
	CreatedAt    time.Time `json:"created_at"`
	ModelID      *string   `json:"model_id"`
	StatusCode   *int      `json:"status_code"`
	ErrorCode    *string   `json:"error_code"`
	ErrorMessage *string   `json:"error_message"`
	EndReason    string    `json:"end_reason"`
}

// channelStatsHourJSON is one hourly element of the OpenAPI ChannelStats schema.
type channelStatsHourJSON struct {
	Hour      time.Time `json:"hour"`
	Calls     int64     `json:"calls"`
	Succeeded int64     `json:"succeeded"`
}

// channelStatsStatusJSON is one status_codes element of the OpenAPI ChannelStats schema.
type channelStatsStatusJSON struct {
	StatusCode *int  `json:"status_code"`
	Count      int64 `json:"count"`
}

// channelStatsTodayJSON is the today object of the OpenAPI ChannelStats schema.
type channelStatsTodayJSON struct {
	Revenue  string  `json:"revenue"`
	DailyCap *string `json:"daily_cap"`
	Progress *string `json:"progress"`
}

// channelStatsModelJSON is one by_model element of the OpenAPI ChannelStats schema.
type channelStatsModelJSON struct {
	ModelID   string `json:"model_id"`
	Calls     int64  `json:"calls"`
	Succeeded int64  `json:"succeeded"`
	Revenue   string `json:"revenue"`
}

// channelStatsDayJSON is one daily element of the OpenAPI ChannelStats schema.
type channelStatsDayJSON struct {
	Date      string `json:"date"`
	Calls     int64  `json:"calls"`
	Succeeded int64  `json:"succeeded"`
	Revenue   string `json:"revenue"`
}

// channelStatsJSON is the OpenAPI ChannelStats schema.
type channelStatsJSON struct {
	From                     time.Time                `json:"from"`
	To                       time.Time                `json:"to"`
	Calls                    int64                    `json:"calls"`
	Succeeded                int64                    `json:"succeeded"`
	SuccessRate              *string                  `json:"success_rate"`
	Revenue                  string                   `json:"revenue"`
	TTFTP50MS                *int                     `json:"ttft_p50_ms"`
	TTFTP95MS                *int                     `json:"ttft_p95_ms"`
	OutputTokensPerSecondP50 *float64                 `json:"output_tokens_per_second_p50"`
	OutputTokensPerSecondP95 *float64                 `json:"output_tokens_per_second_p95"`
	Last24h                  channelWindowJSON        `json:"last_24h"`
	Last7d                   channelWindowJSON        `json:"last_7d"`
	Hourly                   []channelStatsHourJSON   `json:"hourly"`
	StatusCodes              []channelStatsStatusJSON `json:"status_codes"`
	RecentFailures           []channelFailureJSON     `json:"recent_failures"`
	Today                    channelStatsTodayJSON    `json:"today"`
	ByModel                  []channelStatsModelJSON  `json:"by_model"`
	Daily                    []channelStatsDayJSON    `json:"daily"`
	Events                   []channelEventJSON       `json:"events"`
}

func newUsageJSON(usage observe.Usage) usageJSON {
	return usageJSON{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CacheWriteTokens: usage.CacheWriteTokens, CacheReadTokens: usage.CacheReadTokens,
	}
}

func newChannelRefJSON(ref *observe.ChannelRef) *channelRefJSON {
	if ref == nil {
		return nil
	}
	return &channelRefJSON{ID: ref.ID, Name: ref.Name}
}

func newObservedKeyRefJSON(ref *observe.KeyRef) *keyRefJSON {
	if ref == nil {
		return nil
	}
	return &keyRefJSON{ID: ref.ID, Name: ref.Name}
}

func newAccountRefJSON(ref observe.AccountRef) accountRefJSON {
	return accountRefJSON{ID: ref.ID, Username: ref.Username, DisplayName: ref.DisplayName}
}

// optionalRatio renders an optional ratio as a decimal string, or null when unset.
func optionalRatio(value *float64) *string {
	if value == nil {
		return nil
	}
	text := ratio(*value)
	return &text
}

func newCallSummaryJSON(row observe.CallRow) callSummaryJSON {
	return callSummaryJSON{
		ID:             row.ID,
		CreatedAt:      row.CreatedAt,
		CompletedAt:    row.CompletedAt,
		ModelID:        row.ModelID,
		RequestedModel: row.RequestedModel,
		Format:         row.Format,
		Stream:         row.Stream,
		Tag:            row.Tag,
		APIKey:         newObservedKeyRefJSON(row.Key),
		Outcome:        row.Outcome,
		Channel:        newChannelRefJSON(row.Channel),
		AttemptCount:   row.AttemptCount,
		Usage:          newUsageJSON(row.Usage),
		Cost:           row.Cost.String(),
		Fee:            row.Fee.String(),
		Charged:        row.Charged().String(),
		TTFTMS:         row.TTFTMS,
		DurationMS:     row.DurationMS,
	}
}

func newAdminCallJSON(row observe.CallRow) adminCallJSON {
	return adminCallJSON{callSummaryJSON: newCallSummaryJSON(row), Account: newAccountRefJSON(row.Account)}
}

// lastFailure is the most recent failed attempt among a call's scope attempts.
func lastFailure(attempts []observe.Attempt) *channelCallErrorJSON {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if !attempt.Succeeded() {
			return &channelCallErrorJSON{StatusCode: attempt.StatusCode, ErrorCode: attempt.ErrorCode, ErrorMessage: attempt.ErrorMessage}
		}
	}
	return nil
}

func newChannelCallJSON(row observe.CallRow, channelID string) channelCallJSON {
	served := row.Channel != nil && row.Channel.ID == channelID
	revenue := money.Amount(0)
	if served {
		revenue = row.Revenue()
	}
	return channelCallJSON{
		ID:         row.ID,
		CreatedAt:  row.CreatedAt,
		ModelID:    row.ModelID,
		Format:     row.Format,
		Stream:     row.Stream,
		Outcome:    row.Outcome,
		Usage:      newUsageJSON(row.Usage),
		Revenue:    revenue.String(),
		Served:     served,
		TTFTMS:     row.TTFTMS,
		DurationMS: row.DurationMS,
		Error:      lastFailure(row.ScopeAttempts),
	}
}

func newCallStatsJSON(stats observe.CallStats) callStatsJSON {
	return callStatsJSON{
		Calls:        stats.Calls,
		Succeeded:    stats.Succeeded,
		Failed:       stats.Failed,
		SuccessRate:  optionalRatio(stats.SuccessRate()),
		Charged:      stats.Charged.String(),
		InputTokens:  stats.InputTokens,
		OutputTokens: stats.OutputTokens,
		TotalTokens:  stats.TotalTokens(),
		TTFTP50MS:    stats.TTFTP50MS,
		TTFTP95MS:    stats.TTFTP95MS,
	}
}

func newCallAttemptJSON(attempt observe.Attempt) callAttemptJSON {
	return callAttemptJSON{
		Channel:       newChannelRefJSON(attempt.Channel),
		StatusCode:    attempt.StatusCode,
		ErrorCode:     attempt.ErrorCode,
		ErrorMessage:  attempt.ErrorMessage,
		ConnectMS:     attempt.ConnectMS,
		TTFTMS:        attempt.TTFTMS,
		DurationMS:    attempt.DurationMS,
		ResponseBytes: attempt.ResponseByte,
		EndReason:     attempt.EndReason,
	}
}

// rawJSONOrNil keeps stored jsonb as is and turns an absent or null value into a nil message, which encodes as null.
func rawJSONOrNil(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func newCallDetailJSON(detail observe.CallDetail) callDetailJSON {
	attempts := make([]callAttemptJSON, 0, len(detail.Attempts))
	for _, attempt := range detail.Attempts {
		attempts = append(attempts, newCallAttemptJSON(attempt))
	}
	speed := detail.TokensPerSecond
	if !detail.Stream {
		// Non-streaming speed is the end-to-end average, including all attempts.
		speed = nil
		if detail.DurationMS != nil && *detail.DurationMS > 0 && detail.Usage.OutputTokens > 0 {
			value := float64(detail.Usage.OutputTokens) * 1000 / float64(*detail.DurationMS)
			speed = &value
		}
	}
	return callDetailJSON{
		callSummaryJSON:       newCallSummaryJSON(detail.CallRow),
		RoutingMode:           detail.RoutingMode,
		RoutingSource:         detail.RoutingSource,
		ClientUserAgent:       detail.ClientUserAgent,
		Attempts:              attempts,
		PriceSnapshot:         rawJSONOrNil(detail.PriceSnapshot),
		OutputTokensPerSecond: speed,
		InterTokenP50MS:       detail.IntervalP50MS,
		InterTokenP95MS:       detail.IntervalP95MS,
		ResponseBytes:         detail.ResponseBytes,
		LedgerTransactionID:   detail.LedgerTxID,
	}
}

func newUsageRowJSON(row observe.UsageRow) usageRowJSON {
	return usageRowJSON{
		Key:              row.Key,
		Label:            row.Label,
		Calls:            row.Calls,
		Succeeded:        row.Succeeded,
		InputTokens:      row.InputTokens,
		OutputTokens:     row.OutputTokens,
		CacheWriteTokens: row.CacheWriteTokens,
		CacheReadTokens:  row.CacheReadTokens,
		Charged:          row.Amount.String(),
	}
}

func newUsageReportJSON(report observe.UsageReport) usageReportJSON {
	items := make([]usageRowJSON, 0, len(report.Items))
	for _, row := range report.Items {
		items = append(items, newUsageRowJSON(row))
	}
	view := "spend"
	if report.Revenue {
		view = "revenue"
	}
	return usageReportJSON{View: view, GroupBy: report.GroupBy, From: report.From, To: report.To, Items: items, Total: newUsageRowJSON(report.Total)}
}

func newChannelWindowJSON(window observe.Window) channelWindowJSON {
	return channelWindowJSON{Calls: window.Calls, Succeeded: window.Succeeded, SuccessRate: optionalRatio(window.SuccessRate)}
}

func newChannelStatsJSON(report observe.ChannelReport) channelStatsJSON {
	hourly := make([]channelStatsHourJSON, 0, len(report.Hourly))
	for _, bucket := range report.Hourly {
		hourly = append(hourly, channelStatsHourJSON{Hour: bucket.At, Calls: bucket.Attempts, Succeeded: bucket.Successes})
	}
	statuses := make([]channelStatsStatusJSON, 0, len(report.Statuses))
	for _, bucket := range report.Statuses {
		statuses = append(statuses, channelStatsStatusJSON{StatusCode: bucket.StatusCode, Count: bucket.Count})
	}
	failures := make([]channelFailureJSON, 0, len(report.Failures))
	for _, failure := range report.Failures {
		failures = append(failures, channelFailureJSON{
			CallID:       failure.CallID,
			CreatedAt:    failure.CreatedAt,
			ModelID:      failure.ModelID,
			StatusCode:   failure.Attempt.StatusCode,
			ErrorCode:    failure.Attempt.ErrorCode,
			ErrorMessage: failure.Attempt.ErrorMessage,
			EndReason:    failure.Attempt.EndReason,
		})
	}
	models := make([]channelStatsModelJSON, 0, len(report.Models))
	for _, bucket := range report.Models {
		models = append(models, channelStatsModelJSON{ModelID: bucket.ModelID, Calls: bucket.Attempts, Succeeded: bucket.Successes, Revenue: bucket.Revenue.String()})
	}
	daily := make([]channelStatsDayJSON, 0, len(report.Daily))
	for _, bucket := range report.Daily {
		daily = append(daily, channelStatsDayJSON{Date: bucket.Day, Calls: bucket.Attempts, Succeeded: bucket.Successes, Revenue: bucket.Revenue.String()})
	}
	events := make([]channelEventJSON, 0, len(report.Events))
	for _, event := range report.Events {
		events = append(events, channelEventJSON{ID: itoa(event.ID), Kind: event.Kind, Reason: event.Reason, CreatedAt: event.CreatedAt})
	}
	return channelStatsJSON{
		From:                     report.From,
		To:                       report.To,
		Calls:                    report.Window.Calls,
		Succeeded:                report.Window.Succeeded,
		SuccessRate:              optionalRatio(report.Window.SuccessRate),
		Revenue:                  report.Revenue.String(),
		TTFTP50MS:                report.TTFTP50MS,
		TTFTP95MS:                report.TTFTP95MS,
		OutputTokensPerSecondP50: report.SpeedP50,
		OutputTokensPerSecondP95: report.SpeedP95,
		Last24h:                  newChannelWindowJSON(report.Last24h),
		Last7d:                   newChannelWindowJSON(report.Last7d),
		Hourly:                   hourly,
		StatusCodes:              statuses,
		RecentFailures:           failures,
		Today: channelStatsTodayJSON{
			Revenue: report.TodayRevenue.String(), DailyCap: nullableAmount(report.DailyCap), Progress: optionalRatio(report.Progress),
		},
		ByModel: models,
		Daily:   daily,
		Events:  events,
	}
}

// convertRows renders the rows of a call page with the response type of the endpoint.
func convertRows[T any](rows []observe.CallRow, convert func(observe.CallRow) T) []T {
	items := make([]T, 0, len(rows))
	for _, row := range rows {
		items = append(items, convert(row))
	}
	return items
}

// ---- handlers ----

// queryCalls reads one page of the calls matching the filter. It writes the error response itself
// and reports false when the request is invalid or the query fails. A page has a summary exactly
// when withStats is set.
func (a *app) queryCalls(w http.ResponseWriter, r *http.Request, filter observe.CallFilter, withStats bool) (observe.CallPage, bool) {
	limit, limitOK := pageLimit(r)
	cursor, cursorOK := decodeCallCursor(r)
	if !limitOK {
		writeInvalidQuery(w)
		return observe.CallPage{}, false
	}
	if !cursorOK {
		writeBadCursor(w)
		return observe.CallPage{}, false
	}
	page, err := a.observe.Calls(r.Context(), filter, cursor, limit, withStats)
	if err != nil {
		writeObserveError(w, err)
		return observe.CallPage{}, false
	}
	return page, true
}

func (a *app) listMyCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseCallFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	filter.AccountID = accountFromContext(r.Context()).ID
	page, ok := a.queryCalls(w, r, filter, true)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, callPageJSON{
		Items: convertRows(page.Items, newCallSummaryJSON), NextCursor: encodeCallCursor(page.Next), Summary: newCallStatsJSON(*page.Stats),
	})
}

func (a *app) listAdminCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseCallFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	page, ok := a.queryCalls(w, r, filter, true)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, adminCallPageJSON{
		Items: convertRows(page.Items, newAdminCallJSON), NextCursor: encodeCallCursor(page.Next), Summary: newCallStatsJSON(*page.Stats),
	})
}

func (a *app) listChannelCalls(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if !uuidPattern.MatchString(channelID) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if _, err := a.observe.ChannelAccess(r.Context(), viewerOf(r), channelID); err != nil {
		writeObserveError(w, err)
		return
	}
	filter, ok := parseCallFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	// A channel owner sees calls by outcome, model, format and size only, never
	// by consumer, key, tag or cost.
	filter = observe.CallFilter{
		ScopeChannelID: channelID, Model: filter.Model, Format: filter.Format, Outcome: filter.Outcome, RequestID: filter.RequestID,
		From: filter.From, To: filter.To, MinDurationMS: filter.MinDurationMS, MaxDurationMS: filter.MaxDurationMS,
		MinTokens: filter.MinTokens, MaxTokens: filter.MaxTokens,
	}
	page, ok := a.queryCalls(w, r, filter, false)
	if !ok {
		return
	}
	items := convertRows(page.Items, func(row observe.CallRow) channelCallJSON { return newChannelCallJSON(row, channelID) })
	writeJSON(w, http.StatusOK, pageJSON[channelCallJSON]{Items: items, NextCursor: encodeCallCursor(page.Next)})
}

func (a *app) getCall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("callID")
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	detail, err := a.observe.Call(r.Context(), id, viewerOf(r))
	if err != nil {
		writeObserveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, callDetailEnvelopeJSON{Call: newCallDetailJSON(detail)})
}

func (a *app) getUsage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, fromOK := parseTimeParam(r, "from")
	to, toOK := parseTimeParam(r, "to")
	view := query.Get("view")
	if view != "" && view != "spend" && view != "revenue" {
		writeInvalidQuery(w)
		return
	}
	request := observe.UsageQuery{
		AccountID: accountFromContext(r.Context()).ID, Revenue: view == "revenue", GroupBy: query.Get("group_by"),
		APIKeyID: query.Get("api_key_id"), ChannelID: query.Get("channel_id"), Model: query.Get("model"), Tag: query.Get("tag"),
	}
	for _, id := range []string{request.APIKeyID, request.ChannelID} {
		if id != "" && !uuidPattern.MatchString(id) {
			writeInvalidQuery(w)
			return
		}
	}
	if !fromOK || !toOK {
		writeInvalidQuery(w)
		return
	}
	if from != nil {
		request.From = *from
	}
	if to != nil {
		request.To = *to
	}
	report, err := a.observe.Usage(r.Context(), request)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newUsageReportJSON(report))
}

func (a *app) getChannelStats(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	if !uuidPattern.MatchString(channelID) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	from, fromOK := parseTimeParam(r, "from")
	to, toOK := parseTimeParam(r, "to")
	if !fromOK || !toOK {
		writeInvalidQuery(w)
		return
	}
	report, err := a.observe.ChannelStats(r.Context(), viewerOf(r), channelID, from, to)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newChannelStatsJSON(report))
}

// registerObserveRoutes 注册调用、用量与渠道统计路由（Feature G）。
func (a *app) registerObserveRoutes(r *router) {
	r.implement("G", "GET /api/calls", accessReady, a.listMyCalls)
	r.implement("G", "GET /api/calls/stream", accessReady, a.streamMyCalls)
	r.implement("G", "GET /api/calls/{callID}", accessReady, a.getCall)
	r.implement("G", "GET /api/usage", accessReady, a.getUsage)
	r.implement("G", "GET /api/channels/{channelID}/stats", accessReady, a.getChannelStats)
	r.implement("G", "GET /api/channels/{channelID}/calls", accessReady, a.listChannelCalls)
	r.implement("G", "GET /api/channels/{channelID}/calls/stream", accessReady, a.streamChannelCalls)
	r.implement("G", "GET /api/admin/calls", accessAdmin, a.listAdminCalls)
	r.implement("G", "GET /api/admin/calls/stream", accessAdmin, a.streamAdminCalls)
	r.implement("G", "GET /api/admin/overview", accessAdmin, a.getAdminOverview)
	r.implement("G", "GET /api/admin/points", accessAdmin, a.getAdminPoints)
	r.implement("G", "GET /api/admin/ledger/transactions", accessAdmin, a.listAdminTransactions)
	r.implement("G", "GET /api/admin/ledger/transactions/{transactionID}", accessAdmin, a.getAdminTransaction)
	r.implement("G", "POST /api/admin/ledger/repair-call/{callID}", accessAdmin, a.repairCall)
}
