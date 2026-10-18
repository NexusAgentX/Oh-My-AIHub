package gatewaycontract

import (
	"errors"
	"math"
)

// SearchSettings is the pinned per-task algorithm configuration, not a full-test
// cost, request-count or duration budget. F3 owns admin persistence; F4 owns real
// upstream probes and the distinction between accepted and achieved lengths.
type SearchSettings struct {
	Version       string  `json:"version"`
	Method        string  `json:"method"`
	Low           int64   `json:"low"`
	High          int64   `json:"high"`
	HardMaximum   int64   `json:"hard_maximum"`
	Precision     float64 `json:"precision"`
	PrecisionMode string  `json:"precision_mode"`
	Rounds        int     `json:"rounds"`
	Rechecks      int     `json:"rechecks"`
}

func (s SearchSettings) Validate() error {
	if s.Version == "" || (s.Method != "binary" && s.Method != "ternary") || s.Low < 0 || s.Low >= s.High || s.High > s.HardMaximum ||
		s.Rounds < 1 || s.Rechecks < 0 || math.IsNaN(s.Precision) || math.IsInf(s.Precision, 0) || s.Precision <= 0 ||
		(s.PrecisionMode != "absolute" && s.PrecisionMode != "relative") || (s.PrecisionMode == "relative" && s.Precision > 1) {
		return errors.New("invalid capacity search settings")
	}
	return nil
}
