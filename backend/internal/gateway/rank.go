package gateway

import (
	"slices"
	"sort"
)

// RankInput describes one routing decision.
type RankInput struct {
	Order      []string // manual order
	Excluded   []string
	Candidates []Candidate
	// Sticky is the channel that served the previous Responses turn, if any.
	Sticky string
	// Available reports whether a candidate can take a request now
	// (cooldown, concurrency, RPM, daily revenue cap).
	Available func(Candidate) bool
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
		if !slices.Contains(in.Order, candidate.ChannelID) {
			// A channel missing from the manual list counts as unticked.
			continue
		}
		if in.Available != nil && !in.Available(candidate) {
			continue
		}
		pool = append(pool, candidate)
	}

	position := func(candidate Candidate) int { return slices.Index(in.Order, candidate.ChannelID) }
	sort.SliceStable(pool, func(i, j int) bool { return position(pool[i]) < position(pool[j]) })

	if in.Sticky != "" {
		if index := slices.IndexFunc(pool, func(c Candidate) bool { return c.ChannelID == in.Sticky }); index > 0 {
			sticky := pool[index]
			copy(pool[1:index+1], pool[:index])
			pool[0] = sticky
		}
	}
	return pool
}
