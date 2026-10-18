package groupchannel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

type countedRepository struct {
	Repository
	data  Data
	reads int
	err   error
}

func (s *countedRepository) Snapshot(context.Context) (Data, error) { s.reads++; return s.data, s.err }

type cacheBase struct {
	snapshot.Loader
	model catalog.Model
}

func (cacheBase) LoadKeys(context.Context) ([]gateway.KeyRecord, error)             { return nil, nil }
func (cacheBase) LoadCandidates(context.Context) ([]gateway.CandidateRecord, error) { return nil, nil }
func (b cacheBase) LoadModels(context.Context) ([]catalog.Model, error) {
	return []catalog.Model{b.model}, nil
}
func (cacheBase) LoadRoutePrefs(context.Context) ([]routing.Scoped, error) { return nil, nil }
func (cacheBase) LoadPoints(context.Context, []string) (map[string]ledger.Points, error) {
	return nil, nil
}

func TestGatewayGroupReadsDoNotReloadConfigurationAndFailedInvalidationCloses(t *testing.T) {
	d, data := fixture()
	repo := &countedRepository{data: data}
	r := &Runtime{Repository: repo}
	ctx := context.Background()
	if err := r.Install(ctx, d); err != nil {
		t.Fatal(err)
	}
	cache := snapshot.New(snapshot.Config{Loader: r.Wrap(cacheBase{model: catalog.Model{ID: d.Models[0].ModelID, Enabled: false}}), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.Rebuild = func(ctx context.Context) error {
		return cache.InvalidateChecked(ctx, snapshot.DomainFleet|snapshot.DomainModels)
	}
	if err := cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	source := cache.Gateway(nil)
	first, err := source.Candidates(ctx, d.Models[0].ModelID, channel.FormatOpenAIChat)
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	for range 100 {
		next, err := source.Candidates(ctx, d.Models[0].ModelID, channel.FormatOpenAIChat)
		model, e := cache.Models().Get(ctx, d.Models[0].ModelID)
		if err != nil || e != nil || len(next) != 1 || !model.Enabled {
			t.Fatal("group offering required a legacy global listing", err, e)
		}
	}
	if repo.reads != 1 {
		t.Fatalf("ordinary candidate reads performed configuration I/O: %d", repo.reads)
	}
	d.Groups[0].Revision++
	d.Groups[0].ConnectionVersion++
	d.Quotes[0].Prices.Input = 3 * money.Amount(money.Scale)
	repo.err = errors.New("synthetic configuration read failure")
	if err := r.Install(ctx, d); err == nil {
		t.Fatal("publication ignored rebuild failure")
	}
	if stale, err := source.Candidates(ctx, d.Models[0].ModelID, channel.FormatOpenAIChat); err == nil || len(stale) > 0 {
		t.Fatal("invalidated snapshot fell back to old configuration")
	}
	repo.err = nil
	next, err := source.Candidates(ctx, d.Models[0].ModelID, channel.FormatOpenAIChat)
	if err != nil || len(next) != 1 || next[0].Quotes.Offers[d.Models[0].ModelID].Quote.Prices.Input != 3*money.Amount(money.Scale) {
		t.Fatal("recovery did not use committed new quote", err)
	}
	if first[0].Quotes.Offers[d.Models[0].ModelID].Quote.Prices.Input != money.Amount(money.Scale) {
		t.Fatal("in-flight candidate changed during rebuild")
	}
}
