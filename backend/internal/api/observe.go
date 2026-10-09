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

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func usageJSON(usage observe.Usage) map[string]int64 {
	return map[string]int64{
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
		"cache_write_tokens": usage.CacheWriteTokens, "cache_read_tokens": usage.CacheReadTokens,
	}
}

func channelRefJSON(ref *observe.ChannelRef) any {
	if ref == nil {
		return nil
	}
	return map[string]any{"id": ref.ID, "name": ref.Name}
}

func keyRefJSON(ref *observe.KeyRef) any {
	if ref == nil {
		return nil
	}
	return map[string]any{"id": ref.ID, "name": ref.Name}
}

func accountRefJSON(ref observe.AccountRef) map[string]any {
	return map[string]any{"id": ref.ID, "username": ref.Username, "display_name": ref.DisplayName}
}

func optionalRatio(value *float64) any {
	if value == nil {
		return nil
	}
	return ratio(*value)
}

func callSummaryJSON(row observe.CallRow) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": row.CreatedAt, "completed_at": row.CompletedAt, "model_id": optionalString(row.ModelID),
		"requested_model": row.RequestedModel, "format": row.Format, "stream": row.Stream, "tag": optionalString(row.Tag),
		"api_key": keyRefJSON(row.Key), "outcome": row.Outcome, "channel": channelRefJSON(row.Channel), "attempt_count": row.AttemptCount,
		"usage": usageJSON(row.Usage), "cost": row.Cost.String(), "fee": row.Fee.String(), "charged": row.Charged().String(),
		"ttft_ms": row.TTFTMS, "duration_ms": row.DurationMS,
	}
}

func adminCallJSON(row observe.CallRow) map[string]any {
	call := callSummaryJSON(row)
	call["account"] = accountRefJSON(row.Account)
	return call
}

// lastFailure is the most recent failed attempt among a call's scope attempts.
func lastFailure(attempts []observe.Attempt) any {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if !attempt.Succeeded() {
			return map[string]any{
				"status_code": attempt.StatusCode, "error_code": optionalString(attempt.ErrorCode), "error_message": optionalString(attempt.ErrorMessage),
			}
		}
	}
	return nil
}

func channelCallJSON(row observe.CallRow, channelID string) map[string]any {
	served := row.Channel != nil && row.Channel.ID == channelID
	revenue := money.Amount(0)
	if served {
		revenue = row.Revenue()
	}
	return map[string]any{
		"id": row.ID, "created_at": row.CreatedAt, "model_id": optionalString(row.ModelID), "format": row.Format, "stream": row.Stream,
		"outcome": row.Outcome, "usage": usageJSON(row.Usage), "revenue": revenue.String(), "served": served,
		"ttft_ms": row.TTFTMS, "duration_ms": row.DurationMS, "error": lastFailure(row.ScopeAttempts),
	}
}

func callStatsJSON(stats observe.CallStats) map[string]any {
	return map[string]any{
		"calls": stats.Calls, "succeeded": stats.Succeeded, "failed": stats.Failed, "success_rate": optionalRatio(stats.SuccessRate()),
		"charged": stats.Charged.String(), "input_tokens": stats.InputTokens, "output_tokens": stats.OutputTokens,
		"total_tokens": stats.TotalTokens(), "ttft_p50_ms": stats.TTFTP50MS, "ttft_p95_ms": stats.TTFTP95MS,
	}
}

func attemptJSON(attempt observe.Attempt) map[string]any {
	return map[string]any{
		"channel": channelRefJSON(attempt.Channel), "status_code": attempt.StatusCode, "error_code": optionalString(attempt.ErrorCode),
		"error_message": optionalString(attempt.ErrorMessage), "connect_ms": attempt.ConnectMS, "ttft_ms": attempt.TTFTMS,
		"duration_ms": attempt.DurationMS, "response_bytes": attempt.ResponseByte, "end_reason": attempt.EndReason,
	}
}

func rawJSONOrNil(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func callDetailJSON(detail observe.CallDetail) map[string]any {
	call := callSummaryJSON(detail.CallRow)
	attempts := make([]map[string]any, 0, len(detail.Attempts))
	for _, attempt := range detail.Attempts {
		attempts = append(attempts, attemptJSON(attempt))
	}
	call["routing_mode"] = optionalString(detail.RoutingMode)
	call["routing_source"] = optionalString(detail.RoutingSource)
	call["client_user_agent"] = optionalString(detail.ClientUserAgent)
	call["attempts"] = attempts
	call["price_snapshot"] = rawJSONOrNil(detail.PriceSnapshot)
	speed := detail.TokensPerSecond
	if !detail.Stream {
		// Non-streaming speed is the end-to-end average, including all attempts.
		speed = nil
		if detail.DurationMS != nil && *detail.DurationMS > 0 && detail.Usage.OutputTokens > 0 {
			value := float64(detail.Usage.OutputTokens) * 1000 / float64(*detail.DurationMS)
			speed = &value
		}
	}
	call["output_tokens_per_second"] = speed
	call["inter_token_p50_ms"] = detail.IntervalP50MS
	call["inter_token_p95_ms"] = detail.IntervalP95MS
	call["response_bytes"] = detail.ResponseBytes
	call["ledger_transaction_id"] = optionalString(detail.LedgerTxID)
	return call
}

// ---- handlers ----

func (a *app) listCallsPage(w http.ResponseWriter, r *http.Request, filter observe.CallFilter, render func(observe.CallRow) map[string]any, withStats bool) {
	limit, limitOK := pageLimit(r)
	cursor, cursorOK := decodeCallCursor(r)
	if !limitOK {
		writeInvalidQuery(w)
		return
	}
	if !cursorOK {
		writeBadCursor(w)
		return
	}
	page, err := a.observe.Calls(r.Context(), filter, cursor, limit, withStats)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, render(row))
	}
	response := map[string]any{"items": items, "next_cursor": encodeCallCursor(page.Next)}
	if page.Stats != nil {
		response["summary"] = callStatsJSON(*page.Stats)
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *app) listMyCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseCallFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	filter.AccountID = accountFromContext(r.Context()).ID
	a.listCallsPage(w, r, filter, callSummaryJSON, true)
}

func (a *app) listAdminCalls(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseCallFilter(r)
	if !ok {
		writeInvalidQuery(w)
		return
	}
	a.listCallsPage(w, r, filter, adminCallJSON, true)
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
	a.listCallsPage(w, r, filter, func(row observe.CallRow) map[string]any { return channelCallJSON(row, channelID) }, false)
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
	writeJSON(w, http.StatusOK, map[string]any{"call": callDetailJSON(detail)})
}

func usageRowJSON(row observe.UsageRow) map[string]any {
	return map[string]any{
		"key": row.Key, "label": row.Label, "calls": row.Calls, "succeeded": row.Succeeded,
		"input_tokens": row.InputTokens, "output_tokens": row.OutputTokens,
		"cache_write_tokens": row.CacheWriteTokens, "cache_read_tokens": row.CacheReadTokens, "charged": row.Amount.String(),
	}
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
	items := make([]map[string]any, 0, len(report.Items))
	for _, row := range report.Items {
		items = append(items, usageRowJSON(row))
	}
	viewName := "spend"
	if report.Revenue {
		viewName = "revenue"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"view": viewName, "group_by": report.GroupBy, "from": report.From, "to": report.To, "items": items, "total": usageRowJSON(report.Total),
	})
}

func windowJSON(window observe.Window) map[string]any {
	return map[string]any{"calls": window.Calls, "succeeded": window.Succeeded, "success_rate": optionalRatio(window.SuccessRate)}
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
	hourly := make([]map[string]any, 0, len(report.Hourly))
	for _, bucket := range report.Hourly {
		hourly = append(hourly, map[string]any{"hour": bucket.At, "calls": bucket.Attempts, "succeeded": bucket.Successes})
	}
	daily := make([]map[string]any, 0, len(report.Daily))
	for _, bucket := range report.Daily {
		daily = append(daily, map[string]any{"date": bucket.Day, "calls": bucket.Attempts, "succeeded": bucket.Successes, "revenue": bucket.Revenue.String()})
	}
	models := make([]map[string]any, 0, len(report.Models))
	for _, bucket := range report.Models {
		models = append(models, map[string]any{"model_id": bucket.ModelID, "calls": bucket.Attempts, "succeeded": bucket.Successes, "revenue": bucket.Revenue.String()})
	}
	statuses := make([]map[string]any, 0, len(report.Statuses))
	for _, bucket := range report.Statuses {
		statuses = append(statuses, map[string]any{"status_code": bucket.StatusCode, "count": bucket.Count})
	}
	failures := make([]map[string]any, 0, len(report.Failures))
	for _, failure := range report.Failures {
		failures = append(failures, map[string]any{
			"call_id": failure.CallID, "created_at": failure.CreatedAt, "model_id": optionalString(failure.ModelID),
			"status_code": failure.Attempt.StatusCode, "error_code": optionalString(failure.Attempt.ErrorCode),
			"error_message": optionalString(failure.Attempt.ErrorMessage), "end_reason": failure.Attempt.EndReason,
		})
	}
	events := make([]map[string]any, 0, len(report.Events))
	for _, event := range report.Events {
		events = append(events, map[string]any{"id": strconv.FormatInt(event.ID, 10), "kind": event.Kind, "reason": event.Reason, "created_at": event.CreatedAt})
	}
	var dailyCap any
	if report.DailyCap != nil {
		dailyCap = report.DailyCap.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": report.From, "to": report.To, "calls": report.Window.Calls, "succeeded": report.Window.Succeeded,
		"success_rate": optionalRatio(report.Window.SuccessRate), "revenue": report.Revenue.String(),
		"ttft_p50_ms": report.TTFTP50MS, "ttft_p95_ms": report.TTFTP95MS,
		"output_tokens_per_second_p50": report.SpeedP50, "output_tokens_per_second_p95": report.SpeedP95,
		"last_24h": windowJSON(report.Last24h), "last_7d": windowJSON(report.Last7d),
		"hourly": hourly, "status_codes": statuses, "recent_failures": failures,
		"today":    map[string]any{"revenue": report.TodayRevenue.String(), "daily_cap": dailyCap, "progress": optionalRatio(report.Progress)},
		"by_model": models, "daily": daily, "events": events,
	})
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
