package detection

import (
	"context"
	"math"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// CapacityProbe carries facts, never an upstream body/error message. Accepted
// is independent of Semantic/Actual: accepting max_output_tokens does not prove
// that many tokens were generated. FailureUpper is present only when the native
// error and a reliable count establish an upper bound in the report's unit.
type CapacityProbe struct {
	Accepted             bool     `json:"accepted"`
	Semantic             bool     `json:"semantic"`
	Actual               *int64   `json:"actual"`
	InputOverhead        *int64   `json:"input_overhead"`
	LengthError          bool     `json:"length_error"`
	FailureUpper         *int64   `json:"failure_upper"`
	Approximate          bool     `json:"approximate"`
	RequestedApproximate bool     `json:"requested_approximate"`
	Reason               string   `json:"reason"`
	AttemptIDs           []string `json:"attempt_ids"`
}
type CapacityStep struct {
	Requested int64 `json:"requested"`
	Round     int   `json:"round"`
	Recheck   int   `json:"recheck"`
	CapacityProbe
}
type CapacityReport struct {
	Kind                   string                         `json:"kind"`
	Unit                   string                         `json:"unit"`
	Declared               *int64                         `json:"declared"`
	Settings               gatewaycontract.SearchSettings `json:"settings"`
	ParameterAcceptedLower *int64                         `json:"parameter_accepted_lower"`
	ParameterRejectedUpper *int64                         `json:"parameter_rejected_upper"`
	VerifiedLower          *int64                         `json:"verified_lower"`
	ProvenFailureUpper     *int64                         `json:"proven_failure_upper"`
	Completed              bool                           `json:"completed"`
	Stop                   string                         `json:"stop"`
	Steps                  []CapacityStep                 `json:"steps"`
}

// SearchCapacity searches a finite configured integer domain. Only explicit
// native length errors contract the requested search interval. Short answers,
// refusals, unknown counts and external errors preserve the unknown upper bound.
// No wall-clock waiting or cumulative full-job budget is introduced here.
func SearchCapacity(ctx context.Context, settings gatewaycontract.SearchSettings, kind, unit string, declared *int64, probe func(context.Context, int64) CapacityProbe) (CapacityReport, error) {
	r := CapacityReport{Kind: kind, Unit: unit, Declared: declared, Settings: settings, Stop: "rounds", Steps: []CapacityStep{}}
	if settings.Validate() != nil || settings.Rounds > 128 || settings.Rechecks > 8 || (kind != "context" && kind != "max_output") || unit != "tokens" || probe == nil {
		return r, ErrInvalid
	}
	lo, hi := settings.Low, settings.High
	// No initial endpoint is a measured success/failure. The configured interval
	// bounds work, and cannot by itself become a reported capacity bound.
	tried := map[int64]bool{}
	for round := 1; round <= settings.Rounds; round++ {
		if ctx.Err() != nil {
			r.Stop = "cancelled"
			return r, ctx.Err()
		}
		gap := hi - lo
		precision := settings.Precision
		if settings.PrecisionMode == "relative" {
			precision *= math.Max(1, float64(hi))
		}
		var points []int64
		if float64(gap) <= precision || gap <= 1 {
			// Test the configured endpoint when no failure has established a real
			// upper bound; do not round a successful near-high sample up to high.
			if r.ParameterRejectedUpper == nil && !tried[hi] {
				points = []int64{hi}
			} else if lo > 0 && !tried[lo] {
				points = []int64{lo}
			} else {
				r.Stop = "precision"
				r.Completed = true
				return r, nil
			}
		} else if settings.Method == "ternary" && gap >= 3 {
			points = []int64{lo + gap/3, hi - gap/3}
		} else {
			points = []int64{lo + gap/2}
		}
		for _, point := range points {
			if tried[point] {
				continue
			}
			tried[point] = true
			var p CapacityProbe
			for retry := 0; retry <= settings.Rechecks; retry++ {
				if ctx.Err() != nil {
					r.Stop = "cancelled"
					return r, ctx.Err()
				}
				p = probe(ctx, point)
				if !reasonValid(p.Reason) || len(p.AttemptIDs) == 0 || len(p.AttemptIDs) > 16 || (p.Actual != nil && *p.Actual < 0) || (p.FailureUpper != nil && (*p.FailureUpper < 0 || !p.LengthError)) {
					return r, ErrInvalid
				}
				r.Steps = append(r.Steps, CapacityStep{Requested: point, Round: round, Recheck: retry, CapacityProbe: p})
				if p.Accepted && (r.ParameterAcceptedLower == nil || point > *r.ParameterAcceptedLower) {
					v := point
					r.ParameterAcceptedLower = &v
				}
				if p.Semantic && p.Actual != nil && !p.Approximate && (r.VerifiedLower == nil || *p.Actual > *r.VerifiedLower) {
					v := *p.Actual
					r.VerifiedLower = &v
				}
				if p.LengthError || (p.Accepted && p.Semantic && p.Actual != nil && !p.Approximate && *p.Actual >= point) {
					break
				}
			}
			if p.LengthError {
				if r.ParameterAcceptedLower != nil && point <= *r.ParameterAcceptedLower {
					r.Completed = true
					r.Stop = "non_monotonic"
					return r, nil
				}
				if r.ParameterRejectedUpper == nil || point < *r.ParameterRejectedUpper {
					v := point
					r.ParameterRejectedUpper = &v
				}
				if p.FailureUpper != nil && !p.Approximate {
					if r.VerifiedLower != nil && *p.FailureUpper <= *r.VerifiedLower {
						r.Completed = true
						r.Stop = "non_monotonic"
						return r, nil
					}
					if r.ProvenFailureUpper == nil || *p.FailureUpper < *r.ProvenFailureUpper {
						v := *p.FailureUpper
						r.ProvenFailureUpper = &v
					}
				}
				hi = min(hi, point)
			} else if p.Accepted && p.Semantic && p.Actual != nil && !p.Approximate && *p.Actual >= point {
				if r.ParameterRejectedUpper != nil && point >= *r.ParameterRejectedUpper {
					r.Completed = true
					r.Stop = "non_monotonic"
					return r, nil
				}
				lo = max(lo, point)
				if point == settings.High {
					r.Completed = true
					r.Stop = "configured_bound"
					return r, nil
				}
			} else {
				r.Completed = true
				r.Stop = "unconfirmed"
				return r, nil
			}
		}
	}
	r.Completed = true
	return r, nil
}
