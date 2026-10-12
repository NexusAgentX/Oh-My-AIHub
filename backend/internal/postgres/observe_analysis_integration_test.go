package postgres_test

import (
	"context"
	"net/url"
	"testing"
	"time"
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
