package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Automatic sync accepts only a plausible USD/CNY rate, and refuses a jump of
// more than MaxAutoRateChange from the current value: a broken or inverted
// feed must not silently reprice every USD model.
const (
	minAutoRateNano   int64 = 1_000_000_000  // 1
	maxAutoRateNano   int64 = 50_000_000_000 // 50
	MaxAutoRateChange       = 0.2
)

var ErrRateRejected = errors.New("exchange rate rejected")

// RateSource fetches the current USD/CNY rate in nano units.
type RateSource interface {
	FetchUSDCNY(ctx context.Context) (int64, error)
}

// RateStore persists the outcome of a sync.
type RateStore interface {
	GetSettings(ctx context.Context) (Settings, error)
	// SaveSyncedRate stores a fetched rate as the automatic rate, clears the
	// sync error and records an audit row (an empty actorID is the system).
	SaveSyncedRate(ctx context.Context, actorID string, rateNano int64, at time.Time) (Settings, error)
	// SaveSyncFailure records a failed attempt and leaves the rate unchanged.
	SaveSyncFailure(ctx context.Context, message string, at time.Time) (Settings, error)
}

// RateSyncer fetches the exchange rate on demand and on the daily schedule.
type RateSyncer struct {
	store  RateStore
	source RateSource
	now    func() time.Time
}

func NewRateSyncer(store RateStore, source RateSource) *RateSyncer {
	return &RateSyncer{store: store, source: source, now: time.Now}
}

// SyncDue reports whether the daily automatic sync should run at now: it is
// enabled, today's sync minute (Asia/Shanghai) has passed, and no attempt was
// made since then.
func SyncDue(value Settings, now time.Time) bool {
	if !value.FXAutoSync {
		return false
	}
	scheduled := localtime.DayStart(now).Add(time.Duration(value.FXSyncMinute) * time.Minute)
	if now.Before(scheduled) {
		return false
	}
	return value.FXSyncAttemptedAt == nil || value.FXSyncAttemptedAt.Before(scheduled)
}

// Sync fetches and stores the rate now. A failed fetch or an implausible
// rate is recorded as the sync error and returned.
func (s *RateSyncer) Sync(ctx context.Context, actorID string) (Settings, error) {
	current, err := s.store.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	now := s.now()
	rate, err := s.source.FetchUSDCNY(ctx)
	if err == nil {
		err = checkAutoRate(current.USDCNYRateNano, rate)
	}
	if err != nil {
		if _, saveErr := s.store.SaveSyncFailure(ctx, err.Error(), now); saveErr != nil {
			return Settings{}, saveErr
		}
		return Settings{}, err
	}
	return s.store.SaveSyncedRate(ctx, actorID, rate, now)
}

// SyncIfDue runs the daily automatic sync when SyncDue says so.
func (s *RateSyncer) SyncIfDue(ctx context.Context) (bool, error) {
	current, err := s.store.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	if !SyncDue(current, s.now()) {
		return false, nil
	}
	_, err = s.Sync(ctx, "")
	return true, err
}

func checkAutoRate(current, next int64) error {
	if next < minAutoRateNano || next > maxAutoRateNano {
		return fmt.Errorf("%w: 汇率 %s 不在 1～50 之间", ErrRateRejected, money.FromNano(next))
	}
	if current > 0 {
		change := float64(next-current) / float64(current)
		if change > MaxAutoRateChange || change < -MaxAutoRateChange {
			return fmt.Errorf("%w: 新汇率 %s 与当前 %s 相差超过 20%%，请人工确认后手动填写", ErrRateRejected, money.FromNano(next), money.FromNano(current))
		}
	}
	return nil
}

// FrankfurterURL serves the European Central Bank reference rates, published
// on TARGET working days around 16:00 CET; on other days it returns the latest
// published rate.
const FrankfurterURL = "https://api.frankfurter.dev/v1/latest?base=USD&symbols=CNY"

// FrankfurterSource reads the USD/CNY ECB reference rate from Frankfurter.
type FrankfurterSource struct {
	URL    string
	Client *http.Client
}

func NewFrankfurterSource() *FrankfurterSource {
	return &FrankfurterSource{URL: FrankfurterURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *FrankfurterSource) FetchUSDCNY(ctx context.Context) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := f.Client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("汇率源请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("汇率源返回 HTTP %d", response.StatusCode)
	}
	var body struct {
		Base  string                 `json:"base"`
		Rates map[string]json.Number `json:"rates"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body); err != nil {
		return 0, fmt.Errorf("汇率源响应无法解析: %w", err)
	}
	value, ok := body.Rates["CNY"]
	if body.Base != "USD" || !ok {
		return 0, errors.New("汇率源响应缺少 USD/CNY 汇率")
	}
	rate, err := money.Parse(value.String())
	if err != nil {
		return 0, fmt.Errorf("汇率源返回的汇率无效: %s", value)
	}
	return rate.Nano(), nil
}
