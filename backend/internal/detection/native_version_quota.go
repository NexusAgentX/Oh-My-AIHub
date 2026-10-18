package detection

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// QuotaEvidence is credential-scoped diagnostic metadata. Publication must not
// expose it to another channel owner. Reset timestamps are never waited on.
type QuotaEvidence struct {
	Header    string `json:"header"`
	Remaining int64  `json:"remaining"`
	AttemptID string `json:"attempt_id"`
}

var quotaHeaders = []string{"x-ratelimit-remaining-requests", "x-ratelimit-remaining-tokens", "anthropic-ratelimit-requests-remaining", "anthropic-ratelimit-input-tokens-remaining", "anthropic-ratelimit-output-tokens-remaining", "anthropic-ratelimit-tokens-remaining"}

func (n *nativeScene) quota(ctx context.Context) {
	o := n.readCatalog(ctx).Last
	observed, valid := false, true
	for _, header := range quotaHeaders {
		values := o.Response.Headers.Values(header)
		if len(values) == 0 {
			continue
		}
		observed = true
		remaining, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 64)
		if len(values) != 1 || err != nil || remaining < 0 {
			valid = false
			continue
		}
		n.result.Quotas = append(n.result.Quotas, QuotaEvidence{Header: header, Remaining: remaining, AttemptID: o.Response.AttemptID})
	}
	if observed {
		n.check("remaining_quota_headers_when_observed", o, valid)
	} else {
		n.unobserved("remaining_quota_headers_when_observed", o)
	}
	// The pinned native inference APIs expose no common account-balance API
	// for an inference Key. Do not borrow an organization/admin credential or
	// reinterpret token counts as a monetary balance.
	n.unobserved("account_balance_not_exposed_by_native_profile", o)
}
func validQuotaEvidence(rows []QuotaEvidence, witnesses map[string]Attempt) bool {
	if len(rows) > len(quotaHeaders) {
		return false
	}
	seen := map[string]bool{}
	for _, q := range rows {
		a, ok := witnesses[q.AttemptID]
		if seen[q.Header] || !slices.Contains(quotaHeaders, q.Header) || q.Remaining < 0 || !ok || !a.Sent || !a.ResponseReceived {
			return false
		}
		seen[q.Header] = true
	}
	return true
}
func (n *nativeScene) versions(ctx context.Context) {
	for _, version := range []string{"v1beta", "v1"} {
		prefix := "/" + version + "/models"
		id := "models.gemini.list"
		if version == "v1" {
			id = "models.gemini.v1.list"
		}
		o := n.call(ctx, Request{EndpointID: id, Path: prefix, Category: "query"}, true)
		models, ok := o.Reply.Object["models"].([]any)
		valid := ok && len(models) <= 10000
		for _, raw := range models {
			model := str(object(raw)["name"])
			valid = valid && strings.HasPrefix(model, "models/") && len(model) > len("models/")
		}
		n.check(version+"_native_directory", o, valid)
	}
}
