package gatewaycontract

import (
	"errors"
	"math"
)

// SearchSettings is the pinned per-task algorithm configuration, not a full-test
// cost, request-count or duration budget. F3 owns admin persistence; F4 owns real
// upstream probes and the distinction between accepted and achieved lengths.
type SearchSettings struct {
	Version       string
	Method        string
	Low           int64
	High          int64
	HardMaximum   int64
	Precision     float64
	PrecisionMode string
	Rounds        int
	Rechecks      int
}

func (s SearchSettings) Validate() error {
	if s.Version == "" || (s.Method != "binary" && s.Method != "ternary") || s.Low < 0 || s.Low >= s.High || s.High > s.HardMaximum ||
		s.Rounds < 1 || s.Rechecks < 0 || math.IsNaN(s.Precision) || math.IsInf(s.Precision, 0) || s.Precision <= 0 ||
		(s.PrecisionMode != "absolute" && s.PrecisionMode != "relative") || (s.PrecisionMode == "relative" && s.Precision > 1) {
		return errors.New("invalid capacity search settings")
	}
	return nil
}
