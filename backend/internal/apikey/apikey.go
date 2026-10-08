// Package apikey manages the platform API keys users call the gateway with.
// A key is stored as a SHA-256 hash for lookup and, so it can be copied again
// at any time, as AEAD ciphertext under the upstream credential keyring
// (ADR-0028).
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrInvalidInput = errors.New("invalid api key")
	ErrNotFound     = errors.New("api key not found")
	ErrLimitReached = errors.New("api key limit reached")
)

const (
	// Prefix marks every platform key so clients and the gateway can tell it
	// from upstream credentials.
	Prefix = "sk-aih-"
	// MaxPerOwner is the number of undeleted keys one account may hold.
	MaxPerOwner = 20
	// DefaultName names the key every account gets on first visit.
	DefaultName = "默认 Key"

	keyVersion = 1
	maxAliases = 50
)

type Status string

const (
	StatusEnabled  Status = "enabled"
	StatusDisabled Status = "disabled"
)

type Spend struct {
	Today money.Amount
	Month money.Amount
	Total money.Amount
}

type Key struct {
	ID            string
	OwnerID       string
	Name          string
	Prefix        string
	Status        Status
	IsDefault     bool
	ExpiresAt     *time.Time
	AllowedModels []string
	BudgetDaily   *money.Amount
	BudgetMonthly *money.Amount
	BudgetTotal   *money.Amount
	ModelAliases  map[string]string
	RoutedModels  []string
	Spend         Spend
	LastUsedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Sealed is the stored form of a new key.
type Sealed struct {
	ID         string
	OwnerID    string
	Name       string
	Prefix     string
	Hash       []byte
	Credential channel.EncryptedCredential
	Settings
}

// Settings are the user-editable fields.
type Settings struct {
	Status        Status
	ExpiresAt     *time.Time
	AllowedModels []string
	BudgetDaily   *money.Amount
	BudgetMonthly *money.Amount
	BudgetTotal   *money.Amount
	ModelAliases  map[string]string
}

// Update changes the given fields; for the nullable ones a non-nil outer
// pointer with a nil inner value clears the field.
type Update struct {
	Name          *string
	Status        *Status
	ExpiresAt     **time.Time
	AllowedModels *[]string
	BudgetDaily   **money.Amount
	BudgetMonthly **money.Amount
	BudgetTotal   **money.Amount
	ModelAliases  *map[string]string
}

func (u Update) Empty() bool {
	return u.Name == nil && u.Status == nil && u.ExpiresAt == nil && u.AllowedModels == nil &&
		u.BudgetDaily == nil && u.BudgetMonthly == nil && u.BudgetTotal == nil && u.ModelAliases == nil
}

type Store interface {
	// Create inserts the key unless the owner already holds MaxPerOwner.
	Create(ctx context.Context, key Sealed) (Key, error)
	// EnsureDefault creates the default key once per account: it sets
	// accounts.default_key_created_at and inserts the key in one transaction,
	// doing nothing when the marker is already set.
	EnsureDefault(ctx context.Context, key Sealed) (created bool, err error)
	List(ctx context.Context, ownerID string) ([]Key, error)
	Get(ctx context.Context, ownerID, id string) (Key, error)
	Update(ctx context.Context, ownerID, id string, update Update) (Key, error)
	Delete(ctx context.Context, ownerID, id string) error
	Credential(ctx context.Context, ownerID, id string) (channel.EncryptedCredential, error)
	// RecordReveal writes the api_key.reveal audit row.
	RecordReveal(ctx context.Context, ownerID, id string) error
}

// Generate returns a fresh random platform key.
func Generate() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// Hash is the lookup digest of a full key.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// DisplayPrefix is the part of a key shown in lists.
func DisplayPrefix(secret string) string {
	if len(secret) <= len(Prefix)+4 {
		return secret
	}
	return secret[:len(Prefix)+4]
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)

type Service struct {
	store   Store
	keyring *channel.Keyring
	models  func(context.Context) ([]string, error)
}

// NewService wires the key service; models returns every catalog model ID.
func NewService(store Store, keyring *channel.Keyring, models func(context.Context) ([]string, error)) *Service {
	return &Service{store: store, keyring: keyring, models: models}
}

// CreateInput carries the optional fields of a new key.
type CreateInput struct {
	Name string
	Settings
}

func (s *Service) seal(ownerID, name string, settings Settings) (Sealed, string, error) {
	secret, err := Generate()
	if err != nil {
		return Sealed{}, "", err
	}
	id, err := ids.NewUUID()
	if err != nil {
		return Sealed{}, "", err
	}
	credential, err := s.keyring.Encrypt(id, keyVersion, secret)
	if err != nil {
		return Sealed{}, "", err
	}
	if settings.Status == "" {
		settings.Status = StatusEnabled
	}
	return Sealed{ID: id, OwnerID: ownerID, Name: name, Prefix: DisplayPrefix(secret), Hash: Hash(secret), Credential: credential, Settings: settings}, secret, nil
}

func validName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 64 && !slices.ContainsFunc([]rune(name), unicode.IsControl)
}

func (s *Service) known(ctx context.Context) (map[string]bool, error) {
	list, err := s.models(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(list))
	for _, id := range list {
		set[id] = true
	}
	return set, nil
}

func validBudget(value *money.Amount) bool { return value == nil || *value > 0 }

func (s *Service) validateSettings(ctx context.Context, settings *Settings) error {
	if settings.Status != StatusEnabled && settings.Status != StatusDisabled {
		return ErrInvalidInput
	}
	if !validBudget(settings.BudgetDaily) || !validBudget(settings.BudgetMonthly) || !validBudget(settings.BudgetTotal) {
		return ErrInvalidInput
	}
	known, err := s.known(ctx)
	if err != nil {
		return err
	}
	allowed := make([]string, 0, len(settings.AllowedModels))
	for _, id := range settings.AllowedModels {
		id = strings.TrimSpace(id)
		if !known[id] {
			return ErrInvalidInput
		}
		if !slices.Contains(allowed, id) {
			allowed = append(allowed, id)
		}
	}
	slices.Sort(allowed)
	settings.AllowedModels = allowed
	if len(settings.ModelAliases) > maxAliases {
		return ErrInvalidInput
	}
	aliases := make(map[string]string, len(settings.ModelAliases))
	for alias, target := range settings.ModelAliases {
		alias, target = strings.TrimSpace(alias), strings.TrimSpace(target)
		if !aliasPattern.MatchString(alias) || !known[target] {
			return ErrInvalidInput
		}
		aliases[alias] = target
	}
	settings.ModelAliases = aliases
	return nil
}

// Create makes a key and returns it with its full secret.
func (s *Service) Create(ctx context.Context, ownerID string, in CreateInput) (Key, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !validName(in.Name) {
		return Key{}, "", ErrInvalidInput
	}
	if in.Status == "" {
		in.Status = StatusEnabled
	}
	if err := s.validateSettings(ctx, &in.Settings); err != nil {
		return Key{}, "", err
	}
	sealed, secret, err := s.seal(ownerID, in.Name, in.Settings)
	if err != nil {
		return Key{}, "", err
	}
	key, err := s.store.Create(ctx, sealed)
	return key, secret, err
}

// EnsureDefault gives the account its default key on the first visit.
func (s *Service) EnsureDefault(ctx context.Context, ownerID string) error {
	sealed, _, err := s.seal(ownerID, DefaultName, Settings{Status: StatusEnabled})
	if err != nil {
		return err
	}
	_, err = s.store.EnsureDefault(ctx, sealed)
	return err
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Key, error) {
	return s.store.List(ctx, ownerID)
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Key, error) {
	return s.store.Get(ctx, ownerID, id)
}

func (s *Service) Update(ctx context.Context, ownerID, id string, update Update) (Key, error) {
	if update.Empty() {
		return Key{}, ErrInvalidInput
	}
	if update.Name != nil {
		name := strings.TrimSpace(*update.Name)
		if !validName(name) {
			return Key{}, ErrInvalidInput
		}
		update.Name = &name
	}
	if update.Status != nil && *update.Status != StatusEnabled && *update.Status != StatusDisabled {
		return Key{}, ErrInvalidInput
	}
	for _, budget := range []**money.Amount{update.BudgetDaily, update.BudgetMonthly, update.BudgetTotal} {
		if budget != nil && !validBudget(*budget) {
			return Key{}, ErrInvalidInput
		}
	}
	if update.AllowedModels != nil || update.ModelAliases != nil {
		settings := Settings{Status: StatusEnabled}
		if update.AllowedModels != nil {
			settings.AllowedModels = *update.AllowedModels
		}
		if update.ModelAliases != nil {
			settings.ModelAliases = *update.ModelAliases
		}
		if err := s.validateSettings(ctx, &settings); err != nil {
			return Key{}, err
		}
		if update.AllowedModels != nil {
			update.AllowedModels = &settings.AllowedModels
		}
		if update.ModelAliases != nil {
			update.ModelAliases = &settings.ModelAliases
		}
	}
	return s.store.Update(ctx, ownerID, id, update)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	return s.store.Delete(ctx, ownerID, id)
}

// Secret decrypts the full key and records the view in the audit log.
func (s *Service) Secret(ctx context.Context, ownerID, id string) (string, error) {
	credential, err := s.store.Credential(ctx, ownerID, id)
	if err != nil {
		return "", err
	}
	secret, err := s.keyring.Decrypt(id, credential)
	if err != nil {
		return "", err
	}
	if err := s.store.RecordReveal(ctx, ownerID, id); err != nil {
		return "", err
	}
	return secret, nil
}
