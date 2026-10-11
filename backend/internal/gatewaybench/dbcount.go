package gatewaybench

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueryCounter counts the statements a pgx pool sends, by the name sqlc gave
// the query (the "-- name: X :one" comment at the head of every generated
// statement). Set it as ConnConfig.Tracer of the pool under test. Every
// statement is one round trip to the database, begin and commit of a
// transaction included, so the counts are the round trips of a request.
//
// A statement issued under a context marked with Background (the engine's
// periodic refresh) is counted apart, because it does not belong to a request.
type QueryCounter struct {
	mu         sync.Mutex
	counts     map[string]int64
	background map[string]int64
	timings    map[string]*Timing
	holds      holdTracker
}

// Timing is how long the statements of one name took as the client saw it:
// the round trip and the server's work, not the wait for a pooled connection.
type Timing struct {
	Count      int64
	Total, Max time.Duration
}

// Mean is the average duration.
func (t Timing) Mean() time.Duration {
	if t.Count == 0 {
		return 0
	}
	return t.Total / time.Duration(t.Count)
}

// NewQueryCounter returns an empty counter.
func NewQueryCounter() *QueryCounter {
	return &QueryCounter{counts: map[string]int64{}, background: map[string]int64{}, timings: map[string]*Timing{}}
}

type backgroundKey struct{}

// Background marks ctx so that statements sent under it count as background work.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

var queryName = regexp.MustCompile(`^\s*--\s*name:\s*(\w+)`)

// NameOf returns the sqlc name of a statement, or the statement's first words
// when it has none (the begin and commit of a transaction, ad-hoc SQL).
func NameOf(sql string) string {
	if match := queryName.FindStringSubmatch(sql); match != nil {
		return match[1]
	}
	statement := strings.Join(strings.Fields(sql), " ")
	if len(statement) > 48 {
		statement = statement[:48]
	}
	return "(" + statement + ")"
}

type startedKey struct{}

type started struct {
	name       string
	at         time.Time
	background bool
}

// TraceQueryStart implements pgx.QueryTracer.
func (c *QueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	name := NameOf(data.SQL)
	background := ctx.Value(backgroundKey{}) != nil
	c.mu.Lock()
	if background {
		c.background[name]++
	} else {
		c.counts[name]++
	}
	c.mu.Unlock()
	return context.WithValue(ctx, startedKey{}, started{name: name, at: time.Now(), background: background})
}

// TraceQueryEnd implements pgx.QueryTracer.
func (c *QueryCounter) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, _ pgx.TraceQueryEndData) {
	begun, ok := ctx.Value(startedKey{}).(started)
	if !ok || begun.background {
		return
	}
	now := time.Now()
	elapsed := now.Sub(begun.at)
	c.mu.Lock()
	c.holds.end(conn, begun.name, begun.at, now)
	timing := c.timings[begun.name]
	if timing == nil {
		timing = &Timing{}
		c.timings[begun.name] = timing
	}
	timing.Count++
	timing.Total += elapsed
	timing.Max = max(timing.Max, elapsed)
	c.mu.Unlock()
}

// Timings is a copy of the durations of the statements of requests.
func (c *QueryCounter) Timings() map[string]Timing {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Timing, len(c.timings))
	for name, timing := range c.timings {
		out[name] = *timing
	}
	return out
}

// Counts is a copy of the counts: request work first, background work second.
func (c *QueryCounter) Counts() (requests, background map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	requests, background = map[string]int64{}, map[string]int64{}
	for name, n := range c.counts {
		requests[name] = n
	}
	for name, n := range c.background {
		background[name] = n
	}
	return requests, background
}

// Since returns what was counted after before (a copy taken earlier with
// Counts), names with no new statements left out.
func Since(now, before map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for name, n := range now {
		if delta := n - before[name]; delta != 0 {
			out[name] = delta
		}
	}
	return out
}

// Total adds up a set of counts.
func Total(counts map[string]int64) int64 {
	var sum int64
	for _, n := range counts {
		sum += n
	}
	return sum
}

// Names returns the names of a set of counts, most frequent first.
func Names(counts map[string]int64) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}
