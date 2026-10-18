package typical

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Service has one worker, one coalesced request and no inference dependency.
// Old immutable estimates survive failures. A failed job cannot publish partial
// or version-mismatched input, and ingestion/checkpoints commit atomically.
type Service struct {
	repo    Repository
	source  Source
	request chan struct{}
	running atomic.Bool
	current atomic.Pointer[Snapshot]
	prices  atomic.Pointer[costIndex]
	mu      sync.Mutex
	status  Status
	now     func() time.Time
}

func New(ctx context.Context, repo Repository, source Source) (*Service, error) {
	if repo == nil || source == nil {
		return nil, ErrInvalid
	}
	s := &Service{repo: repo, source: source, request: make(chan struct{}, 1), now: func() time.Time { return time.Now().UTC() }}
	config, err := repo.Config(ctx)
	if err == nil && Validate(config) != nil {
		return nil, ErrInvalid
	}
	if err == nil {
		previous, loadErr := repo.LoadSnapshot(ctx)
		err = loadErr
		if err == nil && previous != nil {
			in, sourceErr := source.Current(ctx)
			err = sourceErr
			if err == nil && previous.Version == "typical.v1" && previous.InputVersion == InputVersion(in, config) {
				s.install(previous, in)
			}
		}
	}
	// Derived statistics may start unknown during an outage. The bounded worker
	// retries; failure to warm an estimate must not disable actual settlement.
	if err != nil {
		s.status.LastError = "update_failed"
	}
	s.Notify()
	return s, nil
}
func (s *Service) Config(ctx context.Context) (Config, error) { return s.repo.Config(ctx) }
func (s *Service) Update(ctx context.Context, actor string, c Config) (Config, error) {
	if Validate(c) != nil {
		return Config{}, ErrInvalid
	}
	next, err := s.repo.SetConfig(ctx, actor, c)
	if err == nil {
		s.Notify()
	}
	return next, err
}
func (s *Service) Notify() {
	select {
	case s.request <- struct{}{}:
	default:
	}
	s.mu.Lock()
	s.status.Pending = true
	s.mu.Unlock()
}
func (s *Service) Trigger() error {
	select {
	case s.request <- struct{}{}:
		s.mu.Lock()
		s.status.Pending = true
		s.mu.Unlock()
		return nil
	default:
		return ErrBusy
	}
}

// Snapshot is immutable after publication. Callers must not mutate its slices.
func (s *Service) Snapshot() (*Snapshot, Status) {
	s.mu.Lock()
	status := s.status
	s.mu.Unlock()
	status.Running = s.running.Load()
	return s.current.Load(), status
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.request:
		case <-tick.C:
			c, err := s.repo.Config(ctx)
			if err != nil {
				continue
			}
			if s.now().Sub(last) < time.Duration(c.IntervalSeconds)*time.Second {
				continue
			}
		}
		run, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = s.Step(run)
		cancel()
		last = s.now()
	}
}
func (s *Service) Step(ctx context.Context) (err error) {
	if !s.running.CompareAndSwap(false, true) {
		return ErrBusy
	}
	s.mu.Lock()
	s.status.LastAttempt = s.now()
	s.status.Pending = false
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.status.LastError = ""
		s.status.Saturated = errors.Is(err, ErrSaturated)
		if err != nil {
			switch {
			case errors.Is(err, ErrSaturated):
				s.status.LastError = "work_limit_reached"
			case errors.Is(err, ErrStale), errors.Is(err, ErrConflict):
				s.status.LastError = "inputs_changed"
			case errors.Is(err, context.DeadlineExceeded):
				s.status.LastError = "update_timeout"
			default:
				s.status.LastError = "update_failed"
			}
		}
		s.mu.Unlock()
		s.running.Store(false)
	}()
	c, err := s.repo.Config(ctx)
	if err != nil {
		return err
	}
	in, err := s.source.Current(ctx)
	if err != nil {
		return err
	}
	at := s.now()
	floor := at.AddDate(0, 0, -c.WindowDays)
	// Pruning is indexed and bounded; it precedes capacity checks so shortening
	// the window or natural expiry can recover a saturated worker.
	for range ScanPages {
		if err = s.repo.Prune(ctx, floor, PageSize); err != nil {
			return err
		}
	}
	cp, err := s.repo.Checkpoint(ctx)
	if err != nil {
		return err
	}
	if cp.WindowDays != c.WindowDays {
		cp.WindowDays = c.WindowDays
		cp.RepairFloor = time.Time{}
		cp.RepairCeiling = time.Time{}
		cp.RepairedAt = time.Time{}
	}
	if cp.Delta.At.Before(floor) {
		cp.Delta = Cursor{At: floor}
	}
	for range ScanPages / 2 {
		rows, e := s.repo.Calls(ctx, Scan{After: cp.Delta, Floor: floor, Ceiling: at}, PageSize)
		if e != nil {
			return e
		}
		if len(rows) > PageSize {
			return ErrInvalid
		}
		samples := []Sample{}
		for _, raw := range rows {
			if sample, ok := Decode(raw); ok {
				samples = append(samples, sample)
			}
			cp.Delta = Cursor{At: raw.CompletedAt, ID: raw.ID}
		}
		if len(rows) < PageSize {
			cp.Delta = Cursor{At: at, ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}
		}
		if err = s.repo.Ingest(ctx, cp, samples); err != nil {
			return err
		}
		cp.Generation++
		if len(rows) < PageSize {
			break
		}
	}
	// A rolling indexed reconciliation catches settlements whose transaction
	// committed after the delta waterline. Upserts make repair/restart idempotent.
	if cp.RepairCeiling.IsZero() && (cp.RepairedAt.IsZero() || at.Sub(cp.RepairedAt) >= 24*time.Hour) {
		cp.RepairFloor = floor
		cp.RepairCeiling = at
		cp.Repair = Cursor{At: floor}
	}
	if !cp.RepairCeiling.IsZero() {
		for range ScanPages / 2 {
			rows, e := s.repo.Calls(ctx, Scan{After: cp.Repair, Floor: cp.RepairFloor, Ceiling: cp.RepairCeiling}, PageSize)
			if e != nil {
				return e
			}
			if len(rows) > PageSize {
				return ErrInvalid
			}
			samples := []Sample{}
			for _, raw := range rows {
				if sample, ok := Decode(raw); ok {
					samples = append(samples, sample)
				}
				cp.Repair = Cursor{At: raw.CompletedAt, ID: raw.ID}
			}
			done := len(rows) < PageSize
			if done {
				cp.RepairedAt = at
				cp.RepairFloor = time.Time{}
				cp.RepairCeiling = time.Time{}
			}
			if err = s.repo.Ingest(ctx, cp, samples); err != nil {
				return err
			}
			cp.Generation++
			if done {
				break
			}
		}
	}
	samples := []Sample{}
	after := Cursor{At: floor}
	size := 0
	for {
		page, e := s.repo.Samples(ctx, floor, after, PageSize)
		if e != nil {
			return e
		}
		for _, sample := range page {
			raw, e := json.Marshal(sample)
			if e != nil {
				return e
			}
			size += len(raw)
			if size > MaxBytes || len(samples) >= MaxSamples {
				return ErrSaturated
			}
			samples = append(samples, sample)
			after = Cursor{At: sample.CompletedAt, ID: sample.ID}
		}
		if len(page) < PageSize {
			break
		}
	}
	snap, err := Calculate(ctx, in, c, samples, at)
	if err != nil {
		return err
	}
	snap.Watermark = cp.Delta.At
	snap.ReconciledAt = cp.RepairedAt
	latest, err := s.source.Current(ctx)
	if err != nil {
		return err
	}
	config, err := s.repo.Config(ctx)
	if err != nil {
		return err
	}
	if snap.InputVersion != InputVersion(latest, config) {
		return ErrStale
	}
	if err = s.repo.SaveSnapshot(ctx, snap); err != nil {
		return err
	}
	// Reads also carry the immutable quote/config/waterline provenance. F10
	// matches its current candidate/rate against this snapshot without SQL.
	s.install(snap, in)
	return nil
}
