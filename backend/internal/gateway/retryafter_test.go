package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestRateLimitCooldownReadsAndBoundsTheUpstreamHint(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	limits := Limits{CooldownAfter: 3, CooldownFor: 5 * time.Minute}
	cases := []struct {
		name   string
		status int
		header map[string]string
		limits *Limits
		want   time.Duration // zero means no hint
	}{
		{name: "seconds", header: map[string]string{"Retry-After": "2"}, want: 2 * time.Second},
		{name: "seconds with spaces", header: map[string]string{"Retry-After": " 30 "}, want: 30 * time.Second},
		{name: "fractional seconds", header: map[string]string{"Retry-After": "2.5"}, want: 2500 * time.Millisecond},
		{name: "http date", header: map[string]string{"Retry-After": now.Add(90 * time.Second).Format(http.TimeFormat)}, want: 90 * time.Second},
		{name: "http date in the past rests the minimum", header: map[string]string{"Retry-After": now.Add(-time.Hour).Format(http.TimeFormat)}, want: time.Second},
		{name: "zero rests the minimum", header: map[string]string{"Retry-After": "0"}, want: time.Second},
		{name: "below the minimum rests the minimum", header: map[string]string{"Retry-After": "0.2"}, want: time.Second},
		{name: "limited to the channel cooldown", header: map[string]string{"Retry-After": "3600"}, want: 5 * time.Minute},
		{name: "date beyond the channel cooldown", header: map[string]string{"Retry-After": now.Add(24 * time.Hour).Format(http.TimeFormat)}, want: 5 * time.Minute},
		{name: "absurd value does not overflow", header: map[string]string{"Retry-After": "99999999999999999999999"}, want: 5 * time.Minute},
		{name: "milliseconds", header: map[string]string{"retry-after-ms": "1500"}, want: 1500 * time.Millisecond},
		{name: "milliseconds below the minimum", header: map[string]string{"retry-after-ms": "200"}, want: time.Second},
		{name: "milliseconds win over seconds", header: map[string]string{"retry-after-ms": "2500", "Retry-After": "10"}, want: 2500 * time.Millisecond},
		{name: "bad milliseconds fall back to seconds", header: map[string]string{"retry-after-ms": "soon", "Retry-After": "7"}, want: 7 * time.Second},
		{name: "the effective cooldown is the ceiling", header: map[string]string{"Retry-After": "120"}, limits: &Limits{CooldownFor: 10 * time.Second}, want: 10 * time.Second},

		{name: "no header", header: nil},
		{name: "empty", header: map[string]string{"Retry-After": ""}},
		{name: "negative", header: map[string]string{"Retry-After": "-5"}},
		{name: "exponent", header: map[string]string{"Retry-After": "1e3"}},
		{name: "infinity", header: map[string]string{"Retry-After": "Inf"}},
		{name: "two dots", header: map[string]string{"Retry-After": "1.2.3"}},
		{name: "only a dot", header: map[string]string{"Retry-After": "."}},
		{name: "text", header: map[string]string{"Retry-After": "later"}},
		{name: "negative milliseconds", header: map[string]string{"retry-after-ms": "-1"}},
		{name: "other status is only counted", status: http.StatusServiceUnavailable, header: map[string]string{"Retry-After": "2"}},
		{name: "no cooldown configured", header: map[string]string{"Retry-After": "2"}, limits: &Limits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			for name, value := range tc.header {
				header.Set(name, value)
			}
			status, effective := tc.status, limits
			if status == 0 {
				status = http.StatusTooManyRequests
			}
			if tc.limits != nil {
				effective = *tc.limits
			}
			got, ok := RateLimitCooldown(status, header, now, effective)
			if ok != (tc.want > 0) || got != tc.want {
				t.Fatalf("RateLimitCooldown = %v, %v; want %v", got, ok, tc.want)
			}
		})
	}
}

func TestRuntimeCooldownForStartsExtendsAndNeverShortens(t *testing.T) {
	type event struct{ id, kind, reason string }
	var events []event
	rt := NewRuntime(func(id, kind, reason string) { events = append(events, event{id, kind, reason}) })
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	limits := Limits{CooldownAfter: 2, CooldownFor: time.Minute}

	if rt.CooldownFor("c", 0, now, CooldownRateLimited) {
		t.Fatal("a zero cooldown started")
	}
	if state, _ := rt.Check("c", limits, now); state != StateAvailable || len(events) != 0 {
		t.Fatalf("zero cooldown changed the channel: %s %v", state, events)
	}

	rt.Failure("c", limits, now, "boom") // one failure short of the threshold
	if !rt.CooldownFor("c", 2*time.Second, now, CooldownRateLimited) {
		t.Fatal("cooldown did not start")
	}
	if state, _ := rt.Check("c", limits, now.Add(time.Second)); state != StateCooldown {
		t.Fatalf("state during the cooldown = %s", state)
	}
	if remaining := rt.CooldownRemaining("c", now.Add(time.Second)); remaining != time.Second {
		t.Fatalf("remaining = %s", remaining)
	}
	if state, _ := rt.Check("c", limits, now.Add(2*time.Second)); state != StateAvailable {
		t.Fatalf("state after the cooldown = %s", state)
	}
	// The cooldown restarted the count: one more failure is below the threshold.
	if rt.Failure("c", limits, now.Add(3*time.Second), "boom") {
		t.Fatal("the failure count survived the cooldown")
	}
	rt.Success("c")

	// A longer cooldown is extended, a shorter one is ignored; neither is a second start.
	later := now.Add(10 * time.Second)
	if !rt.CooldownFor("c", 20*time.Second, later, CooldownRateLimited) {
		t.Fatal("second cooldown did not start")
	}
	if rt.CooldownFor("c", 5*time.Second, later, CooldownRateLimited) {
		t.Fatal("a shorter hint reported a new cooldown")
	}
	if until := rt.CooldownUntil("c", later); !until.Equal(later.Add(20 * time.Second)) {
		t.Fatalf("a shorter hint changed the cooldown to %s", until)
	}
	if rt.CooldownFor("c", 40*time.Second, later, CooldownRateLimited) {
		t.Fatal("an extension reported a new cooldown")
	}
	if until := rt.CooldownUntil("c", later); !until.Equal(later.Add(40 * time.Second)) {
		t.Fatalf("the cooldown was not extended: %s", until)
	}

	// A cooldown that began by reaching the failure threshold is not shortened either.
	rt.Failure("d", limits, now, "boom")
	if !rt.Failure("d", limits, now, "boom") {
		t.Fatal("failure cooldown did not start")
	}
	rt.CooldownFor("d", time.Second, now, CooldownRateLimited)
	if until := rt.CooldownUntil("d", now); !until.Equal(now.Add(time.Minute)) {
		t.Fatalf("hint shortened the failure cooldown to %s", until)
	}

	want := []event{
		{"c", channel.EventCooldownStarted, CooldownRateLimited},
		{"c", channel.EventCooldownEnded, ""},
		{"c", channel.EventCooldownStarted, CooldownRateLimited},
		{"d", channel.EventCooldownStarted, "boom"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("event %d = %v, want %v", index, events[index], want[index])
		}
	}
}
