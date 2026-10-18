package groupchannel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// ResolveDefaults resolves only channel/group values. A nil timeout still
// resolves against the caller's model-route settings before platform defaults.
// Header rules replace the entire block, including an explicitly empty block.
func ResolveDefaults(override, group market.Defaults) (market.Defaults, map[string]string) {
	sources := map[string]string{}
	text := func(name string, a, b *string) *string {
		if a != nil {
			sources[name] = "channel"
			v := *a
			return &v
		}
		if b != nil {
			sources[name] = "group"
			v := *b
			return &v
		}
		sources[name] = "platform"
		return nil
	}
	number := func(name string, a, b *int32, timeout bool) *int32 {
		if a != nil {
			sources[name] = "channel"
			v := *a
			return &v
		}
		if b != nil {
			sources[name] = "group"
			v := *b
			return &v
		}
		sources[name] = "platform"
		if timeout {
			sources[name] = "model_route_or_platform"
		}
		return nil
	}
	d := market.Defaults{UserAgent: text("user_agent", override.UserAgent, group.UserAgent), ConcurrencyLimit: number("concurrency_limit", override.ConcurrencyLimit, group.ConcurrencyLimit, false), RPMLimit: number("rpm_limit", override.RPMLimit, group.RPMLimit, false), TTFTTimeoutMS: number("ttft_timeout_ms", override.TTFTTimeoutMS, group.TTFTTimeoutMS, true), RequestTimeoutMS: number("request_timeout_ms", override.RequestTimeoutMS, group.RequestTimeoutMS, true), IdleTimeoutMS: number("idle_timeout_ms", override.IdleTimeoutMS, group.IdleTimeoutMS, true), CooldownFailures: number("cooldown_failures", override.CooldownFailures, group.CooldownFailures, false), CooldownSeconds: number("cooldown_seconds", override.CooldownSeconds, group.CooldownSeconds, false)}
	headers := override.HeaderRules
	sources["header_rules"] = "channel"
	if headers == nil {
		headers = group.HeaderRules
		sources["header_rules"] = "group"
	}
	if headers == nil {
		sources["header_rules"] = "platform"
	} else {
		d.HeaderRules = &market.HeaderRules{Set: slices.Clone(headers.Set), Remove: slices.Clone(headers.Remove)}
	}
	return d, sources
}
func Advanced(d market.Defaults) channel.Advanced {
	a := channel.Advanced{UserAgent: d.UserAgent, ConcurrencyLimit: d.ConcurrencyLimit, RPMLimit: d.RPMLimit, TTFTTimeoutMS: d.TTFTTimeoutMS, RequestTimeoutMS: d.RequestTimeoutMS, IdleTimeoutMS: d.IdleTimeoutMS, CooldownFailures: d.CooldownFailures, CooldownSeconds: d.CooldownSeconds}
	if d.HeaderRules != nil {
		for _, s := range d.HeaderRules.Set {
			a.HeaderRules.Set = append(a.HeaderRules.Set, channel.HeaderSet{Name: s.Name, Value: s.Value})
		}
		a.HeaderRules.Remove = slices.Clone(d.HeaderRules.Remove)
	}
	return a
}
func ProbeBinding(r Record, g market.Group, m market.GroupModel) string {
	effective, _ := ResolveDefaults(r.Overrides, g.Defaults)
	// Prices, discounts, exclusions, address ordering and unrelated timeout/
	// concurrency edits do not change a Key's basic protocol evidence.
	payload := struct {
		Channel, Group, Model     string
		CredentialRevision, Epoch int64
		GroupDigest, ModelDigest  string
		UA                        *string
		Headers                   *market.HeaderRules
	}{r.ID, g.ID, m.ID, r.CredentialRevision, r.ProbeEpoch, g.CapabilityDigest, m.CapabilityDigest, effective.UserAgent, effective.HeaderRules}
	b, _ := json.Marshal(payload)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}
func CompatibleHeaders(override, group market.Defaults) bool {
	effective, _ := ResolveDefaults(override, group)
	a, _ := json.Marshal(struct {
		UA      *string
		Headers *market.HeaderRules
	}{effective.UserAgent, effective.HeaderRules})
	b, _ := json.Marshal(struct {
		UA      *string
		Headers *market.HeaderRules
	}{group.UserAgent, group.HeaderRules})
	return string(a) == string(b)
}
