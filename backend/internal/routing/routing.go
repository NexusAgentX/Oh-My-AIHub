// Package routing holds the per-user, per-model routing preferences (Epic
// #170 decision 2): one calling mode per model, optional unticked channels,
// an optional manual order, and per-key overrides of the account setting.
package routing

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid routing preference")
	ErrNotFound     = errors.New("routing preference not found")
)

type Mode string

const (
	ModeCheapest Mode = "cheapest"
	ModeReliable Mode = "reliable"
	ModeFastest  Mode = "fastest"
	ModeManual   Mode = "manual"
)

func (m Mode) Valid() bool {
	return m == ModeCheapest || m == ModeReliable || m == ModeFastest || m == ModeManual
}

type Source string

const (
	SourceDefault Source = "default"
	SourceAccount Source = "account"
	SourceKey     Source = "key"
)

// Pref is one resolved preference. Without any stored row the preference is
// the default: cheapest first, every channel ticked, platform defaults.
type Pref struct {
	ModelID       string
	Source        Source
	Mode          Mode
	Order         []string // manual order of channel IDs (ticked and unticked)
	Excluded      []string // unticked channel IDs
	MaxAttempts   *int32
	TTFTTimeoutMS *int32
	UpdatedAt     *time.Time
}

// Default is the preference of a model nobody configured.
func Default(modelID string) Pref {
	return Pref{ModelID: modelID, Source: SourceDefault, Mode: ModeCheapest, Order: []string{}, Excluded: []string{}}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Validate normalizes IDs to lower case, removes duplicates and checks ranges.
func Validate(p Pref) (Pref, error) {
	if !p.Mode.Valid() {
		return Pref{}, ErrInvalidInput
	}
	unique := func(values []string) ([]string, bool) {
		seen := map[string]bool{}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if !uuidPattern.MatchString(value) {
				return nil, false
			}
			if !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
		return result, true
	}
	var ok bool
	if p.Order, ok = unique(p.Order); !ok || len(p.Order) > 500 {
		return Pref{}, ErrInvalidInput
	}
	if p.Excluded, ok = unique(p.Excluded); !ok || len(p.Excluded) > 500 {
		return Pref{}, ErrInvalidInput
	}
	if p.MaxAttempts != nil && (*p.MaxAttempts < 1 || *p.MaxAttempts > 10) {
		return Pref{}, ErrInvalidInput
	}
	if p.TTFTTimeoutMS != nil && (*p.TTFTTimeoutMS < 1000 || *p.TTFTTimeoutMS > 600000) {
		return Pref{}, ErrInvalidInput
	}
	return p, nil
}

// Store persists preferences. keyID empty addresses the account-level setting.
type Store interface {
	Set(ctx context.Context, accountID, keyID string, pref Pref) (Pref, error)
	Delete(ctx context.Context, accountID, keyID, modelID string) error
	// Get returns the stored preference of exactly this scope, or ErrNotFound.
	Get(ctx context.Context, accountID, keyID, modelID string) (Pref, error)
	// ListForKey returns the key-level preferences of a key.
	ListForKey(ctx context.Context, accountID, keyID string) ([]Pref, error)
	// Resolve returns the key override if present, else the account setting,
	// else the default.
	Resolve(ctx context.Context, accountID, keyID, modelID string) (Pref, error)
}
