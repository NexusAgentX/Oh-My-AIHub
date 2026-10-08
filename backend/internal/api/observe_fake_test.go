package api

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// fakeObserveStore answers the few observation queries the Feature A tests
// reach; every other query panics through the nil embedded interface, which
// the PostgreSQL integration tests cover instead.
type fakeObserveStore struct {
	observe.Store
	exported []observe.ExportEntry
}

func (fakeObserveStore) UserPeriod(context.Context, string, time.Time, time.Time) (money.Amount, money.Amount, []observe.TypeFlow, error) {
	return 0, 0, nil, nil
}

func (fakeObserveStore) UserTrendInputs(context.Context, string, time.Time, time.Time) (money.Amount, []observe.DayFlow, error) {
	return 0, nil, nil
}

func (fakeObserveStore) EntrySummary(context.Context, observe.EntryFilter, bool, bool) (observe.EntrySummary, error) {
	return observe.EntrySummary{}, nil
}

func (s fakeObserveStore) ExportEntries(context.Context, observe.EntryFilter, int) ([]observe.ExportEntry, error) {
	return s.exported, nil
}
