package gateway

import (
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type attemptTimeouts struct{ ttft, idle, request time.Duration }

// Resolve each stage independently; the selected route is already either the
// key's complete model preference or the account's, never a field-wise merge.
func resolveTimeouts(defaults settings.Settings, pref routing.Pref) attemptTimeouts {
	return attemptTimeouts{
		ttft:    timeoutOverride(time.Duration(defaults.DefaultTTFTTimeoutMS)*time.Millisecond, pref.TTFTTimeoutMS),
		idle:    timeoutOverride(time.Duration(defaults.DefaultIdleTimeoutMS)*time.Millisecond, pref.IdleTimeoutMS),
		request: timeoutOverride(time.Duration(defaults.DefaultRequestTimeoutMS)*time.Millisecond, pref.RequestTimeoutMS),
	}
}

func (t attemptTimeouts) withChannel(advanced channel.Advanced) attemptTimeouts {
	return attemptTimeouts{
		ttft:    timeoutOverride(t.ttft, advanced.TTFTTimeoutMS),
		idle:    timeoutOverride(t.idle, advanced.IdleTimeoutMS),
		request: timeoutOverride(t.request, advanced.RequestTimeoutMS),
	}
}

func timeoutOverride(fallback time.Duration, value *int32) time.Duration {
	if value != nil {
		return time.Duration(*value) * time.Millisecond
	}
	return fallback
}
