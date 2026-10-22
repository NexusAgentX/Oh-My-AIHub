package gateway

import (
	"math"
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
	// Demoted are the channels that answer this model with 4xx almost every
	// time while the others do not (detectAnomalies): they are tried after all
	// the others, in any mode, but not excluded.
	Demoted    map[string]Anomaly
	ConsumerID string
	// Available reports whether a candidate can take a request now
	// (cooldown, concurrency, RPM).
	Available func(Candidate) bool
}

func compareCandidatePrice(a, b Candidate, consumer string, comparable bool) int {

	left, right := a.TypicalCost, b.TypicalCost
	knownLeft, knownRight := a.TypicalCostKnown && comparable, b.TypicalCostKnown && comparable
	if consumer != "" && a.OwnerID == consumer {
		left, knownLeft = 0, true
	}
	if consumer != "" && b.OwnerID == consumer {
		right, knownRight = 0, true
	}
	if knownLeft != knownRight {
		if knownLeft {
			return -1
		}
		return 1
	}
	if !knownLeft {
		return 0
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func medianOf(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sort.Float64s(values)
	return (values[(len(values)-1)/2] + values[len(values)/2]) / 2, true
}

// Rank filters and orders the candidates of a request.
func Rank(in RankInput) []Candidate {
	excluded := map[string]bool{}
	for _, id := range in.Excluded {
		excluded[id] = true
	}
	var positions map[string]int
	if in.Mode == routing.ModeManual {
		positions = make(map[string]int, len(in.Order))
		for index, id := range in.Order {
			if _, exists := positions[id]; !exists {
				positions[id] = index
			}
		}
	}
	var pool []Candidate
	for _, candidate := range in.Candidates {
		if excluded[candidate.ChannelID] {
			continue
		}
		if _, selected := positions[candidate.ChannelID]; in.Mode == routing.ModeManual && !selected {
			// A channel missing from the manual list counts as unticked.
			continue
		}
		if in.Available != nil && !in.Available(candidate) {
			continue
		}
		pool = append(pool, candidate)
	}

	pricesComparable := comparableCandidatePrices(pool)
	var rates []float64
	for _, candidate := range pool {
		stats := in.Stats[candidate.ChannelID]
		if stats.Attempts >= minSamples {
			rates = append(rates, stats.SuccessRate())
		}
	}
	medianRate, ok := medianOf(rates)
	if !ok {
		medianRate = 1
	}
	rate := func(candidate Candidate) float64 {
		stats := in.Stats[candidate.ChannelID]
		if stats.Attempts < minSamples {
			return medianRate
		}
		return stats.SuccessRate()
	}
	latency := func(candidate Candidate) float64 {
		if stats, found := in.Stats[candidate.ChannelID]; found && stats.TTFTP50MS != nil && stats.Attempts >= minSamples {
			return *stats.TTFTP50MS
		}
		return math.Inf(1)
	}
	position := func(candidate Candidate) int { return positions[candidate.ChannelID] }
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
			if order := compareCandidatePrice(a, b, in.ConsumerID, pricesComparable); order != 0 {
				return order < 0
			}
			if rate(a) != rate(b) {
				return rate(a) > rate(b)
			}
			return a.ChannelID < b.ChannelID
		}
		if order := compareCandidatePrice(a, b, in.ConsumerID, pricesComparable); order != 0 {
			return order < 0
		}
		return a.ChannelID < b.ChannelID
	})

	if len(in.Demoted) > 0 {
		pool = demote(pool, in.Demoted)
	}

	return pool
}

// demote moves the demoted candidates behind the others, each group keeping its
// order.
func demote(pool []Candidate, demoted map[string]Anomaly) []Candidate {
	rest := make([]Candidate, 0, len(pool))
	var last []Candidate
	for _, candidate := range pool {
		if _, bad := demoted[candidate.ChannelID]; bad {
			last = append(last, candidate)
		} else {
			rest = append(rest, candidate)
		}
	}
	return append(rest, last...)
}
