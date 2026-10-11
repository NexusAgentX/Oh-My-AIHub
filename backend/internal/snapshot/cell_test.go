package snapshot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

// database stands for the stored data of one cell: a version that every
// committed change bumps, and a loader that reads it.
type database struct {
	version atomic.Int64
	reads   atomic.Int64
	fail    atomic.Bool
	// gate, when set, is received from once per load after the version was
	// read, to hold a load in flight with what it has read.
	gate chan struct{}
}

func (d *database) load(context.Context) (int64, error) {
	read := d.version.Load()
	d.reads.Add(1)
	if d.gate != nil {
		<-d.gate
	}
	if d.fail.Load() {
		return 0, errors.New("database down")
	}
	return read, nil
}

// commit is a write that has committed: its change is stored, but the cell
// does not know yet.
func (d *database) commit() int64 { return d.version.Add(1) }

func TestCellLoadsOnFirstReadAndServesItAfterwards(t *testing.T) {
	d := &database{}
	d.version.Store(7)
	c := newCell("test", d.load)
	for range 3 {
		value, err := c.get(ctx)
		if err != nil || value != 7 {
			t.Fatalf("get = %d, %v", value, err)
		}
	}
	if d.reads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", d.reads.Load())
	}
}

func TestCellInvalidateRebuildsBeforeItReturns(t *testing.T) {
	d := &database{}
	c := newCell("test", d.load)
	if _, err := c.get(ctx); err != nil {
		t.Fatal(err)
	}
	d.commit()
	if err := c.invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	// The rebuild is done: reading it does not load again.
	reads := d.reads.Load()
	if value, err := c.get(ctx); err != nil || value != 1 {
		t.Fatalf("get after invalidate = %d, %v", value, err)
	}
	if d.reads.Load() != reads {
		t.Fatal("the read after invalidate rebuilt again")
	}
}

func TestCellNeverServesASnapshotOlderThanACountedChange(t *testing.T) {
	d := &database{}
	c := newCell("test", d.load)
	if _, err := c.get(ctx); err != nil {
		t.Fatal(err)
	}
	// The rebuild after the change fails: the old snapshot must not be served.
	d.commit()
	d.fail.Store(true)
	if err := c.invalidate(ctx); err == nil {
		t.Fatal("invalidate hid the failed rebuild")
	}
	if !c.stale() {
		t.Fatal("a snapshot that missed a counted change is not stale")
	}
	if value, err := c.get(ctx); err == nil {
		t.Fatalf("get served %d from a snapshot that missed a change", value)
	}
	// Once the database is back, the next read rebuilds on its own.
	d.fail.Store(false)
	if value, err := c.get(ctx); err != nil || value != 1 {
		t.Fatalf("get after recovery = %d, %v", value, err)
	}
	if c.stale() {
		t.Fatal("still stale after a successful rebuild")
	}
}

func TestCellRefreshFailureKeepsTheCurrentSnapshot(t *testing.T) {
	d := &database{}
	d.version.Store(3)
	c := newCell("test", d.load)
	if _, err := c.get(ctx); err != nil {
		t.Fatal(err)
	}
	d.commit() // behind the cell's back: nothing counted it
	d.fail.Store(true)
	if err := c.refresh(ctx); err == nil {
		t.Fatal("refresh hid the failure")
	}
	if value, err := c.get(ctx); err != nil || value != 3 {
		t.Fatalf("get after a failed periodic refresh = %d, %v; want the previous snapshot", value, err)
	}
	d.fail.Store(false)
	if err := c.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if value, _ := c.get(ctx); value != 4 {
		t.Fatalf("get after refresh = %d, want the change made behind its back", value)
	}
}

// A rebuild that was already reading when the change committed must not be
// taken for one that includes it.
func TestCellRebuildInFlightDoesNotCoverALaterChange(t *testing.T) {
	d := &database{gate: make(chan struct{})}
	c := newCell("test", d.load)

	// The first rebuild starts and waits at the gate with the old data.
	first := make(chan error, 1)
	go func() { first <- c.invalidate(ctx) }()
	waitFor(t, "the first rebuild to start", func() bool { return d.reads.Load() == 1 })

	// Meanwhile a write commits and counts its change.
	d.commit()
	second := make(chan error, 1)
	go func() { second <- c.invalidate(ctx) }()
	waitFor(t, "the second change to be counted", func() bool { return c.want.Load() == 2 })

	// The first rebuild finishes. It started before the change was counted, so
	// the second invalidate has to rebuild again.
	d.gate <- struct{}{}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second rebuild to start", func() bool { return d.reads.Load() == 2 })
	d.gate <- struct{}{}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if value, err := c.get(ctx); err != nil || value != 1 {
		t.Fatalf("get = %d, %v; want the committed change", value, err)
	}
}

func TestCellConcurrentInvalidationsShareRebuilds(t *testing.T) {
	d := &database{}
	slow := func(ctx context.Context) (int64, error) {
		time.Sleep(20 * time.Millisecond)
		return d.load(ctx)
	}
	c := newCell("test", slow)
	const writers = 40
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			want := d.commit()
			if err := c.invalidate(ctx); err != nil {
				t.Error(err)
				return
			}
			if value, err := c.get(ctx); err != nil || value < want {
				t.Errorf("get = %d, %v after committing %d", value, err, want)
			}
		})
	}
	wg.Wait()
	if reads := d.reads.Load(); reads >= writers {
		t.Fatalf("%d rebuilds for %d concurrent changes: nothing was shared", reads, writers)
	}
}

// Writers commit and invalidate while readers read: a read that starts after
// an invalidate returned never sees anything older than that write.
func TestCellReadersNeverSeeAChangeUndone(t *testing.T) {
	d := &database{}
	c := newCell("test", d.load)
	if _, err := c.get(ctx); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var floor atomic.Int64 // the newest write whose invalidate has returned
	for range 8 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				minimum := floor.Load()
				value, err := c.get(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if value < minimum {
					t.Errorf("read %d after write %d was acknowledged", value, minimum)
					return
				}
			}
		})
	}
	var writers sync.WaitGroup
	for range 4 {
		writers.Go(func() {
			for range 50 {
				version := d.commit()
				if err := c.invalidate(ctx); err != nil {
					t.Error(err)
					return
				}
				for {
					current := floor.Load()
					if version <= current || floor.CompareAndSwap(current, version) {
						break
					}
				}
			}
		})
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
