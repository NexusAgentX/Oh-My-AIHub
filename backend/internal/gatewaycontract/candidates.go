package gatewaycontract

import (
	"fmt"
	"slices"
)

type NoCandidatesError struct {
	MissingEvidence           []string
	ManualOrderMissing        bool
	ScopeOrChannelUnavailable bool
	BoundResourceUnavailable  bool
}

func (e *NoCandidatesError) Error() string {
	return fmt.Sprintf("no eligible candidates: missing_evidence=%v manual_order_missing=%t scope_or_channel_unavailable=%t bound_resource_unavailable=%t", e.MissingEvidence, e.ManualOrderMissing, e.ScopeOrChannelUnavailable, e.BoundResourceUnavailable)
}

type Evidence struct {
	Supported bool
	Valid     bool
}

type Candidate struct {
	Binding
	ModelID      string
	Authorized   bool
	Callable     bool
	Enabled      bool
	Capabilities map[string]Evidence
}

type Selection struct {
	ModelID  string
	Required []string
	Manual   bool
	Order    []string
	Bound    *Binding
}

// Candidates filters one candidate against every hard requirement. There is no
// address capability or invented price/speed. F5/F10 apply model-aware ranking
// after this filter; model-less automatic requests use stable channel IDs,
// manual requests only their explicit order.
func Candidates(request Selection, candidates []Candidate) ([]Candidate, error) {
	positions := map[string]int{}
	for i, id := range request.Order {
		if _, exists := positions[id]; !exists {
			positions[id] = i
		}
	}
	out := make([]Candidate, 0, len(candidates))
	missing := map[string]bool{}
	unavailable := len(candidates) == 0
	for _, c := range candidates {
		if c.GroupID == "" || c.ChannelID == "" || c.CredentialVersion == "" || !c.Enabled || !c.Callable || !c.Authorized || (request.ModelID != "" && request.ModelID != c.ModelID) || (request.Bound != nil && *request.Bound != c.Binding) {
			unavailable = true
			continue
		}
		if request.Manual {
			if _, selected := positions[c.ChannelID]; !selected {
				continue
			}
		}
		valid := true
		for _, capability := range request.Required {
			e := c.Capabilities[capability]
			if !e.Valid || !e.Supported {
				valid = false
				missing[capability] = true
			}
		}
		if valid {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		missingIDs := make([]string, 0, len(missing))
		for id := range missing {
			missingIDs = append(missingIDs, id)
		}
		slices.Sort(missingIDs)
		return nil, &NoCandidatesError{MissingEvidence: missingIDs, ManualOrderMissing: request.Manual && len(request.Order) == 0,
			ScopeOrChannelUnavailable: unavailable, BoundResourceUnavailable: request.Bound != nil}
	}
	if request.Manual || request.ModelID == "" {
		slices.SortStableFunc(out, func(a, b Candidate) int {
			if request.Manual {
				return positions[a.ChannelID] - positions[b.ChannelID]
			}
			if a.ChannelID < b.ChannelID {
				return -1
			}
			if a.ChannelID > b.ChannelID {
				return 1
			}
			return 0
		})
	}
	return out, nil
}
