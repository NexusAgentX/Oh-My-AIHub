// Package typicalpg persists disposable numeric estimates on a private pool.
package typicalpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }
func bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}
func config(row TypicalSetting) typical.Config {
	return typical.Config{Version: row.Version, WindowDays: int(row.WindowDays), MinSamples: int(row.MinSamples), IntervalSeconds: int(row.IntervalSeconds), TypicalTokens: row.TypicalTokens, TypicalRequests: row.TypicalRequests, TypicalImages: row.TypicalImages}
}
func (s *Store) Config(ctx context.Context) (typical.Config, error) {
	ctx, cancel := bounded(ctx)
	defer cancel()
	row, err := s.q.GetTypicalSettings(ctx)
	return config(row), err
}
func (s *Store) SetConfig(ctx context.Context, actor string, value typical.Config) (typical.Config, error) {
	if typical.Validate(value) != nil {
		return typical.Config{}, typical.ErrInvalid
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	var out typical.Config
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := s.q.WithTx(tx).UpdateTypicalSettings(ctx, UpdateTypicalSettingsParams{Version: value.Version, WindowDays: int32(value.WindowDays), MinSamples: int32(value.MinSamples), IntervalSeconds: int32(value.IntervalSeconds), TypicalTokens: value.TypicalTokens, TypicalRequests: value.TypicalRequests, TypicalImages: value.TypicalImages})
		if errors.Is(err, pgx.ErrNoRows) {
			return typical.ErrConflict
		}
		if err != nil {
			return err
		}
		out = config(row)
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: audit.ActionSettingsUpdated, TargetType: "typical_cost", TargetID: "platform", Detail: map[string]any{"settings": out}})
	})
	return out, err
}
func (s *Store) Checkpoint(ctx context.Context) (typical.Checkpoint, error) {
	ctx, cancel := bounded(ctx)
	defer cancel()
	row, err := s.q.GetTypicalState(ctx)
	if err != nil {
		return typical.Checkpoint{}, err
	}
	var cp typical.Checkpoint
	if err = json.Unmarshal(row.Checkpoint, &cp); err != nil {
		return cp, err
	}
	cp.Generation = row.Generation
	return cp, nil
}
func cursor(c typical.Cursor) typical.Cursor {
	if c.ID == "" {
		c.ID = "00000000-0000-0000-0000-000000000000"
	}
	return c
}
func (s *Store) Calls(ctx context.Context, scan typical.Scan, limit int) ([]typical.RawCall, error) {
	if limit < 1 || limit > typical.PageSize {
		return nil, typical.ErrInvalid
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	after := cursor(scan.After)
	rows, err := s.q.ScanTypicalCalls(ctx, ScanTypicalCallsParams{WindowFloor: scan.Floor, WindowCeiling: scan.Ceiling, AfterAt: after.At, AfterID: after.ID, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := []typical.RawCall{}
	for _, r := range rows {
		if r.CompletedAt == nil {
			return nil, typical.ErrInvalid
		}
		out = append(out, typical.RawCall{ID: r.ID, ModelID: r.ModelID, Format: r.Format, Outcome: r.Outcome, UsageStatus: r.UsageStatus, StartedAt: r.CreatedAt, CompletedAt: *r.CompletedAt, Usage: ledger.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheWriteTokens: r.CacheWriteTokens, CacheReadTokens: r.CacheReadTokens}, Snapshot: r.Snapshot})
	}
	return out, nil
}
func (s *Store) Ingest(ctx context.Context, cp typical.Checkpoint, samples []typical.Sample) error {
	if len(samples) > typical.PageSize {
		return typical.ErrInvalid
	}
	type item struct {
		CallID      string          `json:"call_id"`
		CompletedAt time.Time       `json:"completed_at"`
		Profile     json.RawMessage `json:"profile"`
	}
	rows := make([]item, 0, len(samples))
	for _, sample := range samples {
		var err error
		sample, err = typical.CleanSample(sample)
		if err != nil {
			return err
		}
		sample.CompletedAt = sample.CompletedAt.Truncate(time.Microsecond)
		raw, err := json.Marshal(sample)
		if err != nil {
			return err
		}
		if len(raw) > 64<<10 {
			return typical.ErrSaturated
		}
		rows = append(rows, item{sample.ID, sample.CompletedAt, raw})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	checkpoint, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		state, err := q.LockTypicalState(ctx)
		if err != nil {
			return err
		}
		if state.Generation != cp.Generation {
			return typical.ErrConflict
		}
		if err = q.IngestTypicalSamples(ctx, raw); err != nil {
			return err
		}
		count, err := q.CountTypicalSamples(ctx)
		if err != nil {
			return err
		}
		if count > typical.MaxSamples {
			return typical.ErrSaturated
		}
		return q.UpdateTypicalCheckpoint(ctx, checkpoint)
	})
}
func (s *Store) Samples(ctx context.Context, floor time.Time, after typical.Cursor, limit int) ([]typical.Sample, error) {
	if limit < 1 || limit > typical.PageSize {
		return nil, typical.ErrInvalid
	}
	after = cursor(after)
	ctx, cancel := bounded(ctx)
	defer cancel()
	rows, err := s.q.ListTypicalSamples(ctx, ListTypicalSamplesParams{WindowFloor: floor, AfterAt: after.At, AfterID: after.ID, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := []typical.Sample{}
	for _, row := range rows {
		var sample typical.Sample
		if len(row.Profile) > 64<<10 || json.Unmarshal(row.Profile, &sample) != nil || sample.ID != row.CallID || !sample.CompletedAt.Equal(row.CompletedAt) {
			return nil, typical.ErrInvalid
		}
		out = append(out, sample)
	}
	return out, nil
}
func (s *Store) Prune(ctx context.Context, floor time.Time, limit int) error {
	if limit < 1 || limit > typical.PageSize {
		return typical.ErrInvalid
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	return s.q.PruneTypicalSamples(ctx, PruneTypicalSamplesParams{CompletedAt: floor, Limit: int32(limit)})
}
func (s *Store) LoadSnapshot(ctx context.Context) (*typical.Snapshot, error) {
	ctx, cancel := bounded(ctx)
	defer cancel()
	raw, err := s.q.LoadTypicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var snap typical.Snapshot
	if len(raw) > 8<<20 || json.Unmarshal(raw, &snap) != nil {
		return nil, typical.ErrInvalid
	}
	return &snap, nil
}
func (s *Store) SaveSnapshot(ctx context.Context, snap *typical.Snapshot) error {
	if snap == nil {
		return typical.ErrInvalid
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return typical.ErrSaturated
	}
	ctx, cancel := bounded(ctx)
	defer cancel()
	n, err := s.q.SaveTypicalSnapshot(ctx, SaveTypicalSnapshotParams{Snapshot: raw, Version: snap.Config.Version})
	if err != nil {
		return err
	}
	if n != 1 {
		return typical.ErrStale
	}
	return nil
}
