package gateway

import (
	"iter"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// reservation is a ranked candidate whose runtime slot is held; the caller
// must call release when the attempt ends.
type reservation struct {
	candidate Candidate
	limits    Limits
	release   func()
}

// reservations walks the ranked candidates in order and yields each one that
// Runtime.Begin lets through, stopping after maxAttempts of them. A candidate
// Begin refuses (a concurrent request took its last slot or request budget
// since ranking) is skipped and does not use up an attempt, so later
// candidates fill in. The caller releases each reservation before asking for
// the next one.
func (e *Engine) reservations(ordered []Candidate, maxAttempts int, defaults settings.Settings) iter.Seq[reservation] {
	return func(yield func(reservation) bool) {
		granted := 0
		for _, candidate := range ordered {
			if granted >= maxAttempts {
				return
			}
			limits := EffectiveLimits(candidate.Advanced, defaults)
			release, ok, _ := e.Runtime.Begin(candidate.ChannelID, limits, e.Now())
			if !ok {
				continue
			}
			granted++
			if !yield(reservation{candidate: candidate, limits: limits, release: release}) {
				return
			}
		}
	}
}
