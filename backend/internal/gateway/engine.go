// Package gateway is the transparent API gateway (ADR-0035): it authenticates
// the caller, picks channels, forwards the request almost byte for byte,
// streams the response back unchanged while reading usage on the side, and
// books the charge after the call has ended.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const (
	maxRequestBytes  = 32 << 20
	maxFailureBody   = 1 << 20
	maxErrorMessage  = 4096
	chunkSize        = 32 << 10
	clientWriteLimit = 2 * time.Minute
	keyTouchEvery    = time.Minute

	// How often RunRefresh re-reads platform settings and republishes the 24h
	// channel stats. The settings interval is also how long an administrator's
	// change can take to reach the gateway. The stats are read from memory (see
	// StatsBook), so the interval costs no database work.
	settingsRefreshInterval = 5 * time.Second
	statsRefreshInterval    = 5 * time.Second
	// refreshTimeout abandons a refresh query that hangs.
	refreshTimeout = 30 * time.Second
)

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// Why an upstream attempt was cancelled (context.Cause of its context).
var (
	errTTFT         = errors.New("first byte timeout")
	errTotal        = errors.New("total timeout")
	errIdle         = errors.New("idle timeout")
	errCallDeadline = errors.New("call time limit")
)

// Dependencies wires the gateway.
type Dependencies struct {
	Store    Store
	Catalog  Models
	Settings *settings.Service
	Routing  routing.Store
	Keyring  *channel.Keyring
	Outbound channel.Outbound
	Events   Publisher
	Logger   *slog.Logger
	Runtime  *Runtime
	Now      func() time.Time
	// StatsBook holds the channel statistics (rolling 24-hour windows). Nil
	// means a new, empty one; tests pass their own to seed it.
	StatsBook *StatsBook
	// CallTimeLimit is the longest one call may run, every attempt included.
	// Zero means the production value, callTimeLimit; tests shorten it.
	CallTimeLimit time.Duration
	// DrainWindow is how long an answer is still read after its client has
	// left. Zero means the production value, drainWindow.
	DrainWindow time.Duration
}

// Engine serves the gateway endpoints.
type Engine struct {
	Dependencies
	spend    *SpendCache
	inflight *Inflight

	// Settings are published as an immutable snapshot by RunRefresh, so the
	// request path reads them without locking or waiting on the database. The
	// load mutex only serialises loads (the very first one included); readers
	// never take it once a snapshot exists. The channel stats are published the
	// same way, from memory (engine_stats.go).
	settingsSnap atomic.Pointer[settingsSnapshot]
	settingsLoad sync.Mutex
	statsSnap    atomic.Pointer[statsSnapshot]
	statsBuild   sync.Mutex
	// anomalyNotified is when each channel on each model last had its
	// anomalous_client_errors event; guarded by statsBuild.
	anomalyNotified map[pairKey]time.Time

	mutex   sync.Mutex // guards touched
	touched map[string]time.Time
}

// settingsSnapshot is one consistent read of the platform settings together
// with the outbound policy derived from them. It is never modified.
type settingsSnapshot struct {
	value    settings.Settings
	outbound channel.Outbound
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
	if dependencies.CallTimeLimit <= 0 {
		dependencies.CallTimeLimit = callTimeLimit
	}
	if dependencies.DrainWindow <= 0 {
		dependencies.DrainWindow = drainWindow
	}
	if dependencies.StatsBook == nil {
		dependencies.StatsBook = NewStatsBook()
	}
	return &Engine{Dependencies: dependencies, spend: NewSpendCache(), inflight: NewInflight(), touched: map[string]time.Time{}, anomalyNotified: map[pairKey]time.Time{}}
}

// Runtime exposes the in-process channel state for display.
func (e *Engine) State() *Runtime { return e.Runtime }

// Settings returns the current platform settings and the outbound policy
// derived from them. Once a snapshot exists it is a plain atomic read; the
// background refresh (RunRefresh) keeps it current. Before the first
// successful load (startup, or tests that never start RunRefresh) callers load
// it once, sharing one query: concurrent callers queue on settingsLoad and
// the first success serves them all.
func (e *Engine) Settings(ctx context.Context) (settings.Settings, channel.Outbound, error) {
	snapshot := e.settingsSnap.Load()
	if snapshot == nil {
		var err error
		if snapshot, err = e.loadSettings(ctx, false); err != nil {
			return settings.Settings{}, nil, err
		}
	}
	return snapshot.value, snapshot.outbound, nil
}

// loadSettings reads the settings from the store and publishes a new
// snapshot. Without force it returns the existing snapshot if another caller
// published one while this one waited. A failure leaves the snapshot as it is.
func (e *Engine) loadSettings(ctx context.Context, force bool) (*settingsSnapshot, error) {
	e.settingsLoad.Lock()
	defer e.settingsLoad.Unlock()
	if current := e.settingsSnap.Load(); current != nil && !force {
		return current, nil
	}
	value, err := e.Dependencies.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := e.Outbound.WithExtraBlockedHosts(value.ExtraBlockedHosts)
	if err != nil {
		return nil, err
	}
	snapshot := &settingsSnapshot{value: value, outbound: policy}
	e.settingsSnap.Store(snapshot)
	return snapshot, nil
}

// RunRefresh keeps the settings and channel-stats snapshots current until ctx
// ends: settings every few seconds so administrator changes reach the gateway
// quickly, the channel stats (counted in memory, no query) just as often. The
// two run independently so a slow settings query never delays the stats, and
// neither blocks requests. A failed settings refresh is logged and the previous
// snapshot stays in effect. The first refresh of each runs immediately.
func (e *Engine) RunRefresh(ctx context.Context) {
	e.runRefresh(ctx, settingsRefreshInterval, statsRefreshInterval)
}

func (e *Engine) runRefresh(ctx context.Context, settingsEvery, statsEvery time.Duration) {
	var wg sync.WaitGroup
	wg.Go(func() {
		e.refreshEvery(ctx, "settings", settingsEvery, func(ctx context.Context) error {
			_, err := e.loadSettings(ctx, true)
			return err
		})
	})
	wg.Go(func() {
		e.refreshEvery(ctx, "channel stats", statsEvery, func(context.Context) error {
			e.RefreshStats()
			return nil
		})
	})
	wg.Wait()
}

// refreshEvery runs load now and then once per interval.
func (e *Engine) refreshEvery(ctx context.Context, name string, interval time.Duration, load func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, refreshTimeout)
		err := load(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			e.Logger.Error("gateway: refresh failed, keeping previous value", "what", name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// EffectiveLimits resolves the effective limits of a channel against the defaults.
func EffectiveLimits(advanced channel.Advanced, defaults settings.Settings) Limits {
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
	id                   string
	started              time.Time
	key                  KeyAuth
	format               channel.Format
	stream               bool
	requested            string
	modelID              string
	tag                  string
	userAgent            string
	dialect              Dialect
	mode                 routing.Mode
	source               routing.Source
	attempts             []Attempt
	lastChannelID        string
	thinkingRequested    *bool
	requestedServiceTier string
	// usageInjected is set when the gateway turned include_usage on for an
	// OpenAI Chat stream whose client had not: the usage-only frame that the
	// upstream then adds is not for the client (see usageFrameFilter).
	usageInjected bool
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
		WriteError(w, DialectOf(r), http.StatusUnauthorized, "invalid_api_key", "缺少有效的 API Key", "")
		return KeyAuth{}, 0, false
	}
	key, err := e.Store.LookupKey(r.Context(), apikey.Hash(secret))
	if errors.Is(err, ErrKeyNotFound) {
		WriteError(w, DialectOf(r), http.StatusUnauthorized, "invalid_api_key", "API Key 无效", "")
		return KeyAuth{}, 0, false
	}
	if err != nil {
		e.Logger.Error("gateway: key lookup failed", "error", err)
		WriteError(w, DialectOf(r), http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
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

// ---- inference ----

// Serve handles one inference request of the given format. pathModel is the
// model named in a Gemini path.
func (e *Engine) Serve(w http.ResponseWriter, r *http.Request, format channel.Format, pathModel string, pathStream bool) {
	ctx := r.Context()
	started := e.Now()
	// One limit for the whole call, every attempt included (ADR-0035 rule 5).
	// It bounds the upstream exchanges only; settling the call runs on its own
	// context, so a call that hit the limit is still closed and billed. It does
	// not end when the client leaves: each attempt follows the client itself
	// (clientWatch), because an answer already under way is read to its end.
	callCtx, stopCall := context.WithTimeoutCause(context.WithoutCancel(ctx), e.CallTimeLimit, errCallDeadline)
	defer stopCall()
	key, location, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	call := &callState{started: started, key: key, format: format, tag: sanitizeTag(r.Header.Get("X-AIHub-Tag")), userAgent: truncate(r.UserAgent(), 512), dialect: DialectOf(r)}

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

	body, err := readRequestBody(w, r)
	if err != nil {
		call.requested = pathModel
		e.reject(w, call, OutcomeRejectedModel, http.StatusRequestEntityTooLarge, "request_too_large", "请求体过大或读取失败")
		return
	}

	// One scan of the body gives the facts of every format: the two members that
	// pick a price, and for the formats that name the model in the body, the
	// model, stream flag and sticky response too. A Gemini body is not
	// validated here (its model and stream flag are in the path), so a body the
	// scan rejects only means there are no pricing members to read.
	facts, scan, scanErr := ScanRequest(body)
	call.thinkingRequested, call.requestedServiceTier = facts.EnableThinking, facts.ServiceTier
	stickyResponseID := ""
	if format == channel.FormatGemini {
		call.requested, call.stream = pathModel, pathStream
	} else {
		call.requested, call.stream = facts.Model, facts.Stream
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
		WriteError(w, call.dialect, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
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
	if errors.Is(err, catalog.ErrNotFound) {
		// A name outside the catalog may be a channel's custom model
		// (ADR-0034); each candidate then carries its own prices.
		custom, customErr := e.Store.CustomModelOnline(ctx, call.modelID)
		if customErr != nil {
			e.internalError(w, call, customErr)
			return
		}
		if custom {
			model = catalog.Model{ID: call.modelID, DisplayName: call.modelID, PriceCurrency: ledger.CurrencyCNY, Enabled: true}
			err = nil
		}
	}
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

	// From here until the call is booked the account holds one of its slots
	// (ADR-0035 rule 2). The deferred release gives it back on every exit,
	// panics included, and only after the call has been settled.
	releaseInflight, admitted := e.inflight.Acquire(key.OwnerID, int(defaults.AccountMaxInflightCalls))
	if !admitted {
		e.finishRejected(w, call, OutcomeRejectedInflight, http.StatusTooManyRequests, "too_many_inflight_calls", "同时进行的调用过多，请稍后重试")
		return
	}
	defer releaseInflight()

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
	for index := range candidates {
		pricing := candidates[index].PricingModel(model)
		candidates[index].RateNano = ledger.ChannelRate(pricing.PriceCurrency, candidates[index].FXDiscount, defaults.USDCNYRateNano)
	}
	for _, candidate := range candidates {
		if limits := EffectiveLimits(candidate.Advanced, defaults); limits.DailyRevenueCap > 0 && e.Runtime.NeedsRevenue(candidate.ChannelID, started) {
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
	stats := e.statsView()
	ordered := Rank(RankInput{
		Mode: pref.Mode, Order: pref.Order, Excluded: pref.Excluded, Candidates: candidates, Stats: stats.channels,
		Demoted: stats.anomalies[call.modelID], ConsumerID: key.OwnerID, Sticky: sticky,
		Available: func(c Candidate) bool {
			state, _ := e.Runtime.Check(c.ChannelID, EffectiveLimits(c.Advanced, defaults), started)
			return state == StateAvailable
		},
	})
	if len(ordered) == 0 {
		e.finishRejected(w, call, OutcomeRejectedNoChannel, http.StatusServiceUnavailable, "no_channel_available", "当前没有可用的渠道")
		return
	}

	var last *upstreamFailure
	tried := 0
	// maxAttempts counts attempts actually made: a candidate whose slot was
	// taken since ranking is skipped and the next one fills in.
	for slot := range e.reservations(ordered, maxAttempts, defaults) {
		candidate, limits, release := slot.candidate, slot.limits, slot.release
		tried++
		call.lastChannelID = candidate.ChannelID
		result := e.attempt(callCtx, r, call, candidate, limits, defaults, policy, body, scan, location, ttftTimeout)
		call.attempts = append(call.attempts, result.record)
		e.publishAttempt(call, candidate, result.record)
		if result.response == nil {
			// An attempt with an answer to stream back is counted when
			// streamBack has settled how it ended.
			e.recordAttempt(call, candidate, result.record)
		}
		switch {
		case result.clientGone:
			release()
			e.finishAbandoned(call, "")
			return
		case result.expired:
			release()
			e.finishExpired(w, call)
			return
		case result.passthrough != nil:
			e.Runtime.Success(candidate.ChannelID) // an answering upstream is healthy
			e.forwardPassthrough(w, call, result.passthrough)
			release()
			return
		case result.response != nil:
			e.Runtime.Success(candidate.ChannelID)
			e.streamBack(w, call, candidate.PricingModel(model), candidate, result, defaults)
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
		WriteError(w, call.dialect, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作", "")
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
		WriteError(w, call.dialect, status, code, message, "")
		return
	}
	call.id = id
	e.log(call, outcome, status, 0, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Model: call.requested, Format: string(call.format), Outcome: outcome, StatusCode: status})
	WriteError(w, call.dialect, status, code, message, id)
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
	WriteError(w, call.dialect, status, code, message, call.id)
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

// finishExpired closes a call that ran out of time before any answer reached
// the client, and tells the client. Nothing was read, so there is nothing to
// bill; the channels are not blamed.
func (e *Engine) finishExpired(w http.ResponseWriter, call *callState) {
	duration := int(e.Now().Sub(call.started).Milliseconds())
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeInterrupted, ModelID: call.modelID, ChannelID: call.lastChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration,
	})
	if err != nil {
		e.Logger.Error("gateway: finishing expired call failed", "request_id", call.id, "error", err)
	}
	e.log(call, OutcomeInterrupted, http.StatusGatewayTimeout, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: call.lastChannelID, Model: call.modelID, Format: string(call.format), Outcome: OutcomeInterrupted, StatusCode: http.StatusGatewayTimeout, DurationMS: duration})
	WriteError(w, call.dialect, http.StatusGatewayTimeout, codeCallDeadline, messageCallDeadline, call.id)
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

// upstreamFailure is a complete upstream error answer, kept so that it can be
// handed to the client unchanged.
type upstreamFailure struct {
	status int
	header http.Header
	body   []byte
}

// write answers the client with the upstream status, headers and body as they
// came.
func (f *upstreamFailure) write(w http.ResponseWriter, requestID string) {
	for name, values := range f.header {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", requestID)
	w.WriteHeader(f.status)
	_, _ = w.Write(f.body)
}

// redactedCredential stands in for the channel's upstream key wherever an
// upstream error echoed it (ADR-0041).
const redactedCredential = "[redacted]"

// newUpstreamFailure keeps an upstream error answer to hand to the client: the
// status, headers and body as they came, except that the key this attempt used
// is replaced by redactedCredential in header values and body, because an
// upstream may echo the credential it was given, and the caller is not the
// channel's owner (ADR-0036 rule 1, ADR-0041). An answer that does not hold the
// key is passed on byte for byte. payload may hold up to MaxEncodedLen of the
// key beyond maxFailureBody: it is cut after the redaction, so that a key
// straddling the limit does not leave its beginning in the answer.
func newUpstreamFailure(response *http.Response, payload []byte, secret string) *upstreamFailure {
	header := ClientResponseHeaders(response.Header, response.Uncompressed)
	for _, values := range header {
		for index, value := range values {
			values[index], _ = secretguard.RedactString(value, secret, redactedCredential)
		}
	}
	body := payload
	if encoding := strings.TrimSpace(header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		// The transport left this body encoded, so the key cannot be looked for
		// in it. It is not passed on; status and the other headers are.
		body = nil
		header.Del("Content-Encoding")
	} else {
		body, _ = secretguard.Redact(body, secret, redactedCredential)
	}
	if len(body) > maxFailureBody {
		body = body[:maxFailureBody]
	}
	if !bytes.Equal(body, payload) {
		// The body is no longer what the upstream sent: what vouches for those
		// bytes no longer does.
		for _, name := range []string{"Content-Md5", "Digest", "Content-Digest", "Repr-Digest"} {
			header.Del(name)
		}
	}
	if length := header.Get("Content-Length"); length != "" && length != strconv.Itoa(len(body)) {
		header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return &upstreamFailure{status: response.StatusCode, header: header, body: body}
}

// attemptResult is the outcome of one attempt: a live response to stream back,
// a failure to fall back from, a passthrough (an upstream 4xx that is the
// request's own fault and ends the call unchanged), or a departed client.
type attemptResult struct {
	record   Attempt
	response *http.Response
	first    []byte
	firstErr error
	// chunk is the pooled buffer first lies in. streamBack takes it over: it
	// keeps reading into it once first has been delivered, and gives it back.
	chunk *[]byte
	// upstream is the context of the exchange; its cause says what cut it short.
	upstream context.Context
	cancel   context.CancelCauseFunc
	// watch follows the client: see clientWatch.
	watch *clientWatch
	// idle is the idle-timeout clock of a stream after its first byte, and
	// idleAfter its length: streamBack restarts it whenever data arrives.
	idle        *time.Timer
	idleAfter   time.Duration
	started     time.Time
	ttft        time.Duration
	secret      string
	failure     *upstreamFailure
	passthrough *upstreamFailure
	clientGone  bool
	// expired means the call ran out of its time limit during this attempt,
	// before any answer reached the client.
	expired bool
}

func millis(d time.Duration) *int {
	value := int(d.Milliseconds())
	return &value
}

func stringPointer(value string) *string { return &value }

// scrub removes the upstream credential from text that may echo it, for the
// error text that is logged and stored. (What goes back to the client is
// newUpstreamFailure's: it keeps invalid UTF-8, this does not.)
func scrub(text, secret string) string {
	text, _ = secretguard.RedactString(text, secret, "***")
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

// attempt makes one upstream request. ctx carries the time limit of the whole
// call; the request context r.Context() tells whether the client is still
// there. Every deadline of the exchange is a timer on the attempt's context
// (the outbound client has none of its own), so a stream that keeps producing
// output is never cut off by the total timeout, only by the call limit:
//
//   - stream: the first-byte timeout until the first byte, then the total
//     timeout as an idle timeout between two reads that returned data;
//   - non-streaming: the total timeout for the whole exchange.
func (e *Engine) attempt(ctx context.Context, r *http.Request, call *callState, candidate Candidate, limits Limits, defaults settings.Settings,
	policy channel.Outbound, body []byte, scan objectScan, location authLocation, ttft time.Duration) attemptResult {
	result := attemptResult{started: e.Now(), record: Attempt{Channel: &AttemptChannel{ID: candidate.ChannelID, Name: candidate.Name}}}
	// closeFailed writes the failure into the attempt record; fail also counts
	// it against the channel.
	closeFailed := func(endReason, code, message string, status *int) attemptResult {
		result.record.EndReason, result.record.ErrorCode = endReason, stringPointer(code)
		if message != "" {
			result.record.ErrorMessage = stringPointer(limitMessage(scrub(message, result.secret)))
		}
		result.record.StatusCode = status
		result.record.DurationMS = millis(e.Now().Sub(result.started))
		return result
	}
	fail := func(endReason, code, message string, status *int) attemptResult {
		closeFailed(endReason, code, message, status)
		e.Runtime.Failure(candidate.ChannelID, limits, e.Now(), endReason)
		return result
	}
	// expired ends the attempt because the call ran out of time. The channel is
	// not at fault, so it is not counted against it.
	expired := func(status *int) attemptResult {
		result.expired = true
		return closeFailed(EndInterrupted, codeCallDeadline, messageCallDeadline, status)
	}
	// abandoned ends the attempt because the client left before any output.
	abandoned := func() attemptResult {
		result.clientGone = true
		result.record.EndReason = EndClientDisconnected
		result.record.DurationMS = millis(e.Now().Sub(result.started))
		return result
	}
	if r.Context().Err() != nil {
		return abandoned()
	}
	if errors.Is(context.Cause(ctx), errCallDeadline) {
		return expired(nil)
	}

	secret, err := e.Keyring.OpenCredential(candidate.ChannelID, candidate.Credential)
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
	client, err := policy.GatewayClient(ctx, candidate.BaseURL)
	if err != nil {
		return fail(EndConnectError, "egress_blocked", "出站校验未通过", nil)
	}

	upstreamBody := body
	if call.format != channel.FormatGemini {
		upstreamBody, call.usageInjected = UpstreamBody(body, scan, candidate.UpstreamModel, call.format == channel.FormatOpenAIChat && call.stream)
	}
	target, err := UpstreamURL(candidate.BaseURL, r.URL.Path, r.URL.RawQuery, call.format, candidate.UpstreamModel, location, secret)
	if err != nil {
		return fail(EndConnectError, "bad_upstream_url", "无法构造上游地址", nil)
	}

	attemptCtx, cancelCause := context.WithCancelCause(ctx)
	watch := watchClient(r.Context(), cancelCause, e.DrainWindow)
	// arm cancels the attempt with the given cause once d has passed, unless the
	// timer is stopped first. A non-positive d arms nothing. The timers are only
	// touched on this goroutine; their callbacks use cancelCause alone.
	var timers []*time.Timer
	arm := func(d time.Duration, cause error) *time.Timer {
		if d <= 0 {
			return nil
		}
		timer := time.AfterFunc(d, func() { cancelCause(cause) })
		timers = append(timers, timer)
		return timer
	}
	cancel := func(cause error) {
		watch.stop()
		cancelCause(cause)
		for _, timer := range timers {
			timer.Stop()
		}
	}
	result.upstream, result.cancel, result.watch = attemptCtx, cancel, watch
	// Only a stream promises an early first byte; a non-streaming completion
	// answers when generation ends and is bounded by the total timeout alone,
	// which here runs from the request to the last byte.
	var firstByte *time.Timer
	if call.stream {
		firstByte = arm(ttft, errTTFT)
	} else {
		arm(total, errTotal)
	}
	stopFirstByte := func() {
		if firstByte != nil {
			firstByte.Stop()
		}
	}
	var connected time.Time
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected = e.Now() }}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(attemptCtx, trace), http.MethodPost, target, bytes.NewReader(upstreamBody))
	if err != nil {
		cancel(nil)
		return fail(EndConnectError, "bad_upstream_request", "无法构造上游请求", nil)
	}
	request.Header = UpstreamHeaders(r.Header, location, secret, candidate.Advanced)
	request.ContentLength = int64(len(upstreamBody))

	response, err := client.Do(request)
	if err != nil {
		stopFirstByte()
		cause := context.Cause(attemptCtx)
		cancel(nil)
		message := err.Error()
		var urlError *url.Error
		if errors.As(err, &urlError) {
			message = urlError.Err.Error()
		}
		switch {
		case r.Context().Err() != nil:
			return abandoned()
		case errors.Is(cause, errCallDeadline):
			return expired(nil)
		case errors.Is(cause, errTTFT):
			return fail(EndTimeoutTTFT, "timeout_ttft", "首字超时", nil)
		case errors.Is(cause, errTotal), isTimeout(err):
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
		chunk := getChunk()
		n, readErr := 0, error(nil)
		for n == 0 && readErr == nil {
			n, readErr = response.Body.Read(*chunk)
		}
		// An end of body with nothing in it counts as an answer only if the
		// attempt was not cancelled before it: a transport that is being torn
		// down can still deliver the last bytes of a peer that has seen it hang
		// up, and then reports a clean end (see answerEnded).
		if readErr != nil && n == 0 && !answerEnded(attemptCtx, readErr) {
			stopFirstByte()
			cause := context.Cause(attemptCtx)
			response.Body.Close()
			cancel(nil)
			putChunk(chunk)
			switch {
			case r.Context().Err() != nil:
				return abandoned()
			case errors.Is(cause, errCallDeadline):
				return expired(&status)
			case errors.Is(cause, errTTFT):
				return fail(EndTimeoutTTFT, "timeout_ttft", "首字超时", &status)
			case errors.Is(cause, errTotal):
				return fail(EndTimeoutTotal, "timeout_total", "上游响应超时", &status)
			default:
				return fail(EndInterrupted, "read_error", "读取上游响应失败", &status)
			}
		}
		stopFirstByte()
		if call.stream {
			// From the first byte on, the stream may stay silent for the total
			// timeout at most; streamBack restarts this clock on every read.
			result.idle, result.idleAfter = arm(total, errIdle), total
		}
		result.ttft = e.Now().Sub(result.started)
		result.record.TTFTMS = millis(result.ttft)
		result.response, result.chunk, result.first, result.firstErr = response, chunk, (*chunk)[:n], readErr
		return result
	}

	stopFirstByte()
	if call.stream {
		// The answer is an HTTP error: its body is bounded by the total timeout
		// like a non-streaming answer is.
		arm(total, errTotal)
	}
	payload, _ := io.ReadAll(io.LimitReader(response.Body, maxFailureBody+int64(secretguard.MaxEncodedLen(secret))))
	response.Body.Close()
	cause := context.Cause(attemptCtx)
	cancel(nil)
	if errors.Is(cause, errCallDeadline) {
		// The call ran out of time while this answer was still being read: what
		// came in is cut short and says nothing about the channel.
		return expired(&status)
	}
	answer := newUpstreamFailure(response, payload, secret)
	code := "upstream_" + http.StatusText(status)
	if !fallbackStatus(status) {
		// Any other 4xx is taken to be the request's own fault and goes back
		// as is, unless it says the model is missing: the model name sent
		// was the channel's own, so that is the channel's fault.
		if !ModelUnavailable(payload) {
			result.passthrough = answer
			result.record.EndReason = EndClientError
			result.record.DurationMS = millis(e.Now().Sub(result.started))
			return result
		}
		code = ErrorCodeModelUnavailable
	}
	result.failure = answer
	if pause, limited := RateLimitCooldown(status, response.Header, e.Now(), limits); limited {
		// The upstream said when to come back: rest the channel for that long
		// right away rather than counting towards the failure threshold.
		closeFailed(EndUpstreamError, code, string(payload), &status)
		e.Runtime.CooldownFor(candidate.ChannelID, pause, e.Now(), CooldownRateLimited)
		return result
	}
	return fail(EndUpstreamError, code, string(payload), &status)
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
		WriteError(w, call.dialect, http.StatusBadGateway, "upstream_unavailable", "所有渠道均不可用", call.id)
		return
	}
	last.write(w, call.id)
}

// forwardPassthrough returns an upstream 4xx unchanged, unbilled.
func (e *Engine) forwardPassthrough(w http.ResponseWriter, call *callState, answer *upstreamFailure) {
	answer.write(w, call.id)
	duration := int(e.Now().Sub(call.started).Milliseconds())
	_, err := e.Store.FinishCall(context.WithoutCancel(context.Background()), CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: OutcomeUpstreamFailed, ModelID: call.modelID, ChannelID: call.lastChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, DurationMS: duration, ResponseBytes: int64(len(answer.body)),
	})
	if err != nil {
		e.Logger.Error("gateway: finishing passthrough call failed", "request_id", call.id, "error", err)
	}
	e.log(call, OutcomeUpstreamFailed, answer.status, duration, ledger.Usage{}, 0, 0)
	e.Events.Publish(Event{Kind: EventCallFinished, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, KeyID: call.key.KeyID, ChannelID: call.lastChannelID, Model: call.modelID, Format: string(call.format), Outcome: OutcomeUpstreamFailed, StatusCode: answer.status, DurationMS: duration})
}

// interruption is why the upstream answer stopped after output had begun.
type interruption struct {
	code, message string
	// channelFault says whether the channel is blamed (counted as a failure).
	channelFault bool
}

const (
	codeCallDeadline    = "call_deadline"
	messageCallDeadline = "调用已达到时间上限"
)

// interruptionOf classifies the end of a body read that failed with the given
// cause of the attempt's context. The call limit is nobody's fault; an idle or
// total timeout and a broken connection are the channel's.
func interruptionOf(cause error) *interruption {
	switch {
	case errors.Is(cause, errCallDeadline):
		return &interruption{code: codeCallDeadline, message: messageCallDeadline}
	case errors.Is(cause, errIdle):
		return &interruption{code: "timeout_idle", message: "流式响应空闲超时", channelFault: true}
	case errors.Is(cause, errTotal):
		return &interruption{code: "timeout_total", message: "上游响应超时", channelFault: true}
	}
	return &interruption{code: "read_error", message: "读取上游响应失败", channelFault: true}
}

// answerEnded reports whether readErr is the clean end of an upstream answer:
// io.EOF, and the attempt was not already cancelled when it came. Cancelling an
// attempt (call time limit, idle or drain timeout, client gone) makes the
// transport close its connection, but it can close it a moment too late:
// closing a TLS connection first sends close_notify, the upstream sees it,
// finishes its chunked body at once, and the reader that is blocked on the
// connection gets those last bytes and a plain io.EOF instead of an error. So
// an EOF after a cancellation proves nothing about the answer being complete;
// the cause of the cancellation says what stopped it.
func answerEnded(attempt context.Context, readErr error) bool {
	return errors.Is(readErr, io.EOF) && context.Cause(attempt) == nil
}

// streamBack writes the upstream answer to the client chunk by chunk while the
// observer reads usage, then settles the call. A client that leaves once the
// writing has begun does not stop the reading (ADR-0035 rule 6): the rest of
// the answer goes to the observer alone until it ends, the drain window is
// over, or an idle or call time limit hits, so the usage at its end is billed.
func (e *Engine) streamBack(w http.ResponseWriter, call *callState, model catalog.Model, candidate Candidate, result attemptResult, defaults settings.Settings) {
	response := result.response
	// The buffer of the first read serves the rest of the answer too, once its
	// first chunk has been delivered (everything that takes a chunk copies it).
	buffer := result.chunk
	if buffer == nil {
		buffer = getChunk()
	}
	defer putChunk(buffer)
	defer response.Body.Close()
	defer result.cancel(nil)

	watch := result.watch
	if !watch.commit() {
		// The client left between the first byte and the first write: nothing
		// was delivered and the attempt has been cancelled, so nothing is billed.
		record := &call.attempts[len(call.attempts)-1]
		record.EndReason = EndClientDisconnected
		record.DurationMS = millis(e.Now().Sub(result.started))
		e.finishAbandoned(call, candidate.ChannelID)
		return
	}
	controller := http.NewResponseController(w)
	for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
		w.Header()[name] = values
	}
	w.Header().Set("X-AIHub-Request-Id", call.id)
	_ = controller.SetWriteDeadline(e.Now().Add(clientWriteLimit))
	w.WriteHeader(response.StatusCode)

	observer := NewObserver(call.format, call.stream, response.Header.Get("Content-Type"))
	observer.detail.RequestedServiceTier = call.requestedServiceTier
	if call.format == channel.FormatOpenAIChat && call.thinkingRequested != nil && !*call.thinkingRequested {
		observer.qwenObserved = true
	}
	var written int64
	// undelivered is set when a part of the answer did not reach the client.
	undelivered := false
	send := func(chunk []byte) {
		if len(chunk) == 0 {
			return
		}
		if watch.isGone() {
			undelivered = true
			return
		}
		_ = controller.SetWriteDeadline(e.Now().Add(clientWriteLimit))
		if _, err := w.Write(chunk); err != nil {
			undelivered = true
			watch.left() // a client that cannot be written to has left
			return
		}
		_ = controller.Flush()
		written += int64(len(chunk))
	}
	// When the gateway turned include_usage on for a client that had not asked
	// for it, the usage-only frame the upstream adds is kept from the client
	// (ADR-0035 rule 6); everything else reaches it unchanged.
	var strip *usageFrameFilter
	if call.usageInjected && call.format == channel.FormatOpenAIChat && call.stream && observer.sse {
		strip = &usageFrameFilter{}
	}
	deliver := func(chunk []byte) {
		if len(chunk) == 0 {
			return
		}
		// The observer sees every byte the upstream sends, wanted by the client
		// or not: that is what makes the usage at the end billable.
		observer.Write(chunk, e.Now())
		if strip != nil {
			chunk = strip.Filter(chunk)
		}
		send(chunk)
	}
	deliver(result.first)
	readErr := result.firstErr
	for readErr == nil {
		var n int
		n, readErr = response.Body.Read(*buffer)
		if n > 0 && result.idle != nil {
			result.idle.Reset(result.idleAfter) // data arrived: the stream is not idle
		}
		deliver((*buffer)[:n])
	}
	if strip != nil {
		send(strip.Flush()) // what was held back for an event that never ended
	}
	// Whether the answer ran to its end, and whether the client was gone while
	// it did. A client that closes its side after receiving everything, before
	// the upstream has closed its own, is not a disconnect.
	ended := answerEnded(result.upstream, readErr)
	left := watch.isGone()
	var stopped *interruption
	if !ended && !left {
		stopped = interruptionOf(context.Cause(result.upstream))
	}
	clientGone := undelivered || (left && !ended)
	// The exchange is over: stop its clocks before settling.
	result.cancel(nil)
	observation := observer.Finish()

	duration := int(e.Now().Sub(call.started).Milliseconds())
	ttftFromStart := int(result.started.Sub(call.started).Milliseconds()) + int(result.ttft.Milliseconds())
	outcome := OutcomeSucceeded
	endReason := EndCompleted
	switch {
	case clientGone:
		// Whatever stopped the reading after the client left is no fault of the
		// channel, and neither is the client's leaving.
		outcome, endReason = OutcomeClientDisconnect, EndClientDisconnected
	case stopped != nil:
		outcome, endReason = OutcomeInterrupted, EndInterrupted
		if stopped.channelFault {
			e.Runtime.Failure(candidate.ChannelID, EffectiveLimits(candidate.Advanced, defaults), e.Now(), EndInterrupted)
		}
	case !observation.Found:
		outcome = OutcomeUnbilled
	}
	record := &call.attempts[len(call.attempts)-1]
	record.EndReason = endReason
	record.DurationMS = millis(e.Now().Sub(result.started))
	record.ResponseByte = &written
	if stopped != nil {
		record.ErrorCode, record.ErrorMessage = stringPointer(stopped.code), stringPointer(stopped.message)
	}
	e.recordAttempt(call, candidate, *record)

	finish := CallFinish{
		ID: call.id, AccountID: call.key.OwnerID, Outcome: outcome, ModelID: call.modelID, ChannelID: candidate.ChannelID,
		RoutingMode: string(call.mode), RoutingSource: string(call.source), Attempts: call.attempts, Usage: observation.Usage,
		TTFTMS: &ttftFromStart, DurationMS: duration, ResponseBytes: written, UpstreamResponseID: observation.ResponseID,
		IntervalP50MS: observation.IntervalP50MS, IntervalP95MS: observation.IntervalP95MS,
	}
	speedMillis := duration
	if call.stream {
		speedMillis -= ttftFromStart
	}
	if observation.Found && observation.Usage.OutputTokens > 0 && speedMillis > 0 {
		speed := float64(observation.Usage.OutputTokens) * 1000 / float64(speedMillis)
		finish.TokensPerSecond = &speed
	}
	if observation.Found {
		self := candidate.OwnerID == call.key.OwnerID
		feeRate := defaults.FeeRateNano
		if self {
			feeRate = 0
		}
		priced, err := ledger.CalculatePriceAt(observation.Usage, model.BasePrices(), model.PriceTiers, call.started, candidate.MultiplierNano, candidate.RateNano, feeRate)
		if err != nil {
			e.Logger.Error("gateway: pricing failed", "request_id", call.id, "error", err)
		} else {
			finish.Cost, finish.Fee = priced.Cost, priced.Fee
			finish.PriceSnapshot = priceSnapshot(model, priced, candidate, defaults.USDCNYRateNano, feeRate, observation.Usage, call.started)
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
		e.Events.Publish(Event{Kind: EventSettlementFailed, At: e.Now(), CallID: call.id, AccountID: call.key.OwnerID, ChannelID: candidate.ChannelID, Model: call.modelID, Format: string(call.format)})
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

func priceSnapshot(model catalog.Model, priced ledger.PriceResult, candidate Candidate, usdRateNano, feeRateNano int64, usage ledger.Usage, at time.Time) []byte {
	prompt, _ := ledger.PromptSideTokens(usage)
	effective, seq := ledger.SelectPriceTier(model.BasePrices(), model.PriceTiers, prompt, at, usage.Detail)
	multiplierNano := candidate.MultiplierNano
	multiplier := func(price money.Amount) string {
		return ledger.ScalePriceAt(price, multiplierNano, candidate.RateNano).String()
	}
	price := func(amount money.Amount) string { return amount.String() }
	snapshot := map[string]any{
		"base_prices": map[string]string{
			"input": price(model.InputPrice), "output": price(model.OutputPrice),
			"cache_write": price(model.CacheWritePrice), "cache_read": price(model.CacheReadPrice),
		},
		"detail":               usage.Detail,
		"token_prices":         map[string]string{},
		"base_token_prices":    map[string]string{},
		"selected_prices":      map[string]any{"input": effective.InputPerMillion.String(), "output": effective.OutputPerMillion.String(), "cache_read": effective.CacheReadPerMillion.String(), "cache_write": effective.CacheWritePerMillion.String(), "token_prices": map[string]string{}},
		"tier":                 nil,
		"multiplier":           money.FromNano(multiplierNano).String(),
		"currency":             model.PriceCurrency,
		"fx_discount":          model.PriceCurrency == ledger.CurrencyUSD && candidate.FXDiscount,
		"effective_multiplier": money.FromNano(ledger.EffectiveMultiplier(multiplierNano, candidate.RateNano, model.Rate(usdRateNano))).String(),
		"fee_rate_nano":        feeRateNano,
		"custom":               candidate.Custom != nil,
		"prices": map[string]string{
			"input": multiplier(effective.InputPerMillion), "output": multiplier(effective.OutputPerMillion),
			"cache_write": multiplier(effective.CacheWritePerMillion), "cache_read": multiplier(effective.CacheReadPerMillion),
		},
	}
	for key, value := range effective.TokenPrices {
		snapshot["token_prices"].(map[string]string)[key] = multiplier(value)
		snapshot["selected_prices"].(map[string]any)["token_prices"].(map[string]string)[key] = value.String()
	}
	for key, value := range model.TokenPrices {
		snapshot["base_token_prices"].(map[string]string)[key] = value.String()
	}
	if usage.Detail == nil {
		snapshot["detail"] = &ledger.UsageDetail{Notes: []string{"未提供可靠细分，按通用价格结算"}}
	} else if len(usage.Detail.Tokens) == 0 {
		detail := *usage.Detail
		detail.Notes = append(append([]string(nil), detail.Notes...), "未提供可靠细分，按通用价格结算")
		snapshot["detail"] = &detail
	}
	if seq > 0 {
		snapshot["tier"] = map[string]any{"seq": seq, "name": model.PriceTiers[seq-1].Name}
	}
	// Base and selected prices stay in the model's currency; prices are points.
	snapshot["usd_cny_rate"] = nil
	if model.PriceCurrency == ledger.CurrencyUSD {
		snapshot["usd_cny_rate"] = money.FromNano(usdRateNano).String()
	}
	detail := ledger.UsageDetail{}
	if saved, ok := snapshot["detail"].(*ledger.UsageDetail); ok && saved != nil {
		detail = *saved
		detail.Notes = append([]string(nil), saved.Notes...)
	}
	remaining := []int64{usage.InputTokens, usage.OutputTokens, usage.CacheWriteTokens, usage.CacheReadTokens}
	for key, n := range detail.Tokens {
		if bucket := ledger.TokenBucket(key); bucket >= 0 {
			remaining[bucket] -= n
		}
	}
	for i, n := range remaining {
		if n > 0 {
			detail.Notes = append(detail.Notes, fmt.Sprintf("%s通用余量 %d tokens（无可靠细分，按本组通用价）", []string{"输入", "输出", "缓存写", "缓存读"}[i], n))
		}
	}
	snapshot["detail"] = &detail
	encoded, _ := json.Marshal(snapshot)
	return encoded
}

func writeJSONBody(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}
