package postgres_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// Channel edits clear anomaly evidence before returning and after a restart.
// The diagnostic flag changes; the configured manual order remains unchanged.

// anomalyPair is the usual setup: the cheaper channel answers every request
// with a 400, the dearer one is fine, and both serve the same model.
type anomalyPair struct {
	e                   *env
	secret              string
	lastCall            string
	owner               person
	brokenID, healthyID string
	broken, healthy     *scriptedUpstream
}

func newAnomalyPair(t *testing.T) *anomalyPair {
	t.Helper()
	p := &anomalyPair{e: newEnv(t)}
	consumer := p.e.member("consumer", "1000")
	p.owner = p.e.member("sharer", "0")
	p.broken = newScriptedUpstream(t, 0, overloadedBody)
	p.broken.status.Store(http.StatusBadRequest)
	p.healthy = newScriptedUpstream(t, 0, overloadedBody)
	p.brokenID = p.e.createChannel(p.owner, channelSpec{name: "broken", upstream: p.broken.upstream, multiplier: "1"})
	p.healthyID = p.e.createChannel(p.owner, channelSpec{name: "healthy", upstream: p.healthy.upstream, multiplier: "2"})
	_, p.secret = p.e.defaultKey(consumer)
	p.e.selectFixtureChannels(consumer)
	return p
}

func (p *anomalyPair) call() int {
	response := p.e.call(p.secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	p.lastCall = response.Header().Get("X-AIHub-Request-Id")
	return response.Code
}

// breakAndFlag sends the 20 calls the cheaper channel needs to be judged and
// publishes the statistics; the channel is then last.
func (p *anomalyPair) breakAndFlag() {
	p.e.t.Helper()
	for range 20 {
		if code := p.call(); code != http.StatusBadRequest {
			p.e.t.Fatalf("call = %d, want the cheaper channel's 400", code)
		}
	}
	p.e.engine.RefreshStats()
	p.expectFirst("healthy", "after the 4xx answers")
}

// expectFirst makes one call and checks which channel took it.
func (p *anomalyPair) expectFirst(name, when string) {
	p.e.t.Helper()
	brokenBefore, healthyBefore := len(p.broken.inference()), len(p.healthy.inference())
	code := p.call()
	brokenGot, healthyGot := len(p.broken.inference())-brokenBefore, len(p.healthy.inference())-healthyBefore
	if brokenGot != 1 || healthyGot != 0 {
		p.e.t.Fatalf("%s: manual order changed: code %d healthy %d broken %d", when, code, healthyGot, brokenGot)
	}
	var flagged bool
	err := p.e.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM jsonb_array_elements(diagnostics->'routing') d WHERE d->'channel'->>'id'=$2 AND d->>'reason'='anomalous') FROM calls WHERE id=$1`, p.lastCall, p.brokenID).Scan(&flagged)
	if err != nil || flagged != (name == "healthy") {
		p.e.t.Fatalf("%s: anomaly=%v want=%v err=%v", when, flagged, name == "healthy", err)
	}

}

// fix makes the cheaper channel's upstream answer well, as if its owner had
// corrected the configuration the 400 came from.
func (p *anomalyPair) fix() { p.broken.status.Store(0) }

// Editing the channel ends the demotion before the edit returns, with no refresh
// in between; the old answers do not come back at a restart, while a channel
// nobody edited is still judged on them after one.
func TestEditingABrokenChannelEndsItsDemotionAndStaysEndedAfterARestart(t *testing.T) {
	p := newAnomalyPair(t)
	p.breakAndFlag()

	// A restart without an edit judges the channel on the same answers.
	p.e.restartGateway()
	p.expectFirst("healthy", "after a restart, nothing edited")

	// The owner corrects the upstream key. The next call already goes to the
	// cheaper channel again: no refresh ran since the edit.
	p.fix()
	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"api_key": "sk-corrected-0002"})
	p.expectFirst("broken", "right after the edit")

	// Neither the next refresh nor a restart brings the old answers back; the
	// calls are all in the database, 20 of them 4xx.
	p.e.engine.RefreshStats()
	p.expectFirst("broken", "after a refresh")
	p.e.restartGateway()
	p.expectFirst("broken", "after a restart")
	if p.e.callsTableHoldsAttemptsOf(p.brokenID) < 20 {
		t.Fatal("the 4xx calls should still be in the calls table")
	}
	if got := eventReasons(p.e, p.brokenID, channel.EventAnomalousClientErrors); len(got) != 1 {
		t.Fatalf("events of the broken channel = %v, want the one from before the edit", got)
	}
}

// Each way of changing a channel counts as an edit.
func TestEveryKindOfChannelEditEndsTheDemotion(t *testing.T) {
	edits := []struct {
		name string
		edit func(p *anomalyPair)
	}{
		{"upstream key", func(p *anomalyPair) {
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"api_key": "sk-other-0003"})
		}},
		{"name", func(p *anomalyPair) {
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"name": "broken, renamed"})
		}},
		{"models", func(p *anomalyPair) {
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{
				"models": []any{map[string]any{"model_id": "gpt-test", "upstream_model": "right-name", "multiplier": "1"}},
			})
		}},
		{"unlist and list again", func(p *anomalyPair) {
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"status": "unlisted"})
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"status": "listed"})
		}},
		{"probe results applied", func(p *anomalyPair) {
			p.e.apiJSON(p.owner, http.StatusOK, http.MethodPost, "/api/channels/"+p.brokenID+"/test", map[string]any{"apply": true})
		}},
		{"administrator suspends and restores", func(p *anomalyPair) {
			p.e.apiJSON(p.e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+p.brokenID+"/suspend", map[string]any{"reason": "looking into it"})
			p.e.apiJSON(p.e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+p.brokenID+"/unsuspend", map[string]any{"reason": "looked"})
		}},
	}
	for _, tc := range edits {
		t.Run(tc.name, func(t *testing.T) {
			p := newAnomalyPair(t)
			p.breakAndFlag()
			p.fix()
			tc.edit(p)
			p.expectFirst("broken", "right after the edit")
			p.e.restartGateway()
			p.expectFirst("broken", "after a restart")
		})
	}
}

// Probing without applying the results changes nothing about the channel, and
// editing another channel changes nothing about this one.
func TestAnEditThatDoesNotChangeTheChannelLeavesItDemoted(t *testing.T) {
	p := newAnomalyPair(t)
	p.breakAndFlag()
	p.fix()

	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPost, "/api/channels/"+p.brokenID+"/test", map[string]any{"apply": false})
	p.expectFirst("healthy", "after a probe that only recorded its results")

	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.healthyID, map[string]any{"name": "healthy, renamed"})
	p.expectFirst("healthy", "after the other channel was edited")

	p.e.restartGateway()
	p.expectFirst("healthy", "after a restart")

	// And the edit that does change it lifts it.
	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"name": "broken, renamed"})
	p.expectFirst("broken", "after the edit")
}

// If the edit did not fix anything, 20 fresh 4xx answers flag the channel again
// and its owner is told once more, however recent the first event was.
func TestAChannelStillBrokenAfterAnEditIsFlaggedAgain(t *testing.T) {
	p := newAnomalyPair(t)
	p.breakAndFlag()

	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"name": "broken, still"})
	for range 19 {
		if code := p.call(); code != http.StatusBadRequest {
			t.Fatalf("call = %d, want the cheaper channel's 400 again", code)
		}
	}
	p.e.engine.RefreshStats()
	if got := eventReasons(p.e, p.brokenID, channel.EventAnomalousClientErrors); len(got) != 1 {
		t.Fatalf("events after 19 fresh attempts = %v", got)
	}
	if code := p.call(); code != http.StatusBadRequest {
		t.Fatalf("call 20 = %d", code)
	}
	p.e.engine.RefreshStats()
	if got := eventReasons(p.e, p.brokenID, channel.EventAnomalousClientErrors); len(got) != 2 || got[1] != "gpt-test: 4xx 20/20 (100%)" {
		t.Fatalf("events after 20 fresh attempts = %v, want a second one counting only the fresh attempts", got)
	}
	p.expectFirst("healthy", "after the second judgment")
}

// The warm-up reads the edit time from the channel row: the attempts of calls
// that began before it are reported apart, and the channel's health still
// counts them.
func TestAttemptHistoryReportsWhatHappenedBeforeTheLastEdit(t *testing.T) {
	p := newAnomalyPair(t)
	for range 20 {
		p.call()
	}
	history := func() (counted, clientErrors, beforeAttempts, beforeClientErrors int64) {
		since := time.Unix((time.Now().Unix()/300-287)*300, 0)
		rows, err := p.e.store.Gateway.AttemptHistory(context.Background(), since)
		if err != nil {
			p.e.t.Fatal(err)
		}
		for _, row := range rows {
			if row.ChannelID == p.brokenID {
				counted += row.Counted
				clientErrors += row.ClientErrors
				beforeAttempts += row.BeforeEditAttempts
				beforeClientErrors += row.BeforeEditClientErrors
			}
		}
		return
	}
	// The channel was created before the calls: nothing precedes its last edit.
	if counted, client, before, beforeClient := history(); counted != 0 || client != 20 || before != 0 || beforeClient != 0 {
		t.Fatalf("before any edit: counted %d, 4xx %d, before the edit %d (4xx %d)", counted, client, before, beforeClient)
	}
	p.e.apiJSON(p.owner, http.StatusOK, http.MethodPatch, "/api/channels/"+p.brokenID, map[string]any{"name": "broken, edited"})
	p.fix()
	for range 3 {
		if code := p.call(); code != http.StatusOK {
			t.Fatalf("call after the edit = %d", code)
		}
	}
	counted, client, before, beforeClient := history()
	if counted != 3 || client != 20 || before != 20 || beforeClient != 20 {
		t.Fatalf("after the edit: counted %d, 4xx %d, before the edit %d (4xx %d), want 3, 20, 20, 20", counted, client, before, beforeClient)
	}
}
