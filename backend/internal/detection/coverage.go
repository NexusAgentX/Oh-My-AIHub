package detection

import (
	"slices"
	"strings"
)

var reasons = []string{"ok", "unsupported", "assertion_failed", "prerequisite_failed", "external_error", "detector_error", "not_observed", "client_disabled", "platform_unavailable", "cancelled", "interrupted", "invalid_response", "body_limit", "timeout", "network_error", "invalid_request", "key_unavailable", "inputs_changed", "suite_pending"}

func reasonValid(reason string) bool { return slices.Contains(reasons, reason) }
func terminal(state string) bool {
	return slices.Contains([]string{"pass", "partial", "fail", "unsupported", "inconclusive"}, state)
}

// ValidateResult checks independent assertions and real request witnesses. It
// never accepts skipped/untested/cancelled as a terminal capability conclusion.
// Dependency failures may be inconclusive only with an actual prerequisite
// witness supplied by the executor, and independent scenarios still run.
func ValidateResult(item Planned, result Result, witnesses map[string]Attempt) error {
	if !terminal(result.State) || !reasonValid(result.Reason) || result.ScenarioID != item.ID || result.Layer != item.Layer || !slices.Contains([]string{"real_upstream", "synthetic"}, result.Source) || result.FinishedAt.Before(result.StartedAt) || len(result.Assertions) != len(item.Assertions) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, a := range result.Assertions {
		if seen[a.ID] || !slices.Contains(item.Assertions, a.ID) || !reasonValid(a.Reason) || len(a.AttemptIDs) == 0 || len(a.AttemptIDs) > 16 {
			return ErrInvalid
		}
		seen[a.ID] = true
		if !terminal(a.State) && !(a.State == "unobserved" && slices.Contains(item.Conditional, a.ID)) {
			return ErrInvalid
		}
		if a.State == "unobserved" && a.Reason != "not_observed" {
			return ErrInvalid
		}
		for _, id := range a.AttemptIDs {
			attempt, ok := witnesses[id]
			if !ok || (!attempt.Sent && a.Reason != "external_error" && a.Reason != "network_error" && a.Reason != "timeout" && a.Reason != "prerequisite_failed") {
				return ErrInvalid
			}
		}
		// A connection failure cannot prove unsupported or supported. HTTP error
		// responses and protocol validators provide their own specific assertions.
		if a.State == "pass" || a.State == "unsupported" {
			valid := false
			for _, id := range a.AttemptIDs {
				attempt := witnesses[id]
				valid = valid || (attempt.ResponseReceived && (a.State != "unsupported" || (attempt.Status != 401 && attempt.Status != 403 && attempt.Status != 429 && attempt.Status < 500)))
			}
			if !valid {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Complete is intentionally stricter than job terminality or a green summary.
// Negative but fully executed reports qualify; absent suites, fixture results,
// mismatched versions or unfinished items never qualify for object creation.
func Complete(job Job) bool {
	if job.State != "finished" || len(job.Plan.Items) == 0 || job.Plan.ManifestVersion != job.Target.Binding.ManifestVersion || job.Plan.RegistryVersion != job.Target.Binding.CodeVersion || job.Target.Binding.ParameterVersion != parametersVersion(job.Parameters) || job.Target.Binding.CredentialScope == "" {
		return false
	}
	results := map[string]Result{}
	for _, result := range job.Results {
		if _, ok := results[result.ScenarioID]; ok {
			return false
		}
		results[result.ScenarioID] = result
	}
	for _, item := range job.Plan.Items {
		result, ok := results[item.ID]
		if !ok || !item.Implemented || !terminal(result.State) || result.Source != "real_upstream" || result.Layer != item.Layer || len(result.Assertions) != len(item.Assertions) {
			return false
		}
		seen := map[string]bool{}
		for _, a := range result.Assertions {
			if seen[a.ID] || !slices.Contains(item.Assertions, a.ID) || len(a.AttemptIDs) == 0 || (!terminal(a.State) && !(a.State == "unobserved" && slices.Contains(item.Conditional, a.ID))) {
				return false
			}
			seen[a.ID] = true
		}
	}
	return len(results) == len(job.Plan.Items)
}
func HasPending(plan Plan) bool {
	for _, item := range plan.Items {
		if !item.Implemented {
			return true
		}
	}
	return false
}
func safeCode(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\x00")
}
