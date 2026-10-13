package observe

import (
	"context"
	"errors"
	"testing"
	"time"
)

type directoryTestStore struct {
	Store
	reads int
}

func (s *directoryTestStore) DirectoryAnalysis(context.Context, DirectoryFilter) (DirectoryReport, error) {
	s.reads++
	return DirectoryReport{}, nil
}
func TestDirectoryTrendBudgetIncludesPartialBuckets(t *testing.T) {
	store := &directoryTestStore{}
	service := NewService(store)
	from := time.Date(2026, 1, 1, 0, 4, 59, 0, time.UTC)
	filter := DirectoryFilter{From: from, To: from.Add(998 * 300 * time.Second), Bucket: 300}
	if _, err := service.DirectoryAnalysis(context.Background(), filter); err != nil {
		t.Fatal(err)
	}
	filter.To = filter.To.Add(time.Second)
	if _, err := service.DirectoryAnalysis(context.Background(), filter); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("partial bucket bypassed budget: %v", err)
	}
	if store.reads != 1 {
		t.Fatal("oversized query reached persistence")
	}
}
