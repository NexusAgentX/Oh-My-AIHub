package gateway

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CooldownRateLimited is the reason of a cooldown_started event that began
// because the upstream said when to come back.
const CooldownRateLimited = "rate_limited"

// minRateLimitCooldown is the shortest cooldown an upstream hint can cause,
// even for "Retry-After: 0" or a date in the past.
const minRateLimitCooldown = time.Second

// RateLimitCooldown reports how long a channel must rest after an upstream
// answer, when that answer is a 429 that says when to retry. The wait comes
// from retry-after-ms or Retry-After (seconds or an HTTP date) and is limited
// to [1s, limits.CooldownFor], the cooldown the channel would otherwise get.
//
// Any other status, a missing or unparsable hint, and a channel without a
// configured cooldown report false: those failures are only counted.
func RateLimitCooldown(status int, header http.Header, now time.Time, limits Limits) (time.Duration, bool) {
	if status != http.StatusTooManyRequests || limits.CooldownFor < minRateLimitCooldown {
		return 0, false
	}
	wait, ok := retryAfter(header, now)
	if !ok {
		return 0, false
	}
	return min(max(wait, minRateLimitCooldown), limits.CooldownFor), true
}

// retryAfter reads the wait an upstream asks for. retry-after-ms is more
// precise than Retry-After and wins when it is valid; a negative result means
// the HTTP date lies in the past.
func retryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	if wait, ok := parseWait(header.Get("Retry-After-Ms"), time.Millisecond); ok {
		return wait, true
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if wait, ok := parseWait(value, time.Second); ok {
		return wait, true
	}
	if at, err := http.ParseTime(value); err == nil {
		return at.Sub(now), true
	}
	return 0, false
}

// parseWait parses a non-negative decimal number of units (no sign, no
// exponent). It saturates instead of overflowing.
func parseWait(value string, unit time.Duration) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	digits, dots := 0, 0
	for _, char := range value {
		switch {
		case char >= '0' && char <= '9':
			digits++
		case char == '.':
			dots++
		default:
			return 0, false
		}
	}
	if digits == 0 || dots > 1 {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	nanos := number * float64(unit)
	if nanos >= math.MaxInt64 {
		return math.MaxInt64, true
	}
	return time.Duration(nanos), true
}
