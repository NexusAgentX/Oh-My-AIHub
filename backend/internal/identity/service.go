package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

type Service struct {
	store           Store
	now             func() time.Time
	sessionLifetime time.Duration
	dummyHash       string
}

func NewService(store Store, sessionLifetime time.Duration) (*Service, error) {
	dummyHash, err := HashPassword("not-a-real-user-password")
	if err != nil {
		return nil, err
	}
	return &Service{
		store:           store,
		now:             time.Now,
		sessionLifetime: sessionLifetime,
		dummyHash:       dummyHash,
	}, nil
}

func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	account, err := s.store.FindAccountByUsername(ctx, NormalizeUsername(username))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			VerifyPassword(s.dummyHash, password)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}
	passwordMatches := VerifyPassword(account.PasswordHash, password)
	if account.Status != StatusActive || !passwordMatches {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, session, err := s.newSession(account.ID, account.PasswordVersion)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Account: account.Account, SessionToken: token}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Account, error) {
	hash, err := tokenHash(token)
	if err != nil {
		return Account{}, ErrInvalidCredentials
	}
	account, err := s.store.FindAccountBySession(ctx, hash, s.now().UTC())
	if errors.Is(err, ErrNotFound) {
		return Account{}, ErrInvalidCredentials
	}
	return account, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash, err := tokenHash(token)
	if err != nil {
		return nil
	}
	return s.store.DeleteSession(ctx, hash)
}

func (s *Service) ChangePassword(ctx context.Context, accountID, currentPassword, newPassword string) (LoginResult, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return LoginResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	account, err := s.store.FindAccountByID(ctx, accountID)
	if err != nil {
		return LoginResult{}, err
	}
	if !VerifyPassword(account.PasswordHash, currentPassword) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if currentPassword == newPassword {
		return LoginResult{}, fmt.Errorf("%w: new password must differ from current password", ErrInvalidInput)
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return LoginResult{}, err
	}
	token, session, err := s.newSession(accountID, account.PasswordVersion+1)
	if err != nil {
		return LoginResult{}, err
	}
	changedAt := s.now().UTC()
	if err := s.store.ReplacePasswordAndSessions(ctx, accountID, account.PasswordVersion, newHash, session, changedAt); err != nil {
		return LoginResult{}, err
	}
	account.PasswordHash = newHash
	account.MustChangePassword = false
	account.PasswordVersion++
	account.PasswordChangedAt = &changedAt
	account.UpdatedAt = changedAt
	return LoginResult{Account: account.Account, SessionToken: token}, nil
}

// AdminResetPassword 生成管理员代发的新初始密码（ADR-0013）：目标账户被标记必须改密，
// 既有会话全部撤销；不为目标创建新会话，因为发起者是管理员而非账户本人。
func (s *Service) AdminResetPassword(ctx context.Context, actor Account, accountID string) (CreatedAccount, error) {
	if !actor.IsAdmin {
		return CreatedAccount{}, ErrForbidden
	}
	if accountID == "" {
		return CreatedAccount{}, ErrInvalidInput
	}
	if accountID == actor.ID {
		return CreatedAccount{}, ErrSelfModification
	}
	password, err := GenerateInitialPassword()
	if err != nil {
		return CreatedAccount{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return CreatedAccount{}, err
	}
	account, err := s.store.ResetPassword(ctx, actor.ID, accountID, hash, s.now().UTC())
	if err != nil {
		return CreatedAccount{}, err
	}
	return CreatedAccount{Account: account, InitialPassword: password}, nil
}

// CreateInvitedAccount creates an account with a one-time initial password.
// A nil credit limit uses the platform default.
func (s *Service) CreateInvitedAccount(ctx context.Context, actor Account, username, displayName string, creditLimit *money.Amount, isAdmin bool) (CreatedAccount, error) {
	if !actor.IsAdmin {
		return CreatedAccount{}, ErrForbidden
	}
	username = NormalizeUsername(username)
	displayName = strings.TrimSpace(displayName)
	if !usernamePattern.MatchString(username) || !validDisplayName(displayName) || (creditLimit != nil && *creditLimit < 0) {
		return CreatedAccount{}, ErrInvalidInput
	}
	password, err := GenerateInitialPassword()
	if err != nil {
		return CreatedAccount{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return CreatedAccount{}, err
	}
	account, err := s.store.CreateAccount(ctx, NewAccount{
		ActorID:            actor.ID,
		Username:           username,
		DisplayName:        displayName,
		PasswordHash:       hash,
		IsAdmin:            isAdmin,
		MustChangePassword: true,
		CreditLimit:        creditLimit,
	})
	if err != nil {
		return CreatedAccount{}, err
	}
	return CreatedAccount{Account: account, InitialPassword: password}, nil
}

func (s *Service) HasAdministrator(ctx context.Context) (bool, error) {
	return s.store.HasAdministrator(ctx)
}

func (s *Service) CreateBootstrapAdmin(ctx context.Context, username, displayName string, password string) (Account, error) {
	username = NormalizeUsername(username)
	displayName = strings.TrimSpace(displayName)
	if !usernamePattern.MatchString(username) || !validDisplayName(displayName) {
		return Account{}, ErrInvalidInput
	}
	if err := ValidatePassword(password); err != nil {
		return Account{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	zero := money.Amount(0)
	return s.store.CreateBootstrapAdmin(ctx, NewAccount{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		IsAdmin:      true,
		// 密码由创始人自设（网页初始化），无第三方经手，
		// 不适用受邀账户的首登强制改密规则。
		MustChangePassword: false,
		CreditLimit:        &zero,
	})
}

func (s *Service) ListAccounts(ctx context.Context, actor Account, filter AccountFilter) ([]AdminAccount, error) {
	if !actor.IsAdmin {
		return nil, ErrForbidden
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Limit < 1 || filter.Limit > 100 || (filter.Status != "" && filter.Status != StatusActive && filter.Status != StatusDisabled) {
		return nil, ErrInvalidInput
	}
	return s.store.ListAccounts(ctx, filter)
}

func (s *Service) GetAccount(ctx context.Context, actor Account, accountID string) (AdminAccount, error) {
	if !actor.IsAdmin {
		return AdminAccount{}, ErrForbidden
	}
	return s.store.GetAccount(ctx, accountID)
}

func (s *Service) UpdateAccount(ctx context.Context, actor Account, accountID string, update AccountUpdate) (AdminAccount, error) {
	if !actor.IsAdmin {
		return AdminAccount{}, ErrForbidden
	}
	if update.DisplayName == nil && update.Status == nil && update.CreditLimit == nil && update.IsAdmin == nil {
		return AdminAccount{}, ErrInvalidInput
	}
	if update.DisplayName != nil {
		trimmed := strings.TrimSpace(*update.DisplayName)
		if !validDisplayName(trimmed) {
			return AdminAccount{}, ErrInvalidInput
		}
		update.DisplayName = &trimmed
	}
	if update.Status != nil && *update.Status != StatusActive && *update.Status != StatusDisabled {
		return AdminAccount{}, ErrInvalidInput
	}
	if update.CreditLimit != nil && *update.CreditLimit < 0 {
		return AdminAccount{}, ErrInvalidInput
	}
	if accountID == actor.ID && ((update.Status != nil && *update.Status == StatusDisabled) || (update.IsAdmin != nil && !*update.IsAdmin)) {
		return AdminAccount{}, ErrSelfModification
	}
	return s.store.UpdateAccount(ctx, actor.ID, accountID, update)
}

func validDisplayName(value string) bool {
	length := len([]rune(value))
	return length >= 1 && length <= 64
}

func (s *Service) newSession(accountID string, passwordVersion int64) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, Session{
		TokenHash:       hash[:],
		AccountID:       accountID,
		PasswordVersion: passwordVersion,
		ExpiresAt:       s.now().UTC().Add(s.sessionLifetime),
	}, nil
}

func tokenHash(token string) ([]byte, error) {
	if len(token) < 32 || len(token) > 128 {
		return nil, ErrInvalidCredentials
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return nil, ErrInvalidCredentials
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
