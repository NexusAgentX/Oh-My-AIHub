package postgres_test

import (
	"context"
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

func TestObserveAnalysisSharedFiltersAndDrill(t *testing.T) {
	o := newObserved(t)
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	window := "?from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(to)
	params := "&stream=false&min_attempts=2&status_code=500&attempt_channel_id=" + o.brokenID + "&tag=suite&billed=true"
	list := o.get(o.consumer, "/api/calls"+window+params)
	report := o.get(o.consumer, "/api/calls/analysis"+window+params+"&group_by=channel&split_by=model")
	current := asMap(t, report["current"])
	if current["calls"] != float64(len(asList(t, list["items"]))) || current["calls"] != float64(1) || current["retried"] != float64(1) || current["recovered"] != float64(1) || current["attempts"] != float64(2) || current["first_succeeded"] != float64(0) || current["ttft_p95"] != nil {
		t.Fatalf("report=%v list=%v", report, list)
	}
	if current["delivery_known"] != float64(1) || current["usage_reported"] != float64(1) || current["charged"] != expectedCharge {
		t.Fatalf("accounting/delivery=%v", current)
	}
	group := asMap(t, asList(t, report["groups"])[0])
	if group["key"] != o.goodID {
		t.Fatalf("final channel %v", group)
	}
	drill := o.get(o.consumer, "/api/calls"+window+params+"&dimension=channel&dimension_value="+o.goodID+"&second_dimension=model&second_value=gpt-test")
	if len(asList(t, drill["items"])) != 1 {
		t.Fatal("drill differs")
	}
	// Both predicates must match the same attempt, not different channels of this call.
	bad := o.get(o.consumer, "/api/calls/analysis"+window+"&status_code=500&attempt_channel_id="+o.goodID)
	if asMap(t, bad["current"])["calls"] != float64(0) {
		t.Fatal("cross-attempt filter match")
	}
	private := o.get(o.outsider, "/api/calls/analysis"+window)
	if asMap(t, private["current"])["calls"] != float64(0) {
		t.Fatal("account leak")
	}
	waitForSettlementTiming(t, o.pool, o.fellBack)
	detail := asMap(t, o.get(o.consumer, "/api/calls/"+o.fellBack)["call"])
	diagnostics := asMap(t, detail["diagnostics"])
	stages := asList(t, diagnostics["stages"])
	found := 0
	for _, s := range stages {
		if asMap(t, s)["stage"] == "settlement" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("settlement stage %v", stages)
	}
	entries := o.get(o.consumer, "/api/points/entries?transaction_id="+detail["ledger_transaction_id"].(string))
	if len(asList(t, entries["items"])) != 1 {
		t.Fatal("transaction filter")
	}
	// Historical unmeasured diagnostics must not be interpreted as failed delivery.
	if _, err := o.pool.Exec(context.Background(), "UPDATE calls SET diagnostics=NULL WHERE id=$1", o.fellBack); err != nil {
		t.Fatal(err)
	}
	old := o.get(o.consumer, "/api/calls/analysis"+window+params)
	if asMap(t, old["current"])["delivery_known"] != float64(0) || asMap(t, old["current"])["settlement_p95"] != nil {
		t.Fatal("historical unknown treated as zero")
	}
	if len(asList(t, report["trend"])) < 2 {
		t.Fatal("empty buckets missing")
	}
}

func TestObserveAnalysisRawPercentilesPeriodsAndStageDuplicates(t *testing.T) {
	e := newEnv(t)
	owner := e.member("analysis", "0")
	ctx := context.Background()
	from := time.Now().UTC().Truncate(time.Hour)
	to := from.Add(time.Hour)
	previousFrom := from.Add(-time.Hour)
	_, err := e.pool.Exec(ctx, `INSERT INTO calls
	 (account_id,format,requested_model,tag,created_at,outcome,stream,duration_ms,ttft_ms,attempts,diagnostics)
	 SELECT $1,'openai_chat',model,tag,at,'succeeded',stream,duration,duration/10,attempts::jsonb,diagnostics::jsonb
	 FROM (VALUES
	  ('alpha','keep',$4::timestamptz-interval '1 second',true,9000,'[]',NULL),
	  ('alpha','keep',$4::timestamptz,true,1000,'[]','{"stages":[{"stage":"authentication","duration_ms":10}]}'),
	  ('alpha','keep',$2::timestamptz-interval '1 second',true,2000,'[]','{"stages":[{"stage":"authentication","duration_ms":20}]}'),
	  ('alpha','keep',$2::timestamptz,true,100,'[{"end_reason":"upstream_error","duration_ms":10},{"end_reason":"completed","duration_ms":70}]','{"stages":[{"stage":"authentication","duration_ms":1},{"stage":"authentication","duration_ms":999},{"stage":"request","duration_ms":null},{"stage":"request","duration_ms":999}]}'),
	  ('alpha','keep',$2::timestamptz+interval '1 minute',true,300,'[]','{"stages":[{"stage":"authentication","duration_ms":3}]}'),
	  ('beta','keep',$2::timestamptz+interval '2 minutes',false,500,'[{"end_reason":"completed","duration_ms":450}]',NULL),
	  ('beta','keep',$2::timestamptz+interval '3 minutes',true,700,'[{"end_reason":"completed","duration_ms":600},{"end_reason":"completed","duration_ms":50}]','{"stages":[{"stage":"authentication","duration_ms":5}]}'),
	  ('beta','other',$2::timestamptz+interval '4 minutes',true,9000,'[]',NULL),
	  ('beta','other',$2::timestamptz-interval '4 minutes',true,9000,'[]',NULL),
	  ('beta','keep',$3::timestamptz,true,9000,'[]',NULL)
	 ) AS fixture(model,tag,at,stream,duration,attempts,diagnostics)`, owner.id, from, to, previousFrom)
	if err != nil {
		t.Fatal(err)
	}
	filter := observe.CallFilter{AccountID: owner.id, Tag: "keep", From: &from, To: &to}
	report, err := e.observe.Analysis(ctx, filter, "model", "stream", 3600)
	if err != nil {
		t.Fatal(err)
	}
	v := report.Current
	if v.Calls != 4 || report.Previous.Calls != 2 || v.Attempts != 5 || v.AttemptSucceeded != 4 || v.FirstSucceeded != 2 || v.Retried != 2 || v.Recovered != 2 {
		t.Fatalf("current=%+v previous=%+v", v, report.Previous)
	}
	near := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-8 {
			t.Fatalf("%s=%v want %v", name, got, want)
		}
	}
	near("duration p50", v.DurationP50, 400)
	near("duration p95", v.DurationP95, 670)
	near("ttft p95", v.TtftP95, 66)
	near("authentication p95", v.AuthenticationP95, 4.8)
	near("fallback p95", v.FallbackP95, 570.5)
	near("overhead p95", v.OverheadP95, 262.5)
	near("previous authentication p95", report.Previous.AuthenticationP95, 19.5)
	if v.RequestP95 != nil || v.SettlementP95 != nil {
		t.Fatalf("unknown/first-null stages became measured: %+v", v)
	}
	if len(report.Groups) != 3 || report.Groups[0].Key != "alpha" || report.Groups[0].SplitKey != "true" || report.Groups[0].Values.Calls != 2 || len(report.Trend) != 1 || report.Trend[0].Values.Calls != 4 {
		t.Fatalf("groups=%+v trend=%+v", report.Groups, report.Trend)
	}
	near("trend p95", report.Trend[0].Values.DurationP95, 670)
	// A dimension that produces an empty key still has a real group.
	all, err := e.observe.Analysis(ctx, filter, "all", "all", 3600)
	if err != nil || len(all.Groups) != 1 || all.Groups[0].Key != "" || all.Groups[0].Values.Calls != 4 {
		t.Fatalf("all groups=%+v err=%v", all.Groups, err)
	}
	filter.Tag = "absent"
	empty, err := e.observe.Analysis(ctx, filter, "model", "all", 3600)
	if err != nil || empty.Current.Calls != 0 || empty.Previous.Calls != 0 || empty.Current.DurationP95 != nil || len(empty.Groups) != 0 || len(empty.Trend) != 1 || empty.Trend[0].Values.DurationP95 != nil {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}
