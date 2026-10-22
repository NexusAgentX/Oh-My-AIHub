package api

import (
	"sync"
	"time"
)

// rateLimits 集中登录与改密相关的限流器和口令哈希并发槽位。
type rateLimits struct {
	forumUploads         *loginLimiter
	forumUploadSlots     chan struct{}
	loginLimiter         *loginLimiter
	loginIPLimiter       *loginLimiter
	loginAttempts        *loginLimiter
	loginIPAttempts      *loginLimiter
	passwordChanges      *loginLimiter
	passwordChangeIPs    *loginLimiter
	channelProbes        *loginLimiter
	loginPasswordSlots   chan struct{}
	accountPasswordSlots chan struct{}
}

func newRateLimits() rateLimits {
	return rateLimits{
		forumUploads:         newLoginLimiter(30, time.Minute, 10_000),
		forumUploadSlots:     make(chan struct{}, 2),
		loginLimiter:         newLoginLimiter(8, 15*time.Minute, 10_000),
		loginIPLimiter:       newLoginLimiter(32, 15*time.Minute, 10_000),
		loginAttempts:        newLoginLimiter(20, 15*time.Minute, 10_000),
		loginIPAttempts:      newLoginLimiter(60, 15*time.Minute, 10_000),
		passwordChanges:      newLoginLimiter(8, 15*time.Minute, 10_000),
		passwordChangeIPs:    newLoginLimiter(32, 15*time.Minute, 10_000),
		channelProbes:        newLoginLimiter(10, time.Minute, 10_000),
		loginPasswordSlots:   make(chan struct{}, 2),
		accountPasswordSlots: make(chan struct{}, 2),
	}
}

func (l *rateLimits) allowLoginAttempt(ipKey, pairKey string) bool {
	return l.loginIPAttempts.take(ipKey) && l.loginAttempts.take(pairKey)
}

func (l *rateLimits) allowPasswordChangeAttempt(ipKey, accountID string) bool {
	return l.passwordChangeIPs.take(ipKey) && l.passwordChanges.take(accountID)
}

type loginAttempt struct {
	failures []time.Time
}

type loginLimiter struct {
	mu         sync.Mutex
	attempts   map[string]loginAttempt
	limit      int
	window     time.Duration
	maxEntries int
	now        func() time.Time
}

func newLoginLimiter(limit int, window time.Duration, maxEntries int) *loginLimiter {
	return &loginLimiter{
		attempts:   make(map[string]loginAttempt),
		limit:      limit,
		window:     window,
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.attempts) >= l.maxEntries {
		for existingKey, attempt := range l.attempts {
			if len(recentFailures(attempt.failures, now.Add(-l.window))) == 0 {
				delete(l.attempts, existingKey)
			}
		}
	}
	attempt, exists := l.attempts[key]
	if !exists && len(l.attempts) >= l.maxEntries {
		return false
	}
	attempt.failures = recentFailures(attempt.failures, now.Add(-l.window))
	if len(attempt.failures) == 0 {
		delete(l.attempts, key)
	} else {
		l.attempts[key] = attempt
	}
	return len(attempt.failures) < l.limit
}

func (l *loginLimiter) failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	attempt.failures = append(recentFailures(attempt.failures, l.now().Add(-l.window)), l.now())
	l.attempts[key] = attempt
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *loginLimiter) take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.attempts) >= l.maxEntries {
		for existingKey, attempt := range l.attempts {
			if len(recentFailures(attempt.failures, now.Add(-l.window))) == 0 {
				delete(l.attempts, existingKey)
			}
		}
	}
	attempt, exists := l.attempts[key]
	if !exists && len(l.attempts) >= l.maxEntries {
		return false
	}
	attempt.failures = recentFailures(attempt.failures, now.Add(-l.window))
	if len(attempt.failures) >= l.limit {
		l.attempts[key] = attempt
		return false
	}
	attempt.failures = append(attempt.failures, now)
	l.attempts[key] = attempt
	return true
}

func recentFailures(failures []time.Time, cutoff time.Time) []time.Time {
	firstRecent := 0
	for firstRecent < len(failures) && failures[firstRecent].Before(cutoff) {
		firstRecent++
	}
	return failures[firstRecent:]
}

func acquirePasswordSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}
