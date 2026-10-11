package gatewaybench

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestNameOfReadsTheSqlcNameOrTheFirstWords(t *testing.T) {
	for sql, want := range map[string]string{
		"-- name: LookupKey :one\nSELECT 1":    "LookupKey",
		"  -- name: FinishCall :execrows\nUPD": "FinishCall",
		"begin":                                "(begin)",
		"SELECT   a,\n  b FROM t":              "(SELECT a, b FROM t)",
	} {
		if got := NameOf(sql); got != want {
			t.Errorf("NameOf(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestCounterSeparatesBackgroundWorkAndReportsDeltas(t *testing.T) {
	counter := NewQueryCounter()
	trace := func(ctx context.Context, sql string) {
		started := counter.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql})
		counter.TraceQueryEnd(started, nil, pgx.TraceQueryEndData{})
	}
	trace(context.Background(), "-- name: A :one\nselect")
	before, _ := counter.Counts()
	trace(context.Background(), "-- name: A :one\nselect")
	trace(context.Background(), "-- name: B :exec\nupdate")
	trace(Background(context.Background()), "-- name: Refresh :one\nselect")
	requests, background := counter.Counts()
	if got := Since(requests, before); !reflect.DeepEqual(got, map[string]int64{"A": 1, "B": 1}) || Total(got) != 2 {
		t.Fatalf("delta = %v", got)
	}
	if !reflect.DeepEqual(background, map[string]int64{"Refresh": 1}) {
		t.Fatalf("background = %v", background)
	}
	if got := Names(map[string]int64{"x": 1, "y": 5, "a": 5}); !reflect.DeepEqual(got, []string{"a", "y", "x"}) {
		t.Fatalf("names = %v", got)
	}
	// Only the statements of requests are timed, and every one of them is.
	timings := counter.Timings()
	if timings["A"].Count != 2 || timings["B"].Count != 1 || len(timings) != 2 {
		t.Fatalf("timings = %v", timings)
	}
	if timings["A"].Mean() > timings["A"].Max || (Timing{}).Mean() != 0 {
		t.Fatalf("mean %v max %v", timings["A"].Mean(), timings["A"].Max)
	}
}
