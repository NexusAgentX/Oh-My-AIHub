package gatewaypg

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

const diagnosticQueueSize = 2048
const diagnosticBatchSize = 128
const diagnosticFlushInterval = 250 * time.Millisecond
const diagnosticWriteTimeout = 2 * time.Second

type settlementTiming struct {
	ID         string
	DurationMS float64
}

// Only post-commit timing is best-effort. Call facts and ledger entries have
// already committed atomically; this queue never participates in accounting.
type diagnosticWriter struct {
	queue   chan settlementTiming
	dropped atomic.Uint64
	failed  atomic.Uint64
	stopped atomic.Bool
}

func newDiagnosticWriter() *diagnosticWriter {
	return &diagnosticWriter{queue: make(chan settlementTiming, diagnosticQueueSize)}
}
func (d *diagnosticWriter) offer(item settlementTiming) {
	if d.stopped.Load() {
		d.dropped.Add(1)
		return
	}
	select {
	case d.queue <- item:
	default:
		d.dropped.Add(1)
	}
}
func (s *Store) DiagnosticDropped() uint64 { return s.diagnostics.dropped.Load() }
func (s *Store) DiagnosticFailed() uint64  { return s.diagnostics.failed.Load() }

// RunDiagnostics has exactly one consumer. Cancellation drains the finite queue
// within one bounded shutdown budget; callers must stop producers before waiting.
func (s *Store) RunDiagnostics(ctx context.Context) { s.diagnostics.run(ctx, s.writeSettlements) }
func (d *diagnosticWriter) run(ctx context.Context, write func(context.Context, []settlementTiming) error) {
	ticker := time.NewTicker(diagnosticFlushInterval)
	defer ticker.Stop()
	batch := make([]settlementTiming, 0, diagnosticBatchSize)
	flush := func(parent context.Context) {
		if len(batch) == 0 {
			return
		}
		bounded, cancel := context.WithTimeout(parent, diagnosticWriteTimeout)
		err := write(bounded, batch)
		cancel()
		if err != nil {
			d.failed.Add(uint64(len(batch)))
		}
		clear(batch)
		batch = batch[:0]
	}
	for {
		select {
		case item := <-d.queue:
			batch = append(batch, item)
			if len(batch) == diagnosticBatchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			d.stopped.Store(true)
			shutdown, cancel := context.WithTimeout(context.Background(), diagnosticWriteTimeout)
			defer cancel()
			for drained := 0; drained < diagnosticQueueSize; drained++ {
				select {
				case item := <-d.queue:
					batch = append(batch, item)
					if len(batch) == diagnosticBatchSize {
						flush(shutdown)
					}
				default:
					flush(shutdown)
					return
				}
			}
			flush(shutdown)
			return
		}
	}
}
func (s *Store) writeSettlements(ctx context.Context, batch []settlementTiming) error {
	p := SetCallSettlementsParams{Ids: make([]string, 0, len(batch)), Durations: make([]float64, 0, len(batch))}
	for _, item := range batch {
		p.Ids = append(p.Ids, item.ID)
		p.Durations = append(p.Durations, item.DurationMS)
	}
	return s.q.SetCallSettlements(ctx, p)
}

// A failed settlement can be retried by the engine. Persist only final timing,
// including earlier attempts, without mutating the engine's telemetry sample.
func encodeDiagnostics(d *gateway.Diagnostics) ([]byte, float64, error) {
	if d == nil {
		return nil, 0, nil
	}
	value := *d
	if value.Stages == nil {
		value.Stages = []gateway.StageTiming{}
	}
	var previous float64
	for i, stage := range d.Stages {
		if stage.Stage == "settlement" {
			previous += stage.DurationMS
			value.Stages = make([]gateway.StageTiming, 0, len(d.Stages)-1)
			value.Stages = append(value.Stages, d.Stages[:i]...)
			value.Stages = append(value.Stages, d.Stages[i+1:]...)
			break // the engine accumulates into one stage per name
		}
	}
	raw, err := json.Marshal(value)
	return raw, previous, err
}
