package gatewaybench

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// statement sends one statement through the tracer as a pool connection would,
// taking about d to be answered.
func statement(counter *QueryCounter, conn *pgx.Conn, sql string, d time.Duration) {
	started := counter.TraceQueryStart(context.Background(), conn, pgx.TraceQueryStartData{SQL: sql})
	time.Sleep(d)
	counter.TraceQueryEnd(started, conn, pgx.TraceQueryEndData{})
}

func TestHoldsRunFromTheLockStatementToTheCommitOfItsConnection(t *testing.T) {
	counter := NewQueryCounter()
	counter.WatchHold("Lock")
	first, second := &pgx.Conn{}, &pgx.Conn{}

	// Work before the lock, on another connection in between, and the
	// statements after the lock are all that count.
	statement(counter, first, "begin", 0)
	statement(counter, first, "-- name: Before :exec\nupdate", 0)
	statement(counter, first, "-- name: Lock :many\nselect", 0)
	before := counter.Holds()
	statement(counter, second, "-- name: Unrelated :one\nselect", 0)
	statement(counter, first, "-- name: Write :exec\nupdate", 2*time.Millisecond)
	statement(counter, first, "-- name: Write :exec\nupdate", 2*time.Millisecond)
	time.Sleep(3 * time.Millisecond) // the client's own time before the commit
	statement(counter, first, "commit", 4*time.Millisecond)

	holds := HoldsSince(counter.Holds(), before)
	if holds.Count != 1 || len(holds.Samples) != 1 {
		t.Fatalf("holds = %+v, want one", holds)
	}
	if got := holds.Mean(); got < 11*time.Millisecond || got > 200*time.Millisecond {
		t.Fatalf("hold = %v, want about 11ms", got)
	}
	if holds.Percentile(50) != holds.Samples[0] || holds.Percentile(99) != holds.Samples[0] {
		t.Fatalf("percentiles %v %v of %v", holds.Percentile(50), holds.Percentile(99), holds.Samples)
	}
	if _, counted := holds.Steps["Before"]; counted {
		t.Fatalf("a statement before the lock is part of the hold: %+v", holds.Steps)
	}
	if _, counted := holds.Steps["Unrelated"]; counted {
		t.Fatalf("a statement of another connection is part of the hold: %+v", holds.Steps)
	}
	write, commit := holds.Steps["Write"], holds.Steps["(commit)"]
	if write.Count != 2 || write.Duration < 4*time.Millisecond {
		t.Fatalf("write step = %+v", write)
	}
	if commit.Count != 1 || commit.Duration < 4*time.Millisecond || commit.Gap < 3*time.Millisecond {
		t.Fatalf("commit step = %+v (its gap is the client's time before it)", commit)
	}
	if got := holds.Statements(); got != 3 {
		t.Fatalf("statements per hold = %v, want 3 (two writes and the commit)", got)
	}
	if names := holds.Names(); len(names) != 2 || names[0] != "(commit)" && names[0] != "Write" {
		t.Fatalf("names = %v", names)
	}
}

func TestAHoldEndsWithARollbackAndNothingIsMeasuredUnlessWatched(t *testing.T) {
	counter := NewQueryCounter()
	conn := &pgx.Conn{}
	statement(counter, conn, "-- name: Lock :many\nselect", 0)
	statement(counter, conn, "commit", 0)
	if holds := counter.Holds(); holds.Count != 0 {
		t.Fatalf("an unwatched counter measured %+v", holds)
	}

	counter.WatchHold("Lock")
	statement(counter, conn, "-- name: Lock :many\nselect", 0)
	statement(counter, conn, "rollback", 0)
	statement(counter, conn, "-- name: Lock :many\nselect", 0)
	statement(counter, conn, "commit", 0)
	if holds := counter.Holds(); holds.Count != 2 || holds.Steps["(rollback)"].Count != 1 || holds.Steps["(commit)"].Count != 1 {
		t.Fatalf("holds = %+v, want one rolled back and one committed", holds)
	}
	// A background statement is not a request's and does not join a hold.
	statement(counter, conn, "-- name: Lock :many\nselect", 0)
	started := counter.TraceQueryStart(Background(context.Background()), conn, pgx.TraceQueryStartData{SQL: "-- name: Refresh :one\nselect"})
	counter.TraceQueryEnd(started, conn, pgx.TraceQueryEndData{})
	statement(counter, conn, "commit", 0)
	if holds := counter.Holds(); holds.Count != 3 || holds.Steps["Refresh"].Count != 0 {
		t.Fatalf("holds = %+v", holds)
	}
}
