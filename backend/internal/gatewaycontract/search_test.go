package gatewaycontract

import (
	"math"
	"testing"
)

func TestSearchSettingsValidation(t *testing.T) {
	valid := SearchSettings{Version: "v1", Method: "binary", Low: 0, High: 1000, HardMaximum: 2000, Precision: 1, PrecisionMode: "absolute", Rounds: 10}
	for _, method := range []string{"binary", "ternary"} {
		for _, mode := range []string{"absolute", "relative"} {
			settings := valid
			settings.Method, settings.PrecisionMode = method, mode
			if mode == "relative" {
				settings.Precision = 0.01
			}
			if err := settings.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"version", "method", "low", "range", "hard_max", "rounds", "rechecks", "nan", "inf", "zero", "negative", "mode", "relative"} {
		t.Run(name, func(t *testing.T) {
			s := valid
			switch name {
			case "version":
				s.Version = ""
			case "method":
				s.Method = "unbounded"
			case "low":
				s.Low = -1
			case "range":
				s.Low = s.High
			case "hard_max":
				s.High = s.HardMaximum + 1
			case "rounds":
				s.Rounds = 0
			case "rechecks":
				s.Rechecks = -1
			case "nan":
				s.Precision = math.NaN()
			case "inf":
				s.Precision = math.Inf(1)
			case "zero":
				s.Precision = 0
			case "negative":
				s.Precision = -1
			case "mode":
				s.PrecisionMode = "unknown"
			case "relative":
				s.PrecisionMode, s.Precision = "relative", 1.1
			}
			if s.Validate() == nil {
				t.Fatal("invalid search settings accepted")
			}
		})
	}
}
