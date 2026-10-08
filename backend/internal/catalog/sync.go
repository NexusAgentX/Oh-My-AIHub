package catalog

import (
	"encoding/json"
	"time"
)

// SourceInfo is provenance, not a second set of effective billing prices.
type SourceInfo struct {
	Key           string                     `json:"key"`
	SyncEnabled   bool                       `json:"sync_enabled"`
	Status        string                     `json:"status"`
	Problems      []string                   `json:"problems"`
	Warnings      []string                   `json:"warnings"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
	LastSeenAt    time.Time                  `json:"last_seen_at"`
	LastAppliedAt *time.Time                 `json:"last_applied_at"`
	AppliedRate   string                     `json:"applied_rate"`
	PriceReady    bool                       `json:"price_ready"`
}
