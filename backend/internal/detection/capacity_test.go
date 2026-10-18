package detection

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func searchSettings() gatewaycontract.SearchSettings {
	return gatewaycontract.SearchSettings{Version: "test.v1", Method: "binary", Low: 0, High: 1000, HardMaximum: 1000, Precision: 1, PrecisionMode: "absolute", Rounds: 20, Rechecks: 1}
}
func TestCapacityRealThresholdAndConfiguredEndpoint(t *testing.T) {
	for _, method := range []string{"binary", "ternary"} {
		t.Run(method, func(t *testing.T) {
			s := searchSettings()
			s.Method = method
			for _, limit := range []int64{1, 273, 999, 1000, 2000} {
				r, err := SearchCapacity(context.Background(), s, "context", "tokens", nil, func(_ context.Context, n int64) CapacityProbe {
					v := n
					if n > limit {
						return CapacityProbe{LengthError: true, FailureUpper: &v, Reason: "unsupported", AttemptIDs: []string{"length-error"}}
					}
					return CapacityProbe{Accepted: true, Semantic: true, Actual: &v, Reason: "ok", AttemptIDs: []string{"real-success"}}
				})
				if err != nil {
					t.Fatal(err)
				}
				if !r.Completed || r.VerifiedLower == nil || *r.VerifiedLower != min(limit, s.High) {
					t.Fatalf("limit %d: %+v", limit, r)
				}
				if limit < s.High {
					if r.ProvenFailureUpper == nil || *r.ProvenFailureUpper != limit+1 {
						t.Fatalf("wrong upper: %+v", r)
					}
				} else if r.ProvenFailureUpper != nil || r.Stop != "configured_bound" {
					t.Fatalf("invented true maximum: %+v", r)
				}
			}
		})
	}
}
func TestCapacityExternalOrShortDoesNotShrinkUpper(t *testing.T) {
	for _, reason := range []string{"timeout", "network_error", "external_error", "key_unavailable", "ok"} {
		t.Run(reason, func(t *testing.T) {
			s := searchSettings()
			actual := int64(7)
			r, err := SearchCapacity(context.Background(), s, "max_output", "tokens", nil, func(_ context.Context, n int64) CapacityProbe {
				return CapacityProbe{Accepted: reason == "ok", Semantic: reason == "ok", Actual: &actual, Reason: reason, AttemptIDs: []string{"attempt"}}
			})
			if err != nil {
				t.Fatal(err)
			}
			if !r.Completed || r.Stop != "unconfirmed" || r.ParameterRejectedUpper != nil || r.ProvenFailureUpper != nil || len(r.Steps) != s.Rechecks+1 {
				t.Fatalf("external/short changed upper: %+v", r)
			}
			if reason == "ok" {
				if r.ParameterAcceptedLower == nil || *r.ParameterAcceptedLower != 500 || r.VerifiedLower == nil || *r.VerifiedLower != 7 {
					t.Fatalf("acceptance confused with actual: %+v", r)
				}
			} else if r.VerifiedLower != nil || r.ParameterAcceptedLower != nil {
				t.Fatalf("external error proved capacity: %+v", r)
			}
		})
	}
}
func TestCapacityApproximateCountAndCancellation(t *testing.T) {
	s := searchSettings()
	declared := int64(128000)
	r, err := SearchCapacity(context.Background(), s, "context", "tokens", &declared, func(_ context.Context, n int64) CapacityProbe {
		return CapacityProbe{Accepted: true, Semantic: true, Actual: &n, Approximate: true, Reason: "ok", AttemptIDs: []string{"estimated-count"}}
	})
	if err != nil || r.VerifiedLower != nil || r.ProvenFailureUpper != nil || r.Declared == nil || *r.Declared != 128000 {
		t.Fatalf("estimate/declared became proof: %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	r, err = SearchCapacity(ctx, s, "context", "tokens", nil, func(_ context.Context, n int64) CapacityProbe {
		calls++
		cancel()
		return CapacityProbe{Reason: "external_error", AttemptIDs: []string{"cancelled-attempt"}}
	})
	if !errors.Is(err, context.Canceled) || r.Completed || calls != 1 || r.Stop != "cancelled" {
		t.Fatalf("cancelled search became complete: %+v %v", r, err)
	}
}
func TestCapacityParameterLimitDoesNotInventMeasuredUpper(t *testing.T) {
	s := searchSettings()
	s.Rounds = 1
	r, err := SearchCapacity(context.Background(), s, "context", "tokens", nil, func(_ context.Context, n int64) CapacityProbe {
		return CapacityProbe{LengthError: true, Reason: "unsupported", AttemptIDs: []string{"native-limit-without-count"}}
	})
	if err != nil || !r.Completed || r.ParameterRejectedUpper == nil || *r.ParameterRejectedUpper != 500 || r.ProvenFailureUpper != nil {
		t.Fatalf("requested length became token measurement: %+v %v", r, err)
	}
}
