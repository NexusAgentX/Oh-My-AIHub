package catalogsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var ErrBusy = errors.New("catalog sync already running")

type Status struct {
	ExchangeRate string         `json:"exchange_rate"`
	StartedAt    *time.Time     `json:"started_at"`
	FinishedAt   *time.Time     `json:"finished_at"`
	Status       string         `json:"status"`
	Error        string         `json:"error"`
	Result       map[string]int `json:"result"`
}
type Store interface {
	SyncStatus(context.Context) (Status, error)
	SetSyncRate(context.Context, string, string) error
	// RunSync holds a database advisory lock, including during fetch.
	RunSync(context.Context, func(string) ([]Entry, error)) error
	SourceRaw(context.Context, string) (json.RawMessage, error)
}
type Service struct {
	store      Store
	client     *http.Client
	mu         sync.Mutex
	background context.Context
}

func NewService(store Store, ctx context.Context) *Service {
	return &Service{store: store, background: ctx, client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("datasheet redirects are disabled") }}}
}
func (s *Service) Status(ctx context.Context) (Status, error) { return s.store.SyncStatus(ctx) }

var ratePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$`)

func (s *Service) SetRate(ctx context.Context, actor, rate string) error {
	if rate != "" {
		if !ratePattern.MatchString(rate) {
			return catalog.ErrInvalidInput
		}
		if _, err := Price(json.RawMessage(`0`), rate); err != nil {
			return catalog.ErrInvalidInput
		}
	}
	return s.store.SetSyncRate(ctx, actor, rate)
}
func (s *Service) Raw(ctx context.Context, id string) (json.RawMessage, error) {
	return s.store.SourceRaw(ctx, id)
}
func (s *Service) Trigger() bool {
	if !s.mu.TryLock() {
		return false
	}
	go func() { defer s.mu.Unlock(); s.run(s.background) }()
	return true
}
func (s *Service) Run(ctx context.Context) {
	s.Trigger()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Trigger()
		}
	}
}
func (s *Service) run(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err := s.store.RunSync(ctx, func(rate string) ([]Entry, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("datasheet HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
		if err != nil {
			return nil, err
		}
		return Decode(body, rate)
	})
	if err != nil && !errors.Is(err, ErrBusy) {
		slog.Error("catalog synchronization failed", "error", err)
	}
}
