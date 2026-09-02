package money

import (
	"math"
	"testing"
)

func TestParseAndFormat(t *testing.T) {
	tests := map[string]string{
		"0":                     "0",
		"10.00":                 "10",
		"14.4":                  "14.4",
		"0.000000001":           "0.000000001",
		"-8.20":                 "-8.2",
		"9223372036.854775807":  "9223372036.854775807",
		"-9223372036.854775808": "-9223372036.854775808",
	}
	for input, expected := range tests {
		amount, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if got := amount.String(); got != expected {
			t.Fatalf("Parse(%q).String() = %q, want %q", input, got, expected)
		}
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", ".1", "1.", "1.0000000001", "one", "--1", "9223372036.854775808", "-9223372036.854775809"} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", input)
		}
	}
}

func TestAmountStringHandlesIntegerLimits(t *testing.T) {
	if got := Amount(math.MaxInt64).String(); got != "9223372036.854775807" {
		t.Fatalf("MaxInt64 string = %q", got)
	}
	if got := Amount(math.MinInt64).String(); got != "-9223372036.854775808" {
		t.Fatalf("MinInt64 string = %q", got)
	}
}
