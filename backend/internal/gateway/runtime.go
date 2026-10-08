package gateway

import (
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// ChannelState explains why a channel can or cannot take a request now.
type ChannelState string

const (
	StateAvailable ChannelState = "available"
	StateCooldown  ChannelState = "cooldown"
	StateLimited   ChannelState = "limited"
)

// Limits are the effective per-channel limits (zero means unlimited).
type Limits struct {
	Concurrency     int
	RPM             int
	DailyRevenueCap money.Amount
	CooldownAfter   int
	CooldownFor     time.Duration
}

type channelRuntime struct {
	inflight      int
	requests      []time.Time // starts within the last minute
	failures      int
	cooldownUntil time.Time
	revenueDay    time.Time
	revenue       money.Amount
	revenueLoaded bool
	limitNoticed  bool
}

// Runtime keeps the in-process, single-instance state of channels:
// concurrency, requests per minute, consecutive failures and cooldowns, and the
// cached revenue of the current day. Nothing here survives a restart.
type Runtime struct {
	mutex    sync.Mutex
	channels map[string]*channelRuntime
	notify   func(channelID, kind, reason string)
}

// NewRuntime creates the state; notify is called (without holding any lock)
// when a channel enters or leaves cooldown or reaches a limit.
func NewRuntime(notify func(channelID, kind, reason string)) *Runtime {
	if notify == nil {
		notify = func(string, string, string) {}
	}
	return &Runtime{channels: map[string]*channelRuntime{}, notify: notify}
}

func (r *Runtime) state(id string) *channelRuntime {
	state, ok := r.channels[id]
	if !ok {
		state = &channelRuntime{}
		r.channels[id] = state
	}
	return state
}

func (s *channelRuntime) prune(now time.Time) {
	cutoff := now.Add(-time.Minute)
	index := 0
	for index < len(s.requests) && !s.requests[index].After(cutoff) {
		index++
	}
	s.requests = s.requests[index:]
}

// rollRevenue resets the cached revenue when the accounting day changes.
func (s *channelRuntime) rollRevenue(now time.Time) {
	day := localtime.DayStart(now)
	if !s.revenueDay.Equal(day) {
		s.revenueDay, s.revenue, s.revenueLoaded = day, 0, false
	}
}

// Check reports whether the channel can take a request now without changing
// anything except expiring a finished cooldown.
func (r *Runtime) Check(id string, limits Limits, now time.Time) (ChannelState, string) {
	var ended bool
	r.mutex.Lock()
	state := r.state(id)
	if !state.cooldownUntil.IsZero() && !now.Before(state.cooldownUntil) {
		state.cooldownUntil, ended = time.Time{}, true
	}
	result, reason := r.check(state, limits, now)
	r.mutex.Unlock()
	if ended {
		r.notify(id, channel.EventCooldownEnded, "")
	}
	return result, reason
}

func (r *Runtime) check(state *channelRuntime, limits Limits, now time.Time) (ChannelState, string) {
	if now.Before(state.cooldownUntil) {
		return StateCooldown, "cooldown"
	}
	state.prune(now)
	if limits.Concurrency > 0 && state.inflight >= limits.Concurrency {
		return StateLimited, "concurrency"
	}
	if limits.RPM > 0 && len(state.requests) >= limits.RPM {
		return StateLimited, "rpm"
	}
	state.rollRevenue(now)
	if limits.DailyRevenueCap > 0 && state.revenueLoaded && state.revenue >= limits.DailyRevenueCap {
		return StateLimited, "daily_revenue"
	}
	return StateAvailable, ""
}

// NeedsRevenue reports whether the day's revenue must be loaded before the
// daily cap can be judged.
func (r *Runtime) NeedsRevenue(id string, now time.Time) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	state := r.state(id)
	state.rollRevenue(now)
	return !state.revenueLoaded
}

// SetRevenue seeds the cached revenue of the current day from the database.
func (r *Runtime) SetRevenue(id string, revenue money.Amount, now time.Time) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	state := r.state(id)
	state.rollRevenue(now)
	if !state.revenueLoaded {
		state.revenue, state.revenueLoaded = revenue, true
	}
}

// AddRevenue accounts a billed call to the channel's day.
func (r *Runtime) AddRevenue(id string, amount money.Amount, now time.Time, cap money.Amount) {
	r.mutex.Lock()
	state := r.state(id)
	state.rollRevenue(now)
	crossed := false
	if state.revenueLoaded {
		state.revenue += amount
		crossed = cap > 0 && state.revenue >= cap && !state.limitNoticed
		if crossed {
			state.limitNoticed = true
		}
	}
	r.mutex.Unlock()
	if crossed {
		r.notify(id, channel.EventLimitReached, "daily_revenue")
	}
}

// Begin reserves a slot for one attempt. The returned release function must
// be called when the attempt ends.
func (r *Runtime) Begin(id string, limits Limits, now time.Time) (release func(), ok bool, reason string) {
	r.mutex.Lock()
	state := r.state(id)
	result, why := r.check(state, limits, now)
	if result != StateAvailable {
		r.mutex.Unlock()
		if result == StateLimited && why != "daily_revenue" {
			r.noticeLimit(id, why)
		}
		return nil, false, why
	}
	state.inflight++
	state.requests = append(state.requests, now)
	r.mutex.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mutex.Lock()
			state.inflight--
			r.mutex.Unlock()
		})
	}, true, ""
}

func (r *Runtime) noticeLimit(id, reason string) {
	r.mutex.Lock()
	state := r.state(id)
	first := !state.limitNoticed
	state.limitNoticed = true
	r.mutex.Unlock()
	if first {
		r.notify(id, channel.EventLimitReached, reason)
	}
}

// ClearLimitNotice allows the next limit event once the channel has capacity.
func (r *Runtime) ClearLimitNotice(id string) {
	r.mutex.Lock()
	r.state(id).limitNoticed = false
	r.mutex.Unlock()
}

// Success clears the consecutive failure count.
func (r *Runtime) Success(id string) {
	r.mutex.Lock()
	r.state(id).failures = 0
	r.mutex.Unlock()
}

// Failure counts one failed attempt and starts a cooldown when the threshold
// of consecutive failures is reached. It reports whether a cooldown began.
func (r *Runtime) Failure(id string, limits Limits, now time.Time, reason string) bool {
	r.mutex.Lock()
	state := r.state(id)
	state.failures++
	started := false
	if limits.CooldownAfter > 0 && state.failures >= limits.CooldownAfter && !now.Before(state.cooldownUntil) {
		state.cooldownUntil = now.Add(limits.CooldownFor)
		state.failures = 0
		started = true
	}
	r.mutex.Unlock()
	if started {
		r.notify(id, channel.EventCooldownStarted, reason)
	}
	return started
}

// CooldownRemaining returns the seconds left of a cooldown, or 0.
func (r *Runtime) CooldownRemaining(id string, now time.Time) time.Duration {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	state, ok := r.channels[id]
	if !ok || !now.Before(state.cooldownUntil) {
		return 0
	}
	return state.cooldownUntil.Sub(now)
}

// CooldownUntil returns when the cooldown of a channel ends, or the zero time.
func (r *Runtime) CooldownUntil(id string, now time.Time) time.Time {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	state, ok := r.channels[id]
	if !ok || !now.Before(state.cooldownUntil) {
		return time.Time{}
	}
	return state.cooldownUntil
}

// Describe summarizes the channel for display.
func (r *Runtime) Describe(id string, limits Limits, now time.Time) (ChannelState, time.Duration) {
	state, _ := r.Check(id, limits, now)
	if state == StateCooldown {
		return state, r.CooldownRemaining(id, now)
	}
	return state, 0
}

// SpendCache holds the spend of API keys inside their budget windows. It is
// loaded from the database on first use and then updated after every billed
// call.
type SpendCache struct {
	mutex sync.Mutex
	keys  map[string]*keySpend
}

type keySpend struct {
	day, month       time.Time
	today, thisMonth money.Amount
	total            money.Amount
	loaded           bool
}

func NewSpendCache() *SpendCache { return &SpendCache{keys: map[string]*keySpend{}} }

type Spend struct{ Today, Month, Total money.Amount }

// Get returns the cached spend, calling load once per window change.
func (c *SpendCache) Get(keyID string, now time.Time, load func() (Spend, error)) (Spend, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	entry, ok := c.keys[keyID]
	if !ok {
		entry = &keySpend{}
		c.keys[keyID] = entry
	}
	day, month := localtime.DayStart(now), localtime.MonthStart(now)
	if !entry.loaded || !entry.day.Equal(day) || !entry.month.Equal(month) {
		spend, err := load()
		if err != nil {
			return Spend{}, err
		}
		*entry = keySpend{day: day, month: month, today: spend.Today, thisMonth: spend.Month, total: spend.Total, loaded: true}
	}
	return Spend{Today: entry.today, Month: entry.thisMonth, Total: entry.total}, nil
}

// Add records a charge on a loaded key.
func (c *SpendCache) Add(keyID string, amount money.Amount, now time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	entry, ok := c.keys[keyID]
	if !ok || !entry.loaded || !entry.day.Equal(localtime.DayStart(now)) || !entry.month.Equal(localtime.MonthStart(now)) {
		return
	}
	entry.today += amount
	entry.thisMonth += amount
	entry.total += amount
}
