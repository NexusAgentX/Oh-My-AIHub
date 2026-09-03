package ledger

import (
	"time"

	// Embed the IANA timezone database so tier windows keep working in slim
	// production containers that do not ship /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// PriceTier is one conditional price set of a model. A tier participates in
// pricing only when every present predicate matches the call facts; the first
// matching tier in stored order wins and a model without conditional tiers
// always prices with its unconditional catalog prices.
//
// Predicates are intentionally two-dimensional for now: prompt-side token
// volume (MinPromptTokens inclusive, MaxPromptTokens exclusive) and a weekly
// time window expressed in the tier's IANA timezone (StartMinute inclusive,
// EndMinute exclusive; Start > End crosses midnight and the window belongs to
// its start day; Weekdays constrains that start day, empty means every day).
type PriceTier struct {
	Name            string
	MinPromptTokens *int64
	MaxPromptTokens *int64
	Timezone        string
	Weekdays        []int
	StartMinute     *int16
	EndMinute       *int16
	InputPrice      money.Amount
	OutputPrice     money.Amount
	CacheWritePrice money.Amount
	CacheReadPrice  money.Amount
}

// HasPredicate reports whether the tier constrains anything at all. Tiers
// without predicates are meaningless next to the default prices and are
// rejected by catalog validation.
func (t PriceTier) HasPredicate() bool {
	return t.MinPromptTokens != nil || t.MaxPromptTokens != nil || t.StartMinute != nil || len(t.Weekdays) > 0
}

// Matches evaluates every predicate of the tier against the prompt-side token
// count and the call start time. An invalid timezone fails closed.
func (t PriceTier) Matches(promptTokens int64, at time.Time) bool {
	if t.MinPromptTokens != nil && promptTokens < *t.MinPromptTokens {
		return false
	}
	if t.MaxPromptTokens != nil && promptTokens >= *t.MaxPromptTokens {
		return false
	}
	hasWindow := t.StartMinute != nil
	hasDays := len(t.Weekdays) > 0
	if !hasWindow && !hasDays {
		return true
	}
	location, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return false
	}
	local := at.In(location)
	weekday := isoWeekday(local)
	if !hasWindow {
		return containsWeekday(t.Weekdays, weekday)
	}
	start, end := int(*t.StartMinute), int(*t.EndMinute)
	minute := local.Hour()*60 + local.Minute()
	if start < end {
		return minute >= start && minute < end && (!hasDays || containsWeekday(t.Weekdays, weekday))
	}
	// A window that crosses midnight belongs to its start day.
	if minute >= start {
		return !hasDays || containsWeekday(t.Weekdays, weekday)
	}
	if minute < end {
		return !hasDays || containsWeekday(t.Weekdays, previousWeekday(weekday))
	}
	return false
}

// Prices projects the tier into the standard four-bucket price set used by
// the settlement formula.
func (t PriceTier) Prices() OfficialPricesV1 {
	return OfficialPricesV1{
		InputPerMillion:      t.InputPrice,
		OutputPerMillion:     t.OutputPrice,
		CacheWritePerMillion: t.CacheWritePrice,
		CacheReadPerMillion:  t.CacheReadPrice,
	}
}

// SelectPriceTier resolves the effective price set for one call: the first
// conditional tier (in slice order, matching tier sequence numbers starting
// at 1) whose predicates all match, or the model's unconditional prices when
// none does. The second return value is the matched tier sequence, 0 meaning
// the default prices.
func SelectPriceTier(defaultPrices OfficialPricesV1, tiers []PriceTier, promptTokens int64, at time.Time) (OfficialPricesV1, int) {
	for index, tier := range tiers {
		if tier.Matches(promptTokens, at) {
			return tier.Prices(), index + 1
		}
	}
	return defaultPrices, 0
}

// PromptSideTokens is the tier-decision volume: everything the request sent
// on the prompt side, regardless of whether the upstream billed it as fresh
// input, cache write, or cache read. All protocol adapters normalize usage
// into these three disjoint buckets, so the sum is protocol-consistent.
func PromptSideTokens(usage UsageV1) (int64, error) {
	total := usage.InputTokens + usage.CacheWriteTokens + usage.CacheReadTokens
	if usage.InputTokens < 0 || usage.CacheWriteTokens < 0 || usage.CacheReadTokens < 0 || total < 0 {
		return 0, ErrInvalidInput
	}
	return total, nil
}

func isoWeekday(at time.Time) int {
	return (int(at.Weekday())+6)%7 + 1
}

func previousWeekday(weekday int) int {
	if weekday == 1 {
		return 7
	}
	return weekday - 1
}

func containsWeekday(weekdays []int, weekday int) bool {
	for _, value := range weekdays {
		if value == weekday {
			return true
		}
	}
	return false
}
