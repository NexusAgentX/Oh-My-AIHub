package snapshot

import (
	"slices"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// The sets below are what the snapshots hold. They are built once, published
// whole and never modified afterwards, so any number of requests read them
// without locking. What they hand out (slices, maps and pointers inside the
// values) is shared and read-only for every reader.

// keySet maps the digest of a platform key to the key.
type keySet map[string]gateway.KeyAuth

func buildKeys(records []gateway.KeyRecord) keySet {
	keys := make(keySet, len(records))
	for _, record := range records {
		keys[string(record.Hash)] = record.Auth
	}
	return keys
}

// candidateKey addresses the candidates of one model in one format.
type candidateKey struct {
	model  string
	format channel.Format
}

// fleetSet is everything derived from the listed channels and their models:
// the candidates of each model and format, the models that have any (with the
// formats they are served in), and the custom ones among them.
type fleetSet struct {
	modelLists   map[channel.Format][]gateway.CandidateRecord
	auxiliary    map[channel.Format][]gateway.CandidateRecord
	candidates   map[candidateKey][]gateway.Candidate
	online       []gateway.OnlineModel
	customOnline map[string]struct{}
}

// buildFleet indexes candidate records, which come in model order and,
// within a model, in channel order. The index keeps that order, and the list
// of online models is in model order with the formats sorted.
func buildFleet(records []gateway.CandidateRecord) fleetSet {
	fleet := fleetSet{
		modelLists:   make(map[channel.Format][]gateway.CandidateRecord),
		auxiliary:    make(map[channel.Format][]gateway.CandidateRecord),
		candidates:   make(map[candidateKey][]gateway.Candidate),
		online:       []gateway.OnlineModel{},
		customOnline: make(map[string]struct{}),
	}
	position := map[string]int{}
	for _, record := range records {
		for _, format := range record.Formats {
			fleet.modelLists[format] = append(fleet.modelLists[format], record)
			if record.Candidate.GroupID != "" {
				fleet.auxiliary[format] = append(fleet.auxiliary[format], record)
			}
			key := candidateKey{model: record.ModelID, format: format}
			fleet.candidates[key] = append(fleet.candidates[key], record.Candidate)
		}
		index, seen := position[record.ModelID]
		if !seen {
			index = len(fleet.online)
			position[record.ModelID] = index
			fleet.online = append(fleet.online, gateway.OnlineModel{ID: record.ModelID, Formats: []channel.Format{}})
		}
		online := &fleet.online[index]
		if record.Candidate.Custom != nil {
			online.Custom = true
			fleet.customOnline[record.ModelID] = struct{}{}
		}
		for _, format := range record.Formats {
			if !slices.Contains(online.Formats, format) {
				online.Formats = append(online.Formats, format)
			}
		}
	}
	for index := range fleet.online {
		formats := fleet.online[index].Formats
		sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	}
	return fleet
}

// modelSet is the catalog: every model by name, and the display-ordered lists
// the model list endpoints use.
type modelSet struct {
	byID    map[string]catalog.Model
	all     []catalog.Model
	enabled []catalog.Model
}

// buildModels indexes models given in display order, disabled ones included.
func buildModels(models []catalog.Model) modelSet {
	set := modelSet{byID: make(map[string]catalog.Model, len(models)), all: models, enabled: []catalog.Model{}}
	if set.all == nil {
		set.all = []catalog.Model{}
	}
	for _, model := range models {
		set.byID[model.ID] = model
		if model.Enabled {
			set.enabled = append(set.enabled, model)
		}
	}
	return set
}

// routeKey addresses one stored preference: key empty is the account level.
type routeKey struct {
	account string
	key     string
	model   string
}

type routeSet map[routeKey]routing.Pref

func buildRoutes(prefs []routing.Scoped) routeSet {
	set := make(routeSet, len(prefs))
	for _, scoped := range prefs {
		set[routeKey{account: scoped.AccountID, key: scoped.KeyID, model: scoped.Pref.ModelID}] = scoped.Pref
	}
	return set
}
