package gateway

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

var ErrManualOrderRequired = errors.New("an explicit manual channel order is required")
var ErrCapabilityCandidates = errors.New("no authorized candidate has current support evidence")

type CapabilityRequirement struct{ Layer, ID string }

// ResourceScope is supplied by trusted owner/Key resource metadata, never by
// self-reported session IDs, opaque ciphertext or notes/history identifiers.
type ResourceScope struct{ GroupID, ChannelID, CredentialScope string }
type AuxiliarySelection struct {
	ModelID       string
	Format        channel.Format
	AllowedModels []string
	Preference    routing.Pref
	Required      []CapabilityRequirement
	Reference     *ResourceScope
}
type AuxiliarySource interface {
	AuxiliaryCandidates(context.Context, channel.Format) ([]CandidateRecord, error)
}

// SelectAuxiliary requires one model witness to satisfy every capability on a
// candidate. The witness authorizes access; it is not an invented model price,
// throughput estimate or fee for a model-less metadata operation. With model
// context callers can rank the result using that model's ordinary statistics.
func SelectAuxiliary(records []CandidateRecord, in AuxiliarySelection) ([]CandidateRecord, error) {
	if in.Preference.Mode == routing.ModeManual && len(in.Preference.Order) == 0 {
		return nil, ErrManualOrderRequired
	}
	records = slices.Clone(records)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Candidate.ChannelID != records[j].Candidate.ChannelID {
			return records[i].Candidate.ChannelID < records[j].Candidate.ChannelID
		}
		return records[i].ModelID < records[j].ModelID
	})
	out := []CandidateRecord{}
	seen := map[string]bool{}
	for _, r := range records {
		c := r.Candidate
		if seen[c.ChannelID] || c.GroupID == "" || c.Quotes == nil || !slices.Contains(r.Formats, in.Format) || in.ModelID != "" && r.ModelID != in.ModelID || len(in.AllowedModels) > 0 && !slices.Contains(in.AllowedModels, r.ModelID) || slices.Contains(in.Preference.Excluded, c.ChannelID) {
			continue
		}
		if in.Preference.Mode == routing.ModeManual && !slices.Contains(in.Preference.Order, c.ChannelID) {
			continue
		}
		if ref := in.Reference; ref != nil && (ref.GroupID == "" || ref.ChannelID == "" || ref.CredentialScope == "" || c.GroupID != ref.GroupID || c.ChannelID != ref.ChannelID || c.CredentialScope != ref.CredentialScope) {
			continue
		}
		supported := true
		for _, req := range in.Required {
			if !c.Evidence[req.Layer+"/"+req.ID] {
				supported = false
				break
			}
		}
		if !supported {
			continue
		}
		seen[c.ChannelID] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, ErrCapabilityCandidates
	}
	if in.Preference.Mode == routing.ModeManual {
		positions := map[string]int{}
		for i, id := range in.Preference.Order {
			positions[id] = i
		}
		sort.SliceStable(out, func(i, j int) bool {
			return positions[out[i].Candidate.ChannelID] < positions[out[j].Candidate.ChannelID]
		})
	}
	return out, nil
}
