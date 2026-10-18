package postgres_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// A channel that answers a model with a 4xx every time goes last (G4b). Once its
// owner has edited it, the 4xx answers it gave before are not held against it:
// the gateway ranks it afresh before the edit returns, and after a restart too.

// anomalyPair is the usual setup: the cheaper channel answers every request
// with a 400, the dearer one is fine, and both serve the same model.
type anomalyPair struct {
	e                   *env
	secret              string
	owner               person
	brokenID, healthyID string
	broken, healthy     *scriptedUpstream
}

func newAnomalyPair(t *testing.T) *anomalyPair {
	t.Helper()
	p := &anomalyPair{e: newGroupEnv(t)}
	consumer := p.e.member("consumer", "1000")
	p.owner = p.e.member("sharer", "0")
	p.broken = newScriptedUpstream(t, 0, overloadedBody)
	p.broken.status.Store(http.StatusBadRequest)
	p.healthy = newScriptedUpstream(t, 0, overloadedBody)
	p.brokenID = p.e.createChannel(p.owner, channelSpec{name: "broken", upstream: p.broken.upstream, multiplier: "1"})
	p.healthyID = p.e.createChannel(p.owner, channelSpec{name: "healthy", upstream: p.healthy.upstream, multiplier: "2"})
	_, p.secret = p.e.defaultKey(consumer)
	return p
}

func (p *anomalyPair) call() int {
	return p.e.call(p.secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code
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
	switch name {
	case "healthy":
		if code != http.StatusOK || healthyGot != 1 || brokenGot != 0 {
			p.e.t.Fatalf("%s: call = %d, healthy channel hit %d times, broken %d: want the healthy channel to go first", when, code, healthyGot, brokenGot)
		}
	case "broken":
		if brokenGot != 1 || healthyGot != 0 {
			p.e.t.Fatalf("%s: call = %d, healthy channel hit %d times, broken %d: want the cheaper channel to go first again", when, code, healthyGot, brokenGot)
		}
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
	p.e.replaceGroupedChannelKey(p.owner, p.brokenID, "sk-corrected-0002")
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
			p.e.replaceGroupedChannelKey(p.owner, p.brokenID, "sk-other-0003")
		}},
		{"name", func(p *anomalyPair) {
			p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"name": "broken, renamed"})
		}},
		{"model discount", func(p *anomalyPair) {
			current := p.e.apiJSON(p.owner, 200, "GET", "/api/group-channels/"+p.brokenID, nil)
			model := current["models"].([]any)[0].(map[string]any)
			p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"models": []any{map[string]any{"group_model_id": model["group_model_id"], "discount": "0.9"}}})
		}},
		{"unlist and list again", func(p *anomalyPair) {
			p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"status": "unlisted"})
			p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"status": "listed"})
		}},
		{"probe results applied", func(p *anomalyPair) {
			p.e.retestGroupedFixture(p.owner, p.brokenID)
		}},
		{"administrator suspends and restores", func(p *anomalyPair) {
			if reply := p.e.api(p.e.admin, "POST", "/api/admin/group-channels/"+p.brokenID+"/moderate", map[string]any{"reason": "looking into it"}); reply.Code != 204 {
				p.e.t.Fatal(reply.Code, reply.Body.String())
			}
			if reply := p.e.api(p.e.admin, "POST", "/api/admin/group-channels/"+p.brokenID+"/moderate", map[string]any{"reason": ""}); reply.Code != 204 {
				p.e.t.Fatal(reply.Code, reply.Body.String())
			}
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

// Editing another channel changes nothing about this one.
func TestEditingAnotherChannelLeavesThisChannelDemoted(t *testing.T) {
	p := newAnomalyPair(t)
	p.breakAndFlag()
	p.fix()

	p.e.patchGroupedChannel(p.owner, p.healthyID, map[string]any{"name": "healthy, renamed"})
	p.expectFirst("healthy", "after the other channel was edited")

	p.e.restartGateway()
	p.expectFirst("healthy", "after a restart")

	// And the edit that does change it lifts it.
	p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"name": "broken, renamed"})
	p.expectFirst("broken", "after the edit")
}

// If the edit did not fix anything, 20 fresh 4xx answers flag the channel again
// and its owner is told once more, however recent the first event was.
func TestAChannelStillBrokenAfterAnEditIsFlaggedAgain(t *testing.T) {
	p := newAnomalyPair(t)
	p.breakAndFlag()

	p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"name": "broken, still"})
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
	p.e.patchGroupedChannel(p.owner, p.brokenID, map[string]any{"name": "broken, edited"})
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
