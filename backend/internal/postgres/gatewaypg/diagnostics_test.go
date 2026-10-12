package gatewaypg

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

func TestDiagnosticQueueNeverBlocksAndCountsDrops(t *testing.T) {
	d := newDiagnosticWriter()
	for i := 0; i < diagnosticQueueSize+7; i++ {
		d.offer(settlementTiming{ID: "call"})
	}
	if len(d.queue) != diagnosticQueueSize || d.dropped.Load() != 7 {
		t.Fatalf("queued=%d dropped=%d", len(d.queue), d.dropped.Load())
	}
}

func TestDiagnosticWriterBatchesFlushesAndDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDiagnosticWriter()
		ctx, cancel := context.WithCancel(context.Background())
		var batches []int
		done := make(chan struct{})
		go func() {
			defer close(done)
			d.run(ctx, func(ctx context.Context, b []settlementTiming) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("unbounded write")
				}
				batches = append(batches, len(b))
				return nil
			})
		}()
		for i := 0; i < diagnosticBatchSize+3; i++ {
			d.offer(settlementTiming{ID: "call"})
		}
		synctest.Wait()
		if len(batches) != 1 || batches[0] != diagnosticBatchSize {
			t.Fatalf("batches %v", batches)
		}
		time.Sleep(diagnosticFlushInterval)
		synctest.Wait()
		if len(batches) != 2 || batches[1] != 3 {
			t.Fatalf("timer batches %v", batches)
		}
		d.offer(settlementTiming{ID: "last"})
		cancel()
		<-done
		if len(batches) != 3 || batches[2] != 1 || d.failed.Load() != 0 {
			t.Fatalf("shutdown batches %v failed %d", batches, d.failed.Load())
		}
		d.offer(settlementTiming{ID: "late"})
		if d.dropped.Load() != 1 {
			t.Fatal("late sample not accounted")
		}
	})
}

func TestDiagnosticFailureHasNoRetryAndShutdownHasOneBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDiagnosticWriter()
		for i := 0; i < diagnosticQueueSize; i++ {
			d.offer(settlementTiming{ID: "call"})
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		d.run(ctx, func(ctx context.Context, _ []settlementTiming) error { <-ctx.Done(); return ctx.Err() })
		if time.Since(start) > diagnosticWriteTimeout {
			t.Fatal("shutdown exceeded total budget")
		}
		if d.failed.Load() != diagnosticQueueSize || len(d.queue) != 0 {
			t.Fatalf("failed %d remaining %d", d.failed.Load(), len(d.queue))
		}
	})
	synctest.Test(t, func(t *testing.T) {
		d := newDiagnosticWriter()
		d.offer(settlementTiming{ID: "call"})
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			d.run(ctx, func(context.Context, []settlementTiming) error { attempts++; return errors.New("unavailable") })
		}()
		time.Sleep(3 * diagnosticFlushInterval)
		synctest.Wait()
		cancel()
		<-done
		if attempts != 1 || d.failed.Load() != 1 {
			t.Fatalf("attempts=%d failed=%d", attempts, d.failed.Load())
		}
	})
}

func TestEncodeDiagnosticsPreservesUnknownAndAccumulatesRetry(t *testing.T) {
	raw, previous, err := encodeDiagnostics(nil)
	if raw != nil || previous != 0 || err != nil {
		t.Fatalf("nil encoded as %s", raw)
	}
	original := &gateway.Diagnostics{Stages: []gateway.StageTiming{{Stage: "routing", DurationMS: 2}, {Stage: "settlement", DurationMS: 8}, {Stage: "pricing", DurationMS: 1}}}
	raw, previous, err = encodeDiagnostics(original)
	if err != nil {
		t.Fatal(err)
	}
	var stored gateway.Diagnostics
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if previous != 8 || len(stored.Stages) != 2 || stored.Stages[1].Stage != "pricing" || len(original.Stages) != 3 || original.Stages[1].Stage != "settlement" {
		t.Fatalf("stored=%+v original=%+v previous=%v", stored, original, previous)
	}
}
