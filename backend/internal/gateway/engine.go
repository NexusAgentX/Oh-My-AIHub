// Package gateway is the transparent API gateway (ADR-0027): it authenticates
// the caller, picks channels, forwards the request almost byte for byte,
// streams the response back unchanged while reading usage on the side, and
// books the charge after the call has ended.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const (
	maxRequestBytes  = 32 << 20
	maxFailureBody   = 1 << 20
	maxErrorMessage  = 4096
	chunkSize        = 32 << 10
	clientWriteLimit = 2 * time.Minute
	statsTTL         = 60 * time.Second
	settingsTTL      = 5 * time.Second
	keyTouchEvery    = time.Minute
)

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

var errTTFT = errors.New("first byte timeout")

// Dependencies wires the gateway.
type Dependencies struct {
	Store    Store
	Catalog  *catalog.Service
	Settings *settings.Service
	Routing  routing.Store
	Keyring  *channel.Keyring
	Outbound channel.Outbound
	Events   Publisher
	Logger   *slog.Logger
	Runtime  *Runtime
	Now      func() time.Time
}

// Engine serves the gateway endpoints.
type Engine struct {
	Dependencies
	spend *SpendCache

	mutex         sync.Mutex
	settingsValue settings.Settings
	settingsAt    time.Time
	outboundValue channel.Outbound
	statsValue    map[string]ChannelStats
	statsAt       time.Time
	touched       map[string]time.Time
}

func NewEngine(dependencies Dependencies) *Engine {
	if dependencies.Events == nil {
		dependencies.Events = discard{}
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.Runtime == nil {
		dependencies.Runtime = NewRuntime(nil)
	}
	return &Engine{Dependencies: dependencies, spend: NewSpendCache(), touched: map[string]time.Time{}}
}

// Runtime exposes the in-process channel state for display.
func (e *Engine) State() *Runtime { return e.Runtime }

// Settings returns the platform settings, cached for a few seconds.
func (e *Engine) Settings(ctx context.Context) (settings.Settings, channel.Outbound, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.outboundValue == nil || e.Now().Sub(e.settingsAt) > settingsTTL {
		value, err := e.Dependencies.Settings.Get(ctx)
		if err != nil {
			return settings.Settings{}, nil, err
		}
		policy, err := e.Outbound.WithExtraBlockedHosts(value.ExtraBlockedHosts)
		if err != nil {
			return settings.Settings{}, nil, err
		}
		e.settingsValue, e.outboundValue, e.settingsAt = value, policy, e.Now()
	}
	return e.settingsValue, e.outboundValue, nil
}

// Stats returns the 24h channel health, cached for a minute.
func (e *Engine) Stats(ctx context.Context) map[string]ChannelStats {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.statsValue == nil || e.Now().Sub(e.statsAt) > statsTTL {
		value, err := e.Store.ChannelStats(ctx)
		if err != nil {
			e.Logger.Error("gateway: channel stats failed", "error", err)
			if e.statsValue == nil {
				return map[string]ChannelStats{}
			}
			return e.statsValue
		}
		e.statsValue, e.statsAt = value, e.Now()
	}
	return e.statsValue
}

// Limits resolves the effective limits of a channel against the defaults.
func Limits2(advanced channel.Advanced, defaults settings.Settings) Limits {
	limits := Limits{CooldownAfter: int(defaults.DefaultCooldownFailures), CooldownFor: time.Duration(defaults.DefaultCooldownSeconds) * time.Second}
	if advanced.ConcurrencyLimit != nil {
		limits.Concurrency = int(*advanced.ConcurrencyLimit)
	}
	if advanced.RPMLimit != nil {
		limits.RPM = int(*advanced.RPMLimit)
	}
	if advanced.DailyRevenueCap != nil {
		limits.DailyRevenueCap = *advanced.DailyRevenueCap
	}
	if advanced.CooldownFailures != nil {
		limits.CooldownAfter = int(*advanced.CooldownFailures)
	}
	if advanced.CooldownSeconds != nil {
		limits.CooldownFor = time.Duration(*advanced.CooldownSeconds) * time.Second
	}
	return limits
}

// callState is everything known about one call while it runs.
type callState struct {
	id            string
	started       time.Time
	key           KeyAuth
	format        channel.Format
	stream        bool
	requested     string
	modelID       string
	tag           string
	userAgent     string
	mode          routing.Mode
	source        routing.Source
	attempts      []Attempt
	lastChannelID string
}

// ---- authentication ----

func extractKey(r *http.Request) (secret string, location authLocation, found bool) {
	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(strings.TrimSpace(value), apikey.Prefix) {
		return strings.TrimSpace(value), authBearer, true
	}
	if value := strings.TrimSpace(r.Header.Get("x-api-key")); strings.HasPrefix(value, apikey.Prefix) {
		return value, authXAPIKey, true
	}
	if value := strings.TrimSpace(r.Header.Get("x-goog-api-key")); strings.HasPrefix(value, apikey.Prefix) {
		return value, authGoogleHeader, true
	}
	if value := strings.TrimSpace(r.URL.Query().Get("key")); strings.HasPrefix(value, apikey.Prefix) {
		return value, authQuery, true
	}
	return "", 0, false
}

// authenticate resolves the caller; on failure it has already answered.
func (e *Engine) authenticate(w http.ResponseWriter, r *http.Request) (KeyAuth, authLocation, bool) {
	secret, location, found := extractKey(r)
	if !found {
		writeGatewayError(w, http.StatusUnauthorized, "invalid_api_key", "缺少有效的 API Key", "")
		return KeyAuth{}, 0, false
	}
	key, err := e.Store.LookupKey(r.Context(), apikey.Hash(secret))
	if errors.Is(err, ErrKeyNotFound) {
		writeGatewayError(w, http.StatusUnauthorized, "invalid_api_key", "API Key 无效", "")
		return KeyAuth{}, 0, false
	}
	if err != nil {
		e.Logger.Error("gateway: key lookup failed", "error", err)
		writeGatewayError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return KeyAuth{}, 0, false
	}
	return key, location, true
}

func keyUsable(key KeyAuth, now time.Time) bool {
	return key.Enabled && (key.ExpiresAt == nil || now.Before(*key.ExpiresAt))
}

func (e *Engine) touchKey(ctx context.Context, keyID string) {
	e.mutex.Lock()
	last := e.touched[keyID]
	due := e.Now().Sub(last) >= keyTouchEvery
	if due {
		e.touched[keyID] = e.Now()
	}
	e.mutex.Unlock()
	if due {
		if err := e.Store.TouchKey(ctx, keyID, e.Now()); err != nil {
			e.Logger.Warn("gateway: touching key failed", "error", err)
		}
	}
}

func writeGatewayError(w http.ResponseWriter, status int, code, message, requestID string) {
	body := map[string]string{"error": code, "message": message}
	if requestID != "" {
		body["request_id"] = requestID
		w.Header().Set("X-AIHub-Request-Id", requestID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---- inference ----

// Serve handles one inference request of the given format. pathModel is the
// model named in a Gemini path.
func (e *Engine) Serve(w http.ResponseWriter, r *http.Request, format channel.Format, pathModel string, pathStream bool) {
	ctx := r.Context()
	started := e.Now()
	key, location, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	call := &callState{started: started, key: key, format: format, tag: sanitizeTag(r.Header.Get("X-AIHub-Tag")), userAgent: truncate(r.UserAgent(), 512)}

	if !key.AccountActive || !keyUsable(key, started) {
		status, code, message := http.StatusUnauthorized, "invalid_api_key", "API Key 已停用或过期"
		if !key.AccountActive {
			status, code, message = http.StatusForbidden, "account_disabled", "账号已停用"
		}
		call.requested = pathModel
		e.reject(w, call, OutcomeRejectedKey, status, code, message)
		return
	}
	e.touchKey(ctx, key.KeyID)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		call.requested = pathModel
		e.reject(w, call, OutcomeRejectedModel, http.StatusRequestEntityTooLarge, "request_too_large", "请求体过大或读取失败")
		return
	}

	var scan objectScan
	stickyResponseID := ""
	if format == channel.FormatGemini {
		call.requested, call.stream = pathModel, pathStream
	} else {
		facts, parsed, scanErr := ScanRequest(body)
		call.requested, call.stream, scan = facts.Model, facts.Stream, parsed
		if scanErr != nil || !facts.HasModel || facts.Model == "" {
			e.reject(w, call, OutcomeRejectedModel, http.StatusBadRequest, "invalid_request", "请求体缺少 model 字段或不是合法的 JSON 对象")
			return
		}
		call.stream = facts.Stream
		if format == channel.FormatOpenAIResponses {
			stickyResponseID = facts.PreviousResponseID
		}
	}

	id, err := e.Store.InsertCall(ctx, CallInsert{
		AccountID: key.OwnerID, KeyID: key.KeyID, RequestedModel: call.requested, Format: format, Stream: call.stream,
		Tag: call.tag, UserAgent: call.userAgent, Outcome: OutcomeInProgress,
	})
	if err != nil {
		e.Logger.Error("gateway: opening call failed", "error", err)
		writeGatewayError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return
	}
	call.id = id
	w.Header().Set("X-AIHub-Request-Id", id)
	e.Events.Publish(Event{Kind: EventCallStarted, At: started, CallID: id, AccountID: key.OwnerID, KeyID: key.KeyID, Model: call.requested, Format: string(format)})

	// Model: alias first, then the key's allowed list, then the catalog.
	call.modelID = call.requested
	if target, aliased := key.Aliases[call.requested]; aliased {
		call.modelID = target
	}
	if len(key.AllowedModels) > 0 && !containsString(key.AllowedModels, call.modelID) {
		e.finishRejected(w, call, OutcomeRejectedModel, http.StatusForbidden, "model_not_allowed", "该 API Key 不允许使用此模型")
		return
	}
	model, err := e.Catalog.Get(ctx, call.modelID)
	if errors.Is(err, catalog.ErrNotFound) || (err == nil && !model.Enabled) {
		e.finishRejected(w, call, OutcomeRejectedModel, http.StatusNotFound, "model_not_found", "模型不存在或未启用")
		return
	}
	if err != nil {
		e.internalError(w, call, err)
		return
	}

	defaults, policy, err := e.Settings(ctx)
	if err != nil {
		e.internalError(w, call, err)
		return
	}

	// Balance and budgets are checked, never reserved (Epic #170 decision 6).
	points, err := e.Store.Points(ctx, key.OwnerID)
	if err != nil {
		e.internalError(w, call, err)
		return
	}
	if points.Balance.Nano() <= -points.CreditLimit.Nano() {
		e.finishRejected(w, call, OutcomeRejectedBalance, http.StatusPaymentRequired, "insufficient_balance", "积分余额不足")
		return
	}
	if key.BudgetDaily != nil || key.BudgetMonthly != nil || key.BudgetTotal != nil {
		spend, err := e.spend.Get(key.KeyID, started, func() (Spend, error) { return e.Store.KeySpend(ctx, key.KeyID, started) })
		if err != nil {
			e.internalError(w, call, err)
			return
		}
		if (key.BudgetDaily != nil && spend.Today >= *key.BudgetDaily) || (key.BudgetMonthly != nil && spend.Month >= *key.BudgetMonthly) ||
			(key.BudgetTotal != nil && spend.Total >= *key.BudgetTotal) {
			e.finishRejected(w, call, OutcomeRejectedBudget, http.StatusPaymentRequired, "key_budget_exceeded", "该 API Key 的预算已用完")
			return
		}
	}

	// Routing: the key's own setting, else the account's, else the default.
	pref, err := e.Routing.Resolve(ctx, key.OwnerID, key.KeyID, call.modelID)
	if err != nil {
		e.internalError(w, call, err)
		return
	}
	call.mode, call.source = pref.Mode, pref.Source
	maxAttempts := int(defaults.DefaultMaxAttempts)
	if pref.MaxAttempts != nil {
		maxAttempts = int(*pref.MaxAttempts)
	}
	ttftTimeout := time.Duration(defaults.DefaultTTFTTimeoutMS) * time.Millisecond
	if pref.TTFTTimeoutMS != nil {
		ttftTimeout = time.Duration(*pref.TTFTTimeoutMS) * time.Millisecond
	}

	candidates, err := e.Store.Candidates(ctx, call.modelID, format)
	if err != nil {
		e.internalError(w, call, err)
		return
	}
	for _, candidate := range candidates {
		if limits := Limits2(candidate.Advanced, defaults); limits.DailyRevenueCap > 0 && e.Runtime.NeedsRevenue(candidate.ChannelID, started) {
			revenue, err := e.Store.ChannelRevenue(ctx, candidate.ChannelID, started)
			if err != nil {
				e.Logger.Warn("gateway: channel revenue lookup failed", "error", err)
				continue
			}
			e.Runtime.SetRevenue(candidate.ChannelID, revenue, started)
		}
	}
	sticky := ""
	if stickyResponseID != "" {
		if channelID, err := e.Store.ResponseChannel(ctx, key.OwnerID, stickyResponseID); err == nil {
			sticky = channelID
		}
	}
	ordered := Rank(RankInput{
		Mode: pref.Mode, Order: pref.Order, Excluded: pref.Excluded, Candidates: candidates, Stats: e.Stats(ctx),
		ConsumerID: key.OwnerID, Sticky: sticky,
		Available: func(c Candidate) bool {
			state, _ := e.Runtime.Check(c.ChannelID, Limits2(c.Advanced, defaults), started)
			return state == StateAvailable
		},
	})
	if len(ordered) > maxAttempts {
		ordered = ordered[:maxAttempts]
	}
	if len(ordered) == 0 {
		e.finishRejected(w, call, OutcomeRejectedNoChannel, http.StatusServiceUnavailable, "no_channel_available", "当前没有可用的渠道")
		return
	}

	var last *upstreamFailure
	tried := 0
	for _, candidate := range ordered {
		limits := Limits2(candidate.Advanced, defaults)
		release, reserved, _ := e.Runtime.Begin(candidate.ChannelID, limits, e.Now())
		if !reserved {
			continue
		}
		tried++
		call.lastChannelID = candidate.ChannelID
		result := e.attempt(r, call, candidate, limits, defaults, policy, body, scan, location, ttftTimeout)
		call.attempts = append(call.attempts, result.record)
		e.publishAttempt(call, candidate, result.record)
		switch {
		case result.clientGone:
			release()
			e.finishAbandoned(call, "")
			return
		case result.response != nil && result.passthrough:
			e.Runtime.Success(candidate.ChannelID) // an answering upstream is healthy
			e.forwardPassthrough(w, call, result)
			release()
			return
		case result.response != nil:
			e.Runtime.Success(candidate.ChannelID)
			e.streamBack(w, r, call, model, candidate, result, defaults)
			release()
			return
		}
		release()
		if result.failure != nil {
			last = result.failure
		}
	}

	if tried == 0 {
		e.finishRejected(w, call, OutcomeRejectedNoChannel, http.StatusServiceUnavailable, "no_channel_available", "当前没有可用的渠道")
		return
	}
	e.finishUpstreamFailure(w, call, last)
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func sanitizeTag(value string) string {
	if tagPattern.MatchString(value) {
		return value
	}
	return ""
}

func truncate(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func (e *Engine) internalError(w http.ResponseWriter, call *callState, err error) {
	e.Logger.Error("gateway: request failed", "request_id", call.id, "error", err)
	if call.id == "" {
		writeGatewayError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
		return
	}
	e.finishRejected(w, call, OutcomeUpstreamFailed, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作")
}

// reject records and answers a call refused before it was opened.
func (e *Engine) reject(w http.ResponseWriter, call *callState, outcome string, status int, code, message string) {
	id, err := e.Store.InsertCall(context.WithoutCancel(context.Background()), CallInsert{
		AccountID: call.key.OwnerID, KeyID: call.key.KeyID, RequestedModel: call.requested, Format: call.format, Stream: call.stream,
		Tag: call.tag, UserAgent: call.userAgent, Outcome: outcome,
	})
	if err != nil {
		e.Logger.Error("gateway: recording rejected call failed", "error", err)
		writeGatewayError(w, status, code, message, "")
		return
	}
	call.id = id
	e.log(call, outcome, status, 0, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Model: call.requested, Format: string(call.format), Outcome: outcome, StatusCode: status})
	writeGatewayError(w, status, code, message, id)
}

// finishRejected closes an already opened call without a channel.
func (e *Engine) finishRejected(w http.ResponseWriter, call *callState, outcome string, status int, code, message string) {
	duration := int(e.Now().Sub(call.started).Milliseconds())
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: outcome, ModelID: call.modelID, RoutingMode: string(call.mode),
		RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration,
	})
	if err != nil {
		e.Logger.Error("gateway: finishing rejected call failed", "request_id", call.id, "error", err)
	}
	e.log(call, outcome, status, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Model: call.modelID, Format: string(call.format), Outcome: outcome, StatusCode: status, DurationMS: duration})
	writeGatewayError(w, status, code, message, call.id)
}

// finishAbandoned closes a call whose client left before any output.
func (e *Engine) finishAbandoned(call *callState, channelID string) {
	duration := int(e.Now().Sub(call.started).Milliseconds())
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeClientDisconnect, ModelID: call.modelID, ChannelID: channelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration,
	})
	if err != nil {
		e.Logger.Error("gateway: finishing abandoned call failed", "request_id", call.id, "error", err)
	}
	e.log(call, OutcomeClientDisconnect, 0, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Model: call.modelID, Format: string(call.format), Outcome: OutcomeClientDisconnect, DurationMS: duration})
}

func (e *Engine) publishAttempt(call *callState, candidate Candidate, record Attempt) {
	event := Event{Kind: EventAttemptFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: candidate.ChannelID, Model: call.modelID, Format: string(call.format), EndReason: record.EndReason}
	if record.StatusCode != nil {
		event.StatusCode = *record.StatusCode
	}
	if record.DurationMS != nil {
		event.DurationMS = *record.DurationMS
	}
	if record.TTFTMS != nil {
		event.TTFTMS = *record.TTFTMS
	}
	e.Events.Publish(event)
}

// log writes the one structured line per gateway request. It carries no body
// and no key material: only the key's display prefix.
func (e *Engine) log(call *callState, outcome string, status, duration int, usage ledger.Usage, cost, fee money.Amount) {
	e.Logger.Info("gateway request",
		"request_id", call.id, "account_id", call.key.OwnerID, "key_prefix", call.key.Prefix, "model", call.modelID,
		"format", string(call.format), "channel_id", call.lastChannelID, "outcome", outcome, "status", status,
		"duration_ms", duration, "input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens,
		"cache_write_tokens", usage.CacheWriteTokens, "cache_read_tokens", usage.CacheReadTokens,
		"cost", cost.String(), "fee", fee.String())
}

// ---- one attempt ----

type upstreamFailure struct {
	status int
	header http.Header
	body   []byte
}

type attemptResult struct {
	record      Attempt
	response    *http.Response
	first       []byte
	firstErr    error
	cancel      context.CancelCauseFunc
	started     time.Time
	ttft        time.Duration
	secret      string
	failure     *upstreamFailure
	passthrough bool
	clientGone  bool
}

func millis(d time.Duration) *int {
	value := int(d.Milliseconds())
	return &value
}

func intPointer(value int) *int { return &value }

func stringPointer(value string) *string { return &value }

// scrub removes the upstream credential from text that may echo it.
func scrub(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "***")
		text = strings.ReplaceAll(text, url.QueryEscape(secret), "***")
	}
	return strings.ToValidUTF8(text, "")
}

func limitMessage(text string) string {
	if len(text) > maxErrorMessage {
		text = text[:maxErrorMessage]
	}
	return strings.ToValidUTF8(text, "")
}

func fallbackStatus(status int) bool {
	switch {
	case status >= 500, status < 200, status >= 300 && status < 400:
		return true
	}
	switch status {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	}
	return false
}

func (e *Engine) attempt(r *http.Request, call *callState, candidate Candidate, limits Limits, defaults settings.Settings,
	policy channel.Outbound, body []byte, scan objectScan, location authLocation, ttft time.Duration) attemptResult {
	ctx := r.Context()
	result := attemptResult{started: e.Now(), record: Attempt{Channel: &AttemptChannel{ID: candidate.ChannelID, Name: candidate.Name}}}
	fail := func(endReason, code, message string, status *int) attemptResult {
		result.record.EndReason, result.record.ErrorCode = endReason, stringPointer(code)
		if message != "" {
			result.record.ErrorMessage = stringPointer(limitMessage(scrub(message, result.secret)))
		}
		result.record.StatusCode = status
		result.record.DurationMS = millis(e.Now().Sub(result.started))
		e.Runtime.Failure(candidate.ChannelID, limits, e.Now(), endReason)
		return result
	}

	secret, err := e.Keyring.Decrypt(candidate.ChannelID, candidate.Credential)
	if err != nil {
		e.Logger.Error("gateway: channel credential unavailable", "channel_id", candidate.ChannelID, "error", err)
		return fail(EndConnectError, "credential_unavailable", "渠道凭据不可用", nil)
	}
	result.secret = secret

	total := time.Duration(defaults.DefaultTotalTimeoutMS) * time.Millisecond
	if candidate.Advanced.TotalTimeoutMS != nil {
		total = time.Duration(*candidate.Advanced.TotalTimeoutMS) * time.Millisecond
	}
	if candidate.Advanced.TTFTTimeoutMS != nil {
		ttft = time.Duration(*candidate.Advanced.TTFTTimeoutMS) * time.Millisecond
	}
	client, err := policy.GatewayClient(ctx, candidate.BaseURL, total)
	if err != nil {
		return fail(EndConnectError, "egress_blocked", "出站校验未通过", nil)
	}

	upstreamBody := body
	if call.format != channel.FormatGemini {
		upstreamBody = ReplaceModel(body, scan, candidate.UpstreamModel)
		if call.format == channel.FormatOpenAIChat && call.stream {
			_, rescanned, scanErr := ScanRequest(upstreamBody)
			if scanErr == nil {
				upstreamBody = EnsureIncludeUsage(upstreamBody, rescanned)
			}
		}
	}
	target, err := UpstreamURL(candidate.BaseURL, r.URL.Path, r.URL.RawQuery, call.format, candidate.UpstreamModel, location, secret)
	if err != nil {
		return fail(EndConnectError, "bad_upstream_url", "无法构造上游地址", nil)
	}

	attemptCtx, cancel := context.WithCancelCause(ctx)
	result.cancel = cancel
	timer := time.AfterFunc(ttft, func() { cancel(errTTFT) })
	var connected time.Time
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected = e.Now() }}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(attemptCtx, trace), http.MethodPost, target, bytes.NewReader(upstreamBody))
	if err != nil {
		timer.Stop()
		cancel(nil)
		return fail(EndConnectError, "bad_upstream_request", "无法构造上游请求", nil)
	}
	request.Header = UpstreamHeaders(r.Header, location, secret, candidate.Advanced)
	request.ContentLength = int64(len(upstreamBody))

	response, err := client.Do(request)
	if err != nil {
		timer.Stop()
		cause := context.Cause(attemptCtx)
		cancel(nil)
		cancelled := ctx.Err() != nil
		message := err.Error()
		var urlError *url.Error
		if errors.As(err, &urlError) {
			message = urlError.Err.Error()
		}
		switch {
		case cancelled:
			result.clientGone = true
			result.record.EndReason = EndClientDisconnected
			result.record.DurationMS = millis(e.Now().Sub(result.started))
			return result
		case errors.Is(cause, errTTFT):
			return fail(EndTimeoutTTFT, "timeout_ttft", "首字超时", nil)
		case isTimeout(err):
			return fail(EndTimeoutTotal, "timeout_total", "上游响应超时", nil)
		default:
			return fail(EndConnectError, "connect_error", message, nil)
		}
	}
	if !connected.IsZero() {
		result.record.ConnectMS = millis(connected.Sub(result.started))
	}
	status := response.StatusCode
	result.record.StatusCode = &status

	if status >= 200 && status < 300 {
		chunk := make([]byte, chunkSize)
		n, readErr := 0, error(nil)
		for n == 0 && readErr == nil {
			n, readErr = response.Body.Read(chunk)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) && n == 0 {
			timer.Stop()
			cause := context.Cause(attemptCtx)
			response.Body.Close()
			cancel(nil)
			switch {
			case ctx.Err() != nil:
				result.clientGone = true
				result.record.EndReason = EndClientDisconnected
				result.record.DurationMS = millis(e.Now().Sub(result.started))
				return result
			case errors.Is(cause, errTTFT):
				return fail(EndTimeoutTTFT, "timeout_ttft", "首字超时", &status)
			default:
				return fail(EndInterrupted, "read_error", "读取上游响应失败", &status)
			}
		}
		timer.Stop()
		result.ttft = e.Now().Sub(result.started)
		result.record.TTFTMS = millis(result.ttft)
		result.response, result.first, result.firstErr = response, chunk[:n], readErr
		return result
	}

	timer.Stop()
	if !fallbackStatus(status) {
		// The request itself is at fault: hand the upstream answer back as is.
		result.response, result.passthrough = response, true
		result.record.EndReason = EndClientError
		result.record.DurationMS = millis(e.Now().Sub(result.started))
		return result
	}
	payload, _ := io.ReadAll(io.LimitReader(response.Body, maxFailureBody))
	response.Body.Close()
	cancel(nil)
	result.failure = &upstreamFailure{status: status, header: ClientResponseHeaders(response.Header, response.Uncompressed), body: payload}
	return fail(EndUpstreamError, "upstream_"+http.StatusText(status), string(payload), &status)
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// ---- answering the client ----

// finishUpstreamFailure answers with the last upstream failure, or a generic
// 502 when no channel produced an HTTP answer.
func (e *Engine) finishUpstreamFailure(w http.ResponseWriter, call *callState, last *upstreamFailure) {
	duration := int(e.Now().Sub(call.started).Milliseconds())
	status := http.StatusBadGateway
	if last != nil {
		status = last.status
	}
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeUpstreamFailed, ModelID: call.modelID, ChannelID: call.lastChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration,
	})
	if err != nil {
		e.Logger.Error("gateway: finishing failed call failed", "request_id", call.id, "error", err)
	}
	e.log(call, OutcomeUpstreamFailed, status, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: call.lastChannelID, Model: call.modelID, Format: string(call.format), Outcome: OutcomeUpstreamFailed, StatusCode: status, DurationMS: duration})
	if last == nil {
		writeGatewayError(w, http.StatusBadGateway, "upstream_unavailable", "所有渠道均不可用", call.id)
		return
	}
	for name, values := range last.header {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", call.id)
	w.WriteHeader(last.status)
	_, _ = w.Write(last.body)
}

// forwardPassthrough returns an upstream 4xx unchanged, unbilled.
func (e *Engine) forwardPassthrough(w http.ResponseWriter, call *callState, result attemptResult) {
	response := result.response
	defer response.Body.Close()
	defer result.cancel(nil)
	for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", call.id)
	w.WriteHeader(response.StatusCode)
	written, _ := io.Copy(w, io.LimitReader(response.Body, maxFailureBody))
	duration := int(e.Now().Sub(call.started).Milliseconds())
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeUpstreamFailed, ModelID: call.modelID, ChannelID: call.lastChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration, ResponseBytes: written,
	})
	if err != nil {
		e.Logger.Error("gateway: finishing passthrough call failed", "request_id", call.id, "error", err)
	}
	e.log(call, OutcomeUpstreamFailed, response.StatusCode, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: call.lastChannelID, Model: call.modelID, Format: string(call.format), Outcome: OutcomeUpstreamFailed, StatusCode: response.StatusCode, DurationMS: duration})
}

// streamBack writes the upstream answer to the client chunk by chunk while the
// observer reads usage, then settles the call.
func (e *Engine) streamBack(w http.ResponseWriter, r *http.Request, call *callState, model catalog.Model, candidate Candidate, result attemptResult, defaults settings.Settings) {
	response := result.response
	defer response.Body.Close()
	defer result.cancel(nil)

	controller := http.NewResponseController(w)
	for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", call.id)
	_ = controller.SetWriteDeadline(e.Now().Add(clientWriteLimit))
	w.WriteHeader(response.StatusCode)

	observer := NewObserver(call.format, call.stream, response.Header.Get("Content-Type"))
	var written int64
	clientGone, upstreamBroke := false, false
	emit := func(chunk []byte) bool {
		if len(chunk) == 0 {
			return true
		}
		_ = controller.SetWriteDeadline(e.Now().Add(clientWriteLimit))
		if _, err := w.Write(chunk); err != nil {
			clientGone = true
			return false
		}
		_ = controller.Flush()
		written += int64(len(chunk))
		observer.Write(chunk, e.Now())
		return true
	}
	finished := emit(result.first)
	readErr := result.firstErr
	buffer := make([]byte, chunkSize)
	for finished && readErr == nil {
		var n int
		n, readErr = response.Body.Read(buffer)
		finished = emit(buffer[:n])
	}
	if finished && readErr != nil && !errors.Is(readErr, io.EOF) {
		if r.Context().Err() != nil {
			clientGone = true
		} else {
			upstreamBroke = true
		}
	}
	observation := observer.Finish()

	duration := int(e.Now().Sub(call.started).Milliseconds())
	ttftFromStart := int(result.started.Sub(call.started).Milliseconds()) + int(result.ttft.Milliseconds())
	outcome := OutcomeSucceeded
	endReason := EndCompleted
	switch {
	case clientGone:
		outcome, endReason = OutcomeClientDisconnect, EndClientDisconnected
	case upstreamBroke:
		outcome, endReason = OutcomeInterrupted, EndInterrupted
		e.Runtime.Failure(candidate.ChannelID, Limits2(candidate.Advanced, defaults), e.Now(), EndInterrupted)
	case !observation.Found:
		outcome = OutcomeUnbilled
	}
	record := &call.attempts[len(call.attempts)-1]
	record.EndReason = endReason
	record.DurationMS = millis(e.Now().Sub(result.started))
	record.ResponseByte = &written

	finish := CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: outcome, ModelID: call.modelID, ChannelID: candidate.ChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, Usage: observation.Usage,
		TTFTMS: &ttftFromStart, DurationMS: duration, ResponseBytes: written, UpstreamResponseID: observation.ResponseID,
		IntervalP50MS: observation.IntervalP50MS, IntervalP95MS: observation.IntervalP95MS,
	}
	if streamMillis := duration - ttftFromStart; observation.Found && observation.Usage.OutputTokens > 0 && streamMillis > 0 {
		speed := float64(observation.Usage.OutputTokens) * 1000 / float64(streamMillis)
		finish.TokensPerSecond = &speed
	}
	if observation.Found {
		self := candidate.OwnerID == call.key.OwnerID
		feeRate := defaults.FeeRateNano
		if self {
			feeRate = 0
		}
		priced, err := ledger.CalculatePriceV2(observation.Usage, model.BasePrices(), model.PriceTiers, call.started, candidate.MultiplierNano, feeRate)
		if err != nil {
			e.Logger.Error("gateway: pricing failed", "request_id", call.id, "error", err)
		} else {
			finish.Cost, finish.Fee = priced.Cost, priced.Fee
			finish.PriceSnapshot = priceSnapshot(model, priced, candidate.MultiplierNano, feeRate, observation.Usage, call.started)
			if !self && priced.Cost+priced.Fee > 0 {
				finish.Bill = &Billing{ConsumerID: call.key.OwnerID, ProviderID: candidate.OwnerID, Cost: priced.Cost, Fee: priced.Fee}
			}
		}
	}
	e.settle(call, candidate, finish, response.StatusCode, observation.Usage)
}

// settle writes the call result and, in the same database transaction, the
// ledger posting. If booking fails the call is still closed, with its cost
// recorded but no ledger transaction, which the reconciliation of Feature G
// can find and repair.
func (e *Engine) settle(call *callState, candidate Candidate, finish CallFinish, status int, usage ledger.Usage) {
	ctx := context.WithoutCancel(context.Background())
	outcome, err := e.Store.FinishCall(ctx, finish)
	if err != nil {
		e.Logger.Error("gateway: booking call failed", "request_id", call.id, "error", err)
		fallback := finish
		fallback.Bill = nil
		if _, retryErr := e.Store.FinishCall(ctx, fallback); retryErr != nil {
			e.Logger.Error("gateway: closing call failed", "request_id", call.id, "error", retryErr)
		}
	} else if outcome.Finished && finish.Bill != nil {
		now := e.Now()
		e.spend.Add(call.key.KeyID, finish.Cost+finish.Fee, now)
		var revenueCap money.Amount
		if candidate.Advanced.DailyRevenueCap != nil {
			revenueCap = *candidate.Advanced.DailyRevenueCap
		}
		e.Runtime.AddRevenue(candidate.ChannelID, finish.Cost, now, revenueCap)
	}
	e.log(call, finish.Outcome, status, finish.DurationMS, usage, finish.Cost, finish.Fee)
	event := Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: candidate.ChannelID, Model: call.modelID, Format: string(call.format), Outcome: finish.Outcome, StatusCode: status, DurationMS: finish.DurationMS}
	if finish.TTFTMS != nil {
		event.TTFTMS = *finish.TTFTMS
	}
	e.Events.Publish(event)
}

func priceSnapshot(model catalog.Model, priced ledger.PriceResult, multiplierNano, feeRateNano int64, usage ledger.Usage, at time.Time) []byte {
	prompt, _ := ledger.PromptSideTokens(usage)
	effective, seq := ledger.SelectPriceTier(model.BasePrices(), model.PriceTiers, prompt, at)
	multiplier := func(price money.Amount) string { return ledger.ScalePrice(price, multiplierNano).String() }
	price := func(amount money.Amount) string { return amount.String() }
	snapshot := map[string]any{
		"base_prices": map[string]string{
			"input": price(model.InputPrice), "output": price(model.OutputPrice),
			"cache_write": price(model.CacheWritePrice), "cache_read": price(model.CacheReadPrice),
		},
		"tier":          nil,
		"multiplier":    money.FromNano(multiplierNano).String(),
		"fee_rate_nano": feeRateNano,
		"prices": map[string]string{
			"input": multiplier(effective.InputPerMillion), "output": multiplier(effective.OutputPerMillion),
			"cache_write": multiplier(effective.CacheWritePerMillion), "cache_read": multiplier(effective.CacheReadPerMillion),
		},
	}
	if seq > 0 {
		snapshot["tier"] = map[string]any{"seq": seq, "name": model.PriceTiers[seq-1].Name}
	}
	encoded, _ := json.Marshal(snapshot)
	return encoded
}

func writeJSONBody(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}
