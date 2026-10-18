package detection

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

// Configured and Wire are fixed test options or validated quote selectors.
// Actual is a whitelisted canonical tier, never an arbitrary response string.
type SettingEvidence struct {
	Kind             string   `json:"kind"`
	Configured       string   `json:"configured"`
	Wire             string   `json:"wire"`
	Actual           string   `json:"actual"`
	State            string   `json:"state"`
	Reason           string   `json:"reason"`
	Accepted         bool     `json:"accepted"`
	UsageReliable    bool     `json:"usage_reliable"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	CacheReadTokens  int64    `json:"cache_read_tokens"`
	AttemptIDs       []string `json:"attempt_ids"`
}

func tierNamespace(protocol string) string {
	switch protocol {
	case "native-messages":
		return "anthropic"
	case "native-generate-content":
		return "gemini"
	default:
		return "openai"
	}
}
func canonicalNativeTier(protocol, raw string) string {
	ns := tierNamespace(protocol)
	if ns == "openai" && raw == "priority" {
		raw = "fast"
	}
	values := map[string][]string{"openai": {"default", "fast", "flex", "ultrafast", "scale"}, "anthropic": {"standard", "priority", "batch"}, "gemini": {"standard", "priority", "flex"}}
	if slices.Contains(values[ns], raw) {
		return ns + ":" + raw
	}
	return ""
}
func observedTier(protocol string, out nativeOutcome) string {
	raw := out.Reply.Usage.Tier
	if protocol == "native-generate-content" {
		header := out.Response.Headers.Get("x-gemini-service-tier")
		if header != "" && raw != "" && header != raw {
			return ""
		}
		if raw == "" {
			raw = header
		}
	}
	return canonicalNativeTier(protocol, raw)
}
func (n *nativeScene) tiers(ctx context.Context) {
	ns := tierNamespace(n.protocol)
	baseline := ns + ":default"
	if ns != "openai" {
		baseline = ns + ":standard"
	}
	tiers := []string{baseline}
	for _, tier := range n.s.Target.Profile.ServiceTiers {
		if strings.HasPrefix(tier, ns+":") && !slices.Contains(tiers, tier) {
			tiers = append(tiers, tier)
		}
	}
	for _, configured := range tiers {
		if ctx.Err() != nil {
			return
		}
		wire := nativeRequestedTier(configured)
		body := baseNative(n.protocol, n.s.Target.Model, nativeBasicPrompt)
		if ns == "anthropic" {
			if wire == "standard" {
				wire = "standard_only"
			} else if wire == "priority" {
				wire = "auto"
			}
		}
		if ns == "gemini" {
			body["serviceTier"] = wire
		} else {
			body["service_tier"] = wire
		}
		out := n.infer(ctx, body, false, true)
		actual := observedTier(n.protocol, out)
		accepted := out.State == "pass" && out.Reply.Structure && contentMatches(out, nativeMarker) && nativeEndedNormally(out.Reply)
		valid := accepted && out.Reply.Usage.Reliable && actual == configured
		if ns == "openai" && configured == "openai:priority" {
			valid = accepted && out.Reply.Usage.Reliable && actual == "openai:fast"
		}
		if accepted && !valid {
			out.State, out.Reason = "inconclusive", "not_observed"
		}
		if out.State == "pass" && !accepted {
			out.State, out.Reason = "fail", "assertion_failed"
		}
		n.check("each_enabled_service_tier_actual_return", out, valid)
		n.result.Settings = append(n.result.Settings, SettingEvidence{Kind: "service_tier", Configured: configured, Wire: wire, Actual: actual, State: out.State, Reason: out.Reason, Accepted: accepted, UsageReliable: out.Reply.Usage.Reliable, AttemptIDs: []string{out.Response.AttemptID}})
	}
}

func (n *nativeScene) cachePrefix(option string) string {
	count := 8192
	if n.s.Target.ContextWindow != nil && *n.s.Target.ContextWindow/2 < int64(count) {
		count = max(128, int(*n.s.Target.ContextWindow/2))
	}
	nonce, _ := ids.NewUUID()
	return "Synthetic cache probe " + nonce + " " + option + ". The synthetic code is " + replayCode + ". Ignore this padding:\n" + strings.Repeat(" pad", count)
}
func (n *nativeScene) cacheTier(ctx context.Context) {
	if n.protocol == "native-generate-content" {
		n.explicitCache(ctx)
	} else {
		n.implicitCache(ctx)
	}
	n.tiers(ctx)
}
func (n *nativeScene) implicitCache(ctx context.Context) {
	options := []string{"in_memory", "24h"}
	if n.protocol == "native-messages" {
		options = []string{"5m", "1h"}
	}
	for _, option := range options {
		if ctx.Err() != nil {
			return
		}
		prefix := n.cachePrefix(option)
		body := baseNative(n.protocol, n.s.Target.Model, "Reply only with the synthetic code in the context.")
		switch n.protocol {
		case "openai-chat-completions":
			body["messages"] = append([]any{nativeObject{"role": "system", "content": prefix}}, list(body["messages"])...)
			body["prompt_cache_key"], body["prompt_cache_retention"] = n.s.jobID+"-"+option, option
		case "openai-responses":
			body["instructions"] = prefix
			body["prompt_cache_key"], body["prompt_cache_retention"] = n.s.jobID+"-"+option, option
		case "native-messages":
			body["system"] = []any{nativeObject{"type": "text", "text": prefix, "cache_control": nativeObject{"type": "ephemeral", "ttl": option}}}
		}
		first := n.infer(ctx, body, false, false)
		read := n.infer(ctx, body, false, false)
		accepted := first.State == "pass" && first.Reply.Structure && contentMatches(first, replayCode) && nativeEndedNormally(first.Reply)
		n.check("cache_options_acceptance", first, accepted)
		reliable := first.Reply.Usage.Reliable && read.Reply.Usage.Reliable
		valid := accepted && read.Reply.Structure && contentMatches(read, replayCode) && reliable && read.Reply.Usage.CacheRead > 0
		if n.protocol == "native-messages" {
			valid = valid && first.Reply.Usage.CacheWrite > 0
		}
		out := read
		if out.State == "pass" && !valid {
			out.State, out.Reason = "inconclusive", "not_observed"
		}
		n.linkedCheck("immediate_real_write_read_usage", first, out, valid)
		n.check("no_wait_for_expiration", first, true)
		n.result.Settings = append(n.result.Settings, SettingEvidence{Kind: "cache", Configured: option, Wire: option, State: out.State, Reason: out.Reason, Accepted: accepted, UsageReliable: reliable, CacheWriteTokens: first.Reply.Usage.CacheWrite, CacheReadTokens: read.Reply.Usage.CacheRead, AttemptIDs: []string{first.Response.AttemptID, read.Response.AttemptID}})
	}
}

func (n *nativeScene) explicitCache(ctx context.Context) {
	prefix := n.cachePrefix("explicit")
	body := nativeObject{"model": "models/" + strings.TrimPrefix(n.s.Target.Model, "models/"), "contents": []any{nativeObject{"role": "user", "parts": []any{nativeObject{"text": prefix}}}}, "ttl": "300s"}
	created := n.rest(ctx, "cache.create", "/v1beta/cachedContents", body, nil, false)
	name := str(created.Reply.Object["name"])
	id := strings.TrimPrefix(name, "cachedContents/")
	_, expiryErr := time.Parse(time.RFC3339Nano, str(created.Reply.Object["expireTime"]))
	owned := created.State == "pass" && strings.HasPrefix(name, "cachedContents/") && resourceToken(id)
	if owned {
		owned = n.s.TrackResource(ctx, created.Response.AttemptID, "cache", id, "cache.delete", "/v1beta/cachedContents/"+url.PathEscape(id)) == nil
	}
	n.check("cache_options_acceptance", created, owned && expiryErr == nil)
	if !owned {
		return
	}
	path := "/v1beta/cachedContents/" + url.PathEscape(id)
	get := n.rest(ctx, "cache.get", path, nil, nil, false)
	n.check("owned_explicit_cache_lifecycle", get, str(get.Reply.Object["name"]) == name)
	update := n.rest(ctx, "cache.update", path, nativeObject{"ttl": "3600s"}, url.Values{"updateMask": {"ttl"}}, false)
	_, expiryErr = time.Parse(time.RFC3339Nano, str(update.Reply.Object["expireTime"]))
	n.check("cache_options_acceptance", update, str(update.Reply.Object["name"]) == name && expiryErr == nil)
	n.check("owned_explicit_cache_lifecycle", update, str(update.Reply.Object["name"]) == name)
	cursor := ""
	seen := map[string]bool{}
	foundOwn := false
	var lastPage nativeOutcome
	for range 3 {
		query := url.Values{"pageSize": {"2"}}
		if cursor != "" {
			query.Set("pageToken", cursor)
		}
		page := n.rest(ctx, "cache.list", "/v1beta/cachedContents", nil, query, false)
		lastPage = page
		entries, present := page.Reply.Object["cachedContents"].([]any)
		found := false
		for _, entry := range entries {
			found = found || str(object(entry)["name"]) == name
		}
		foundOwn = foundOwn || found
		cursor = str(page.Reply.Object["nextPageToken"])
		valid := present && len(cursor) <= 4096 && (cursor == "" || !seen[cursor])
		n.check("owned_explicit_cache_lifecycle", page, valid)
		if !valid || found || cursor == "" || ctx.Err() != nil {
			break
		}
		seen[cursor] = true
	}
	if !foundOwn {
		n.uncertain("owned_explicit_cache_lifecycle", lastPage)
	}
	request := baseNative(n.protocol, n.s.Target.Model, "Reply only with the synthetic code in the cached context.")
	request["cachedContent"] = name
	for _, stream := range []bool{false, true} {
		out := n.infer(ctx, request, stream, false)
		valid := out.Reply.Structure && contentMatches(out, replayCode) && nativeEndedNormally(out.Reply) && out.Reply.Usage.Reliable && out.Reply.Usage.CacheRead > 0
		if stream {
			valid = valid && out.Stream.Valid
		}
		if out.State == "pass" && !valid {
			out.State, out.Reason = "inconclusive", "not_observed"
		}
		n.linkedCheck("immediate_real_write_read_usage", created, out, valid)
		n.check("cached_content_generate_and_stream_usage", out, valid)
		n.result.Settings = append(n.result.Settings, SettingEvidence{Kind: "cache", Configured: "explicit", Wire: "300s/3600s", State: out.State, Reason: out.Reason, Accepted: out.Reply.Structure, UsageReliable: out.Reply.Usage.Reliable, CacheReadTokens: out.Reply.Usage.CacheRead, AttemptIDs: []string{created.Response.AttemptID, out.Response.AttemptID}})
		// infer mutates stream flags, so each operation uses a fresh body.
		request = baseNative(n.protocol, n.s.Target.Model, "Reply only with the synthetic code in the cached context.")
		request["cachedContent"] = name
	}
	count, out := n.tokenCount(ctx, request)
	n.check("native_cached_token_count", out, count != nil && *count > 0)
	deleted := n.deleteOwned(ctx, "cache", id)
	n.check("owned_explicit_cache_lifecycle", deleted, deleted.State == "pass")
	n.check("no_wait_for_expiration", deleted, true)
}

func validSettingEvidence(settings []SettingEvidence, witnesses map[string]Attempt) bool {
	if len(settings) > 152 {
		return false
	}
	for _, e := range settings {
		if !slices.Contains([]string{"service_tier", "cache"}, e.Kind) || !terminal(e.State) || !reasonValid(e.Reason) || len(e.Configured) > 64 || len(e.Wire) > 64 || len(e.AttemptIDs) < 1 || len(e.AttemptIDs) > 16 || e.CacheReadTokens < 0 || e.CacheWriteTokens < 0 {
			return false
		}
		if e.Kind == "cache" && (!slices.Contains([]string{"5m", "1h", "in_memory", "24h", "explicit"}, e.Configured) || e.Actual != "") {
			return false
		}
		if e.Kind == "service_tier" {
			ns, _, ok := strings.Cut(e.Configured, ":")
			protocol := map[string]string{"openai": "openai-responses", "anthropic": "native-messages", "gemini": "native-generate-content"}[ns]
			if !ok || protocol == "" || !safeCode(e.Configured) || !safeCode(e.Wire) || (e.Actual != "" && canonicalNativeTier(protocol, strings.TrimPrefix(e.Actual, ns+":")) != e.Actual) {
				return false
			}
		}
		positive := false
		for _, id := range e.AttemptIDs {
			a, ok := witnesses[id]
			if !ok {
				return false
			}
			positive = positive || a.ResponseReceived && a.Status >= 200 && a.Status < 300
		}
		if e.State == "pass" && (!positive || !e.Accepted || !e.UsageReliable) {
			return false
		}
		if e.State == "pass" && e.Kind == "cache" && e.CacheReadTokens == 0 {
			return false
		}
		if e.State == "pass" && e.Kind == "service_tier" {
			expected := e.Configured
			if expected == "openai:priority" {
				expected = "openai:fast"
			}
			if e.Actual != expected {
				return false
			}
		}
	}
	return true
}
