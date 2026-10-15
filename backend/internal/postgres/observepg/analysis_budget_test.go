package observepg

import (
	"context"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"testing"
	"testing/synctest"
	"time"
)

func TestAnalysisBudgetRejectsExcessWorkAndReleasesSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewStore(nil, nil)
		release := make(chan struct{})
		done := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				done <- s.withAnalysisBudget(context.Background(), 15*time.Second, func(context.Context) error { <-release; return nil })
			}()
		}
		synctest.Wait()
		if err := s.withAnalysisBudget(context.Background(), 15*time.Second, func(context.Context) error { t.Error("excess analysis started"); return nil }); !errors.Is(err, observe.ErrAnalysisBusy) {
			t.Fatalf("busy=%v", err)
		}
		close(release)
		<-done
		<-done
		want := errors.New("query failed")
		if err := s.withAnalysisBudget(context.Background(), 15*time.Second, func(context.Context) error { return want }); !errors.Is(err, want) {
			t.Fatal(err)
		}
		if len(s.analysisSlots) != 0 {
			t.Fatal("slot leaked after error")
		}
	})
}
func TestAnalysisBudgetBoundsWholeReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewStore(nil, nil)
		start := time.Now()
		err := s.withAnalysisBudget(context.Background(), 15*time.Second, func(ctx context.Context) error {
			for i := 0; i < 4; i++ {
				select {
				case <-time.After(4 * time.Second):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
		if !errors.Is(err, observe.ErrAnalysisTimeout) || time.Since(start) != 15*time.Second || len(s.analysisSlots) != 0 {
			t.Fatalf("elapsed=%v error=%v slots=%d", time.Since(start), err, len(s.analysisSlots))
		}
	})
}

func TestAnalysisBudgetAllowsLongerReportAndHonorsCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		work     time.Duration
		cancelAt time.Duration
	}{
		{name: "completes before fifteen seconds", work: 14 * time.Second},
		{name: "caller cancels early", work: 14 * time.Second, cancelAt: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := NewStore(nil, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.cancelAt > 0 {
					time.AfterFunc(tc.cancelAt, cancel)
				}
				start := time.Now()
				err := s.withAnalysisBudget(ctx, 15*time.Second, func(ctx context.Context) error {
					select {
					case <-time.After(tc.work):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				wantDuration := tc.work
				var wantErr error
				if tc.cancelAt > 0 {
					wantDuration, wantErr = tc.cancelAt, context.Canceled
				}
				if !errors.Is(err, wantErr) || time.Since(start) != wantDuration || len(s.analysisSlots) != 0 {
					t.Fatalf("elapsed=%v error=%v slots=%d", time.Since(start), err, len(s.analysisSlots))
				}
			})
		})
	}
}

func TestDirectoryAnalysisSharesExistingBudget(t *testing.T) {
	s := NewStore(nil, nil)
	s.analysisSlots <- struct{}{}
	s.analysisSlots <- struct{}{}
	_, err := s.DirectoryAnalysis(context.Background(), observe.DirectoryFilter{})
	if !errors.Is(err, observe.ErrAnalysisBusy) {
		t.Fatalf("directory bypassed shared budget: %v", err)
	}
}
