package gateway

import (
	"slices"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func reserveEngine() (*Engine, time.Time) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	return &Engine{Dependencies: Dependencies{Runtime: NewRuntime(nil), Now: func() time.Time { return now }}}, now
}

// single is a candidate that takes one request at a time.
func single(id string) Candidate {
	one := int32(1)
	return Candidate{ChannelID: id, Advanced: channel.Advanced{ConcurrencyLimit: &one}}
}

// walk runs the candidates through reservations the way Serve does and returns
// the channels attempted. Each attempt is released before the next one.
func walk(e *Engine, ordered []Candidate, maxAttempts int) []string {
	var tried []string
	for slot := range e.reservations(ordered, maxAttempts, settings.Settings{}) {
		tried = append(tried, slot.candidate.ChannelID)
		slot.release()
	}
	return tried
}

func TestReservationsSkipACandidateWhoseSlotIsTakenAndBackfill(t *testing.T) {
	e, now := reserveEngine()
	ordered := []Candidate{single("a"), single("b"), single("c")}
	// Another request holds the only slot of the first candidate.
	release, ok, _ := e.Runtime.Begin("a", EffectiveLimits(ordered[0].Advanced, settings.Settings{}), now)
	if !ok {
		t.Fatal("setup: could not take a's slot")
	}
	defer release()

	if got := walk(e, ordered, 2); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("tried %v, want [b c]: a is skipped and does not use up an attempt", got)
	}
	if got := walk(e, ordered, 1); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("tried %v, want [b] with max_attempts 1", got)
	}
}

func TestReservationsStopAtMaxAttemptsWhenEveryoneIsFree(t *testing.T) {
	e, _ := reserveEngine()
	ordered := []Candidate{single("a"), single("b"), single("c")}
	if got := walk(e, ordered, 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("tried %v, want [a b]", got)
	}
	if got := walk(e, ordered, 10); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("tried %v, want every candidate when max_attempts exceeds them", got)
	}
}

func TestReservationsBackfillWhenALaterSlotIsTakenMidWalk(t *testing.T) {
	e, now := reserveEngine()
	ordered := []Candidate{single("a"), single("b"), single("c"), single("d")}
	var tried []string
	for slot := range e.reservations(ordered, 3, settings.Settings{}) {
		tried = append(tried, slot.candidate.ChannelID)
		if slot.candidate.ChannelID == "a" {
			// While a is being tried, another request takes b's only slot.
			if _, ok, _ := e.Runtime.Begin("b", slot.limits, now); !ok {
				t.Fatal("setup: could not take b's slot")
			}
		}
		slot.release()
	}
	if !slices.Equal(tried, []string{"a", "c", "d"}) {
		t.Fatalf("tried %v, want [a c d]", tried)
	}
}

func TestReservationsYieldNothingWhenNoSlotCanBeTaken(t *testing.T) {
	e, now := reserveEngine()
	ordered := []Candidate{single("a"), single("b")}
	for _, candidate := range ordered {
		release, ok, _ := e.Runtime.Begin(candidate.ChannelID, EffectiveLimits(candidate.Advanced, settings.Settings{}), now)
		if !ok {
			t.Fatalf("setup: could not take %s's slot", candidate.ChannelID)
		}
		defer release()
	}
	if got := walk(e, ordered, 3); len(got) != 0 {
		t.Fatalf("tried %v, want none (Serve then answers no_channel_available)", got)
	}
}

func TestReservationsHoldTheSlotUntilReleasedAndStopWhenTheCallEnds(t *testing.T) {
	e, now := reserveEngine()
	ordered := []Candidate{single("a"), single("b")}
	limits := EffectiveLimits(ordered[0].Advanced, settings.Settings{})
	for slot := range e.reservations(ordered, 2, settings.Settings{}) {
		if state, _ := e.Runtime.Check(slot.candidate.ChannelID, limits, now); state != StateLimited {
			t.Fatalf("%s is %s while its attempt runs, want limited", slot.candidate.ChannelID, state)
		}
		slot.release()
		if state, _ := e.Runtime.Check(slot.candidate.ChannelID, limits, now); state != StateAvailable {
			t.Fatalf("%s is %s after release, want available", slot.candidate.ChannelID, state)
		}
		break // the call is answered
	}
	// Ending the walk early never takes the next candidate's slot.
	if state, _ := e.Runtime.Check("b", limits, now); state != StateAvailable {
		t.Fatalf("b is %s, want untouched", state)
	}
}
