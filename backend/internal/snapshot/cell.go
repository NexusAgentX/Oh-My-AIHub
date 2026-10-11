package snapshot

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// loadTimeout abandons a rebuild whose query hangs.
const loadTimeout = 30 * time.Second

// cell holds one immutable snapshot and knows when it is out of date.
//
// Every change to the data behind it calls invalidate after its transaction
// has committed. invalidate counts the change (want) and rebuilds before it
// returns, so the writer's caller sees the new data in every later read. A
// reader checks the count first: a snapshot older than the latest change is
// never served, it is rebuilt on the spot, or the read fails when that is not
// possible (a database error). A stale answer is therefore never given for a
// change this process has made; only a change made behind its back (a manual
// SQL statement, a migration) waits for the periodic rebuild.
//
// A rebuild remembers the count it started at (gen). Its queries run after
// that point, so they see every change counted up to it. A change counted
// later makes the snapshot out of date again and is picked up by the next
// rebuild, which also repairs a snapshot that read a half-applied change.
type cell[T any] struct {
	name string
	load func(context.Context) (T, error)

	cur   atomic.Pointer[version[T]]
	want  atomic.Uint64 // changes counted so far
	loads atomic.Uint64 // rebuilds finished, for tests

	mu sync.Mutex // one rebuild at a time; the readers of a current snapshot never take it
}

type version[T any] struct {
	gen   uint64
	value T
}

func newCell[T any](name string, load func(context.Context) (T, error)) *cell[T] {
	return &cell[T]{name: name, load: load}
}

// get returns a snapshot that includes every change counted before the call.
// ctx only lends its values to a rebuild this call has to make.
func (c *cell[T]) get(ctx context.Context) (T, error) {
	target := c.want.Load()
	if cur := c.cur.Load(); cur != nil && cur.gen >= target {
		return cur.value, nil
	}
	cur, err := c.ensure(ctx, target)
	if err != nil {
		var none T
		return none, err
	}
	return cur.value, nil
}

// invalidate counts a change and returns once a snapshot including it is
// published. On error the change stays counted: reads keep trying to rebuild
// and fail until one succeeds.
func (c *cell[T]) invalidate(ctx context.Context) error {
	_, err := c.ensure(ctx, c.want.Add(1))
	return err
}

// refresh rebuilds unconditionally; a failure leaves the current snapshot. It
// is the periodic rebuild, so it ends with ctx.
func (c *cell[T]) refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.reload(ctx)
	return err
}

// ensure publishes a snapshot with at least the given generation. Callers
// that queue behind a rebuild which started late enough share its result. The
// rebuild does not end with the context of whoever asked: it serves everyone,
// and one cancelled request must not leave the others without a snapshot.
func (c *cell[T]) ensure(ctx context.Context, target uint64) (*version[T], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur := c.cur.Load(); cur != nil && cur.gen >= target {
		return cur, nil
	}
	return c.reload(context.WithoutCancel(ctx))
}

// reload rebuilds and publishes; c.mu is held.
func (c *cell[T]) reload(ctx context.Context) (*version[T], error) {
	gen := c.want.Load()
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	value, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	next := &version[T]{gen: gen, value: value}
	c.cur.Store(next)
	c.loads.Add(1)
	return next, nil
}

// stale reports whether a change is counted that the published snapshot does
// not include, for tests.
func (c *cell[T]) stale() bool {
	cur := c.cur.Load()
	return cur == nil || cur.gen < c.want.Load()
}
