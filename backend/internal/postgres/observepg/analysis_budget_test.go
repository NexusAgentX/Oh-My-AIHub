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
				done <- s.withAnalysisBudget(context.Background(), func(context.Context) error { <-release; return nil })
			}()
		}
		synctest.Wait()
		if err := s.withAnalysisBudget(context.Background(), func(context.Context) error { t.Error("excess analysis started"); return nil }); !errors.Is(err, observe.ErrAnalysisBusy) {
			t.Fatalf("busy=%v", err)
		}
		close(release)
		<-done
		<-done
		want := errors.New("query failed")
		if err := s.withAnalysisBudget(context.Background(), func(context.Context) error { return want }); !errors.Is(err, want) {
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
		err := s.withAnalysisBudget(context.Background(), func(ctx context.Context) error {
			for i := 0; i < 4; i++ {
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
		if !errors.Is(err, observe.ErrAnalysisTimeout) || time.Since(start) != 3*time.Second || len(s.analysisSlots) != 0 {
			t.Fatalf("elapsed=%v error=%v slots=%d", time.Since(start), err, len(s.analysisSlots))
		}
	})
}
