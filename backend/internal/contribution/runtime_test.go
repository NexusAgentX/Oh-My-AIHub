package contribution

import (
	"context"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"testing"
)

func TestPublicationBarrierCannotBeClearedByBackgroundReload(t *testing.T) {
	s, repo, _ := metadataService(t)
	_ = s
	repo.live = market.Directory{Suppliers: []market.Supplier{{ID: "old"}}}
	calls := 0
	fail := false
	runtime := NewRuntime(memoryDirectory{repo}, func(context.Context, market.Directory) error {
		calls++
		if fail {
			return errors.New("synthetic install failure")
		}
		return nil
	})
	ctx := context.Background()
	if _, err := runtime.Current(); !errors.Is(err, ErrPublication) {
		t.Fatal("uninitialized directory usable")
	}
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := runtime.Current(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("request read caused rebuild")
	}
	runtime.Block()
	if err := runtime.Reload(ctx); !errors.Is(err, ErrPublication) || calls != 1 {
		t.Fatal("background reload cleared barrier")
	}
	runtime.Restore()
	if d, err := runtime.Current(); err != nil || d.Suppliers[0].ID != "old" {
		t.Fatal("rollback lost prior view")
	}
	runtime.Block()
	fail = true
	if err := runtime.Refresh(ctx); err == nil {
		t.Fatal("failed install accepted")
	}
	if _, err := runtime.Current(); !errors.Is(err, ErrPublication) {
		t.Fatal("committed but stale runtime was usable")
	}
	// A failed retry must preserve the earlier blocked state after rollback.
	runtime.Block()
	runtime.Restore()
	if _, err := runtime.Current(); !errors.Is(err, ErrPublication) {
		t.Fatal("nested retry accidentally unblocked")
	}
	fail = false
	repo.live.Suppliers[0].ID = "new"
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if d, err := runtime.Current(); err != nil || d.Suppliers[0].ID != "new" {
		t.Fatal("new atomic view missing")
	}
}
