package gateway

import (
	"context"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"testing"
	"time"
)

type sweepStore struct {
	Store
	cutoffs chan time.Time
}

func (s sweepStore) SweepStaleCalls(_ context.Context, before time.Time) (int64, error) {
	s.cutoffs <- before
	return 0, nil
}

// A call that is still in progress longer than the call time limit plus a
// minute can only be lost to a crash; the reaper must not touch a call that
// can still legitimately finish, so its horizon follows the limit.
func TestReaperHorizonIsTheCallTimeLimitPlusOneMinute(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		limit time.Duration
		want  time.Duration
	}{
		{"default", 0, 61 * time.Minute},
		{"injected", 90 * time.Second, 150 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := sweepStore{cutoffs: make(chan time.Time, 1)}
			engine := NewEngine(Dependencies{Store: store, Now: func() time.Time { return now }, CallTimeLimit: tc.limit})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { engine.RunReaper(ctx, time.Hour); close(done) }()
			cutoff := <-store.cutoffs
			cancel()
			<-done
			if got := now.Sub(cutoff); got != tc.want {
				t.Fatalf("reaper looks %s back, want %s", got, tc.want)
			}
		})
	}
	if callTimeLimit != 60*time.Minute {
		t.Fatalf("call time limit = %s, ADR-0035 says 60 minutes", callTimeLimit)
	}
}

func TestInterruptionOfBlamesTheChannelExceptForTheCallLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		code  string
		fault bool
	}{
		{"call limit", errCallDeadline, "call_deadline", false},
		{"idle", errIdle, "timeout_idle", true},
		{"total", errTotal, "timeout_total", true},
		{"broken connection", context.Canceled, "read_error", true},
		{"no cause", nil, "read_error", true},
		{"wrapped call limit", errors.Join(errors.New("read"), errCallDeadline), "call_deadline", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interruptionOf(tc.cause)
			if got.code != tc.code || got.channelFault != tc.fault || got.message == "" {
				t.Fatalf("interruption = %+v", got)
			}
		})
	}
}

func TestTimeoutStagesResolveIndependently(t *testing.T) {
	ms := func(value int32) *int32 { return &value }
	defaults := settings.Settings{DefaultTTFTTimeoutMS: 30000, DefaultIdleTimeoutMS: 600000, DefaultRequestTimeoutMS: 500000}
	for _, tc := range []struct {
		name     string
		pref     routing.Pref
		advanced channel.Advanced
		want     attemptTimeouts
	}{
		{name: "platform", want: attemptTimeouts{30 * time.Second, 600 * time.Second, 500 * time.Second}},
		{name: "model", pref: routing.Pref{TTFTTimeoutMS: ms(60000), IdleTimeoutMS: ms(2000), RequestTimeoutMS: ms(10000)}, want: attemptTimeouts{60 * time.Second, 2 * time.Second, 10 * time.Second}},
		{name: "channel", pref: routing.Pref{TTFTTimeoutMS: ms(60000), IdleTimeoutMS: ms(2000), RequestTimeoutMS: ms(10000)}, advanced: channel.Advanced{TTFTTimeoutMS: ms(15000), IdleTimeoutMS: ms(5000), RequestTimeoutMS: ms(20000)}, want: attemptTimeouts{15 * time.Second, 5 * time.Second, 20 * time.Second}},
		{name: "mixed sources", pref: routing.Pref{IdleTimeoutMS: ms(2000)}, advanced: channel.Advanced{TTFTTimeoutMS: ms(15000)}, want: attemptTimeouts{15 * time.Second, 2 * time.Second, 500 * time.Second}},
		{name: "channel without model", advanced: channel.Advanced{RequestTimeoutMS: ms(1000)}, want: attemptTimeouts{30 * time.Second, 600 * time.Second, time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTimeouts(defaults, tc.pref).withChannel(tc.advanced)
			if got != tc.want {
				t.Fatalf("timeouts = %+v, want %+v", got, tc.want)
			}
		})
	}
}
