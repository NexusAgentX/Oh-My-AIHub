// Package settings holds the single-row platform settings: fee rate, C2C
// payment timeout, default credit limit and the gateway defaults that
// channels and routes inherit when they leave an advanced field empty.
package settings

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var ErrInvalidInput = errors.New("invalid settings")

// MaxExtraBlockedHosts bounds the administrator-managed egress block list.
const MaxExtraBlockedHosts = 100

type Settings struct {
	FeeRateNano              int64
	C2CPaymentTimeoutMinutes int32
	DefaultCreditLimit       money.Amount
	DefaultMaxAttempts       int32
	DefaultTTFTTimeoutMS     int32
	DefaultTotalTimeoutMS    int32
	DefaultCooldownFailures  int32
	DefaultCooldownSeconds   int32
	ExtraBlockedHosts        []string
	UpdatedAt                time.Time
}

type Store interface {
	GetSettings(ctx context.Context) (Settings, error)
	// UpdateSettings replaces every field and records an audit row with the
	// previous and new values.
	UpdateSettings(ctx context.Context, actorID string, value Settings) (Settings, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Get(ctx context.Context) (Settings, error) { return s.store.GetSettings(ctx) }

func (s *Service) Update(ctx context.Context, actorID string, value Settings) (Settings, error) {
	value.ExtraBlockedHosts = normalizeHosts(value.ExtraBlockedHosts)
	if err := Validate(value); err != nil {
		return Settings{}, err
	}
	return s.store.UpdateSettings(ctx, actorID, value)
}

func normalizeHosts(hosts []string) []string {
	result := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		result = append(result, host)
	}
	return result
}

func validHost(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}

// Validate enforces the same ranges as the database constraints.
func Validate(value Settings) error {
	switch {
	case value.FeeRateNano < 0 || value.FeeRateNano > money.Scale,
		value.C2CPaymentTimeoutMinutes < 5 || value.C2CPaymentTimeoutMinutes > 1440,
		value.DefaultCreditLimit < 0,
		value.DefaultMaxAttempts < 1 || value.DefaultMaxAttempts > 10,
		value.DefaultTTFTTimeoutMS < 1000 || value.DefaultTTFTTimeoutMS > 600000,
		value.DefaultTotalTimeoutMS < 1000 || value.DefaultTotalTimeoutMS > 3600000,
		value.DefaultTotalTimeoutMS < value.DefaultTTFTTimeoutMS,
		value.DefaultCooldownFailures < 1 || value.DefaultCooldownFailures > 100,
		value.DefaultCooldownSeconds < 10 || value.DefaultCooldownSeconds > 86400,
		len(value.ExtraBlockedHosts) > MaxExtraBlockedHosts:
		return ErrInvalidInput
	}
	for _, host := range value.ExtraBlockedHosts {
		if !validHost(host) {
			return ErrInvalidInput
		}
	}
	return nil
}
