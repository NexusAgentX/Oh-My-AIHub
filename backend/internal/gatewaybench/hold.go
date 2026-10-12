package gatewaybench

import (
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// A hold is the time a database lock stays taken, as the client sees it: from
// the answer to the statement that took the lock until the answer to the
// commit (or rollback) of the transaction. In between are the statements the
// transaction still sends and the pauses of the client between them; every one
// of those is time during which every other transaction that wants the lock
// waits. WatchHold turns the measurement on for the statement that takes the
// lock (the settlement's LockLedgerAccounts, the row lock of platform_revenue
// among them), and Holds reads it.
//
// The figure is slightly short of the server's: the lock is taken inside the
// statement, half a round trip before the client hears of it, and released
// inside the commit, half a round trip before the client hears of that.

// HoldStep is what the transactions did in their holds under one statement
// name: the round trips, and the pauses of the client before them.
type HoldStep struct {
	Count    int64
	Duration time.Duration // the round trips, as the client saw them
	Gap      time.Duration // the client's own time before the statements: encoding, scheduling, the work between two statements
}

// HoldStats is the holds of one measurement window.
type HoldStats struct {
	Count   int64
	Total   time.Duration
	Samples []time.Duration // every hold, in the order they ended
	Steps   map[string]HoldStep
}

// Mean is the average hold.
func (h HoldStats) Mean() time.Duration {
	if h.Count == 0 {
		return 0
	}
	return h.Total / time.Duration(h.Count)
}

// Percentile is the p-th percentile of the holds (0 < p <= 100).
func (h HoldStats) Percentile(p float64) time.Duration {
	if len(h.Samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), h.Samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted))*p/100+0.5) - 1
	return sorted[min(max(index, 0), len(sorted)-1)]
}

// Statements is the number of statements a hold holds on average, the commit included.
func (h HoldStats) Statements() float64 {
	if h.Count == 0 {
		return 0
	}
	var n int64
	for _, step := range h.Steps {
		n += step.Count
	}
	return float64(n) / float64(h.Count)
}

// Names returns the statement names of the steps, the ones that took the most time first.
func (h HoldStats) Names() []string {
	names := make([]string, 0, len(h.Steps))
	for name := range h.Steps {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := h.Steps[names[i]], h.Steps[names[j]]
		if a.Duration+a.Gap != b.Duration+b.Gap {
			return a.Duration+a.Gap > b.Duration+b.Gap
		}
		return names[i] < names[j]
	})
	return names
}

// holdTracker follows the open holds, one per connection (a transaction owns
// its connection). Its methods run under the counter's lock.
type holdTracker struct {
	watch string
	open  map[*pgx.Conn]*openHold
	stats HoldStats
}

type openHold struct {
	started, lastEnd time.Time
	steps            map[string]HoldStep
}

// end records that the statement name, begun at begun, has been answered at now.
func (t *holdTracker) end(conn *pgx.Conn, name string, begun, now time.Time) {
	if t.watch == "" {
		return
	}
	hold := t.open[conn]
	if hold == nil {
		if name == t.watch {
			t.open[conn] = &openHold{started: now, lastEnd: now, steps: map[string]HoldStep{}}
		}
		return
	}
	step := hold.steps[name]
	step.Count++
	step.Duration += now.Sub(begun)
	step.Gap += max(begun.Sub(hold.lastEnd), 0)
	hold.steps[name] = step
	hold.lastEnd = now
	if name != "(commit)" && name != "(rollback)" {
		return
	}
	delete(t.open, conn)
	elapsed := now.Sub(hold.started)
	t.stats.Count++
	t.stats.Total += elapsed
	t.stats.Samples = append(t.stats.Samples, elapsed)
	if t.stats.Steps == nil {
		t.stats.Steps = map[string]HoldStep{}
	}
	for stepName, step := range hold.steps {
		total := t.stats.Steps[stepName]
		total.Count += step.Count
		total.Duration += step.Duration
		total.Gap += step.Gap
		t.stats.Steps[stepName] = total
	}
}

// WatchHold starts measuring the holds of the lock that the statement named
// lockStatement (an sqlc query name) takes. Call it before the load starts.
func (c *QueryCounter) WatchHold(lockStatement string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holds.watch = lockStatement
	c.holds.open = map[*pgx.Conn]*openHold{}
}

// Holds is a copy of everything measured so far; HoldsSince cuts a window out of two copies.
func (c *QueryCounter) Holds() HoldStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := HoldStats{Count: c.holds.stats.Count, Total: c.holds.stats.Total, Samples: append([]time.Duration(nil), c.holds.stats.Samples...), Steps: map[string]HoldStep{}}
	for name, step := range c.holds.stats.Steps {
		out.Steps[name] = step
	}
	return out
}

// HoldsSince is what was measured after before (a copy taken earlier).
func HoldsSince(now, before HoldStats) HoldStats {
	out := HoldStats{Count: now.Count - before.Count, Total: now.Total - before.Total, Steps: map[string]HoldStep{}}
	if len(now.Samples) >= len(before.Samples) {
		out.Samples = now.Samples[len(before.Samples):]
	}
	for name, step := range now.Steps {
		was := before.Steps[name]
		if step.Count == was.Count {
			continue
		}
		out.Steps[name] = HoldStep{Count: step.Count - was.Count, Duration: step.Duration - was.Duration, Gap: step.Gap - was.Gap}
	}
	return out
}
