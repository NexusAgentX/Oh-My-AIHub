package gateway

import (
	"math"
	"slices"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// minSamples is how many 24h attempts a channel needs before its own success
// rate counts; fewer are rated with the platform median.
const minSamples = 20

// RankInput describes one routing decision.
type RankInput struct {
	Mode       routing.Mode
	Order      []string // manual order
	Excluded   []string
	Candidates []Candidate
	Stats      map[string]ChannelStats
	ConsumerID string
	// Sticky is the channel that served the previous Responses turn, if any.
	Sticky string
	// Available reports whether a candidate can take a request now
	// (cooldown, concurrency, RPM, daily revenue cap).
	Available func(Candidate) bool
}

// price orders candidates by what they charge: the channel multiplier applied
// to the model's current tier price (identical for every candidate of one
// model, so the multiplier alone orders them). Channels of the caller cost 0.
func price(candidate Candidate, consumerID string) float64 {
	if candidate.OwnerID == consumerID {
		return 0
	}
	return float64(candidate.MultiplierNano)
}

func medianOf(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sort.Float64s(values)
	return values[len(values)/2], true
}

// Rank filters and orders the candidates of a request.
func Rank(in RankInput) []Candidate {
	excluded := map[string]bool{}
	for _, id := range in.Excluded {
		excluded[id] = true
	}
	var pool []Candidate
	for _, candidate := range in.Candidates {
		if excluded[candidate.ChannelID] {
			continue
		}
		if in.Mode == routing.ModeManual && !slices.Contains(in.Order, candidate.ChannelID) {
			// A channel missing from the manual list counts as unticked.
			continue
		}
		if in.Available != nil && !in.Available(candidate) {
			continue
		}
		pool = append(pool, candidate)
	}

	var rates, latencies []float64
	for _, stats := range in.Stats {
		if stats.Attempts >= minSamples {
			rates = append(rates, stats.SuccessRate())
		}
		if stats.TTFTP50MS != nil {
			latencies = append(latencies, *stats.TTFTP50MS)
		}
	}
	medianRate, ok := medianOf(rates)
	if !ok {
		medianRate = 1
	}
	medianLatency, haveLatency := medianOf(latencies)
	rate := func(candidate Candidate) float64 {
		stats := in.Stats[candidate.ChannelID]
		if stats.Attempts < minSamples {
			return medianRate
		}
		return stats.SuccessRate()
	}
	latency := func(candidate Candidate) float64 {
		if stats, found := in.Stats[candidate.ChannelID]; found && stats.TTFTP50MS != nil {
			return *stats.TTFTP50MS
		}
		if haveLatency {
			return medianLatency
		}
		return math.Inf(1)
	}

	position := func(candidate Candidate) int { return slices.Index(in.Order, candidate.ChannelID) }
	sort.SliceStable(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		switch in.Mode {
		case routing.ModeManual:
			return position(a) < position(b)
		case routing.ModeReliable:
			if rate(a) != rate(b) {
				return rate(a) > rate(b)
			}
		case routing.ModeFastest:
			if latency(a) != latency(b) {
				return latency(a) < latency(b)
			}
		default:
			if price(a, in.ConsumerID) != price(b, in.ConsumerID) {
				return price(a, in.ConsumerID) < price(b, in.ConsumerID)
			}
			if rate(a) != rate(b) {
				return rate(a) > rate(b)
			}
			return a.ChannelID < b.ChannelID
		}
		if price(a, in.ConsumerID) != price(b, in.ConsumerID) {
			return price(a, in.ConsumerID) < price(b, in.ConsumerID)
		}
		return a.ChannelID < b.ChannelID
	})

	if in.Sticky != "" {
		if index := slices.IndexFunc(pool, func(c Candidate) bool { return c.ChannelID == in.Sticky }); index > 0 {
			sticky := pool[index]
			copy(pool[1:index+1], pool[:index])
			pool[0] = sticky
		}
	}
	return pool
}
