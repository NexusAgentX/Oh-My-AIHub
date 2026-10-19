package identity

import (
	"context"
	"errors"
	"fmt"
)

// WithFixedAdministrator protects the shared test account. Production does not
// pass this option; no gateway or ordinary login checks are added.
func WithFixedAdministrator(username string) Option {
	return func(s *Service) { s.fixedAdmin = NormalizeUsername(username) }
}

func (s *Service) FixedAdministrator() string { return s.fixedAdmin }

func (s *Service) isFixedAdministrator(username string) bool {
	return s.fixedAdmin != "" && username == s.fixedAdmin
}

func (s *Service) protectAdministrator(ctx context.Context, accountID string) error {
	if s.fixedAdmin == "" {
		return nil
	}
	account, err := s.store.FindAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	if s.isFixedAdministrator(account.Username) {
		return ErrProtectedAccount
	}
	return nil
}

// EnsureFixedAdministrator runs before HTTP starts, including after a test-only
// reset. Existing credentials must match: a restart never silently resets an
// account or takes over an already initialized instance.
func (s *Service) EnsureFixedAdministrator(ctx context.Context, password string) error {
	if s.fixedAdmin == "" {
		return fmt.Errorf("%w: fixed administrator is not configured", ErrInvalidInput)
	}
	if err := ValidatePassword(password); err != nil {
		return fmt.Errorf("%w: invalid fixed administrator password", ErrInvalidInput)
	}
	account, err := s.store.FindAccountByUsername(ctx, s.fixedAdmin)
	if errors.Is(err, ErrNotFound) {
		_, err = s.CreateBootstrapAdmin(ctx, s.fixedAdmin, "内测管理员", password)
		return err
	}
	if err != nil {
		return err
	}
	if !account.IsAdmin || account.Status != StatusActive || account.MustChangePassword || !VerifyPassword(account.PasswordHash, password) {
		return fmt.Errorf("%w: existing administrator does not match test configuration", ErrProtectedAccount)
	}
	return nil
}
