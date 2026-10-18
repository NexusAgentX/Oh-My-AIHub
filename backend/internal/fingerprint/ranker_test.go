package fingerprint

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

// Golden values were produced offline by the original pinned TypeScript
// decoder/ranker, not this port, using deterministic synthetic samples. They
// verify all 60 scores/probabilities, not an identity claim about the samples.
func TestOfficialRankerParity(t *testing.T) {
	body, err := os.ReadFile("testdata/official-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Commit string `json:"source_commit"`
		Cases  []struct {
			Pattern string `json:"pattern"`
			Outputs []struct {
				Text  string `json:"text"`
				Count int    `json:"expected_count"`
			} `json:"outputs"`
			Results     []Ranking    `json:"results"`
			Diagnostics []Diagnostic `json:"diagnostics"`
			Scope       string       `json:"probability_scope"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(body, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Commit != Commit || len(golden.Cases) != 3 {
		t.Fatal("unmatched golden provenance")
	}
	for _, test := range golden.Cases {
		t.Run(test.Pattern, func(t *testing.T) {
			samples := make([]Sample, len(test.Outputs))
			for i, o := range test.Outputs {
				samples[i] = Sample{Text: o.Text, ExpectedCount: o.Count}
			}
			r, err := Analyze(samples)
			if err != nil {
				t.Fatal(err)
			}
			if r.Decision != "ranked" || r.UsedOutputs != 3 || r.ProbabilityScope != test.Scope || len(r.Results) != 60 {
				t.Fatalf("invalid report: %+v", r)
			}
			if !slices.Equal(r.Diagnostics, test.Diagnostics) {
				t.Fatalf("diagnostics: %+v != %+v", r.Diagnostics, test.Diagnostics)
			}
			var total float64
			for i, want := range test.Results {
				got := r.Results[i]
				if got.Model != want.Model || got.DisplayName != want.DisplayName || got.Family != want.Family || got.FamilyName != want.FamilyName {
					t.Fatalf("rank %d got %+v want %+v", i, got, want)
				}
				if math.Abs(got.Score-want.Score) > 1e-9 || got.Probability == nil || want.Probability == nil || math.Abs(*got.Probability-*want.Probability) > 1e-9 {
					t.Fatalf("rank %d numeric mismatch got %+v want %+v", i, got, want)
				}
				total += *got.Probability
			}
			if math.Abs(total-1) > 1e-12 {
				t.Fatalf("probabilities sum to %g", total)
			}
		})
	}
}

func TestStrictSamplesAndNoBodyPersistence(t *testing.T) {
	full := Sample{Text: strings.Repeat("17, ", 300), ExpectedCount: 300}
	for _, samples := range [][]Sample{{}, {full}, {full, full}, {full, full, {Text: "too short 1 2", ExpectedCount: 300}}} {
		r, err := Analyze(samples)
		if err != nil {
			t.Fatal(err)
		}
		if r.Decision != "unscorable" || r.ProbabilityScope != "unavailable" || len(r.Results) != 0 {
			t.Fatalf("incomplete samples produced ranking: %+v", r)
		}
		encoded, _ := json.Marshal(r)
		if strings.Contains(string(encoded), "17, 17") || strings.Contains(string(encoded), "too short") {
			t.Fatal("sample body persisted")
		}
	}
	for _, count := range []int{0, 291, 333} {
		if _, err := Analyze([]Sample{{Text: full.Text, ExpectedCount: count}}); err == nil {
			t.Fatal("accepted invalid sample count")
		}
	}
	if _, err := Analyze([]Sample{full, full, full, full}); err == nil {
		t.Fatal("accepted extra samples")
	}
}

func TestPinnedParser(t *testing.T) {
	for _, test := range []struct {
		Text string
		Want []int
	}{
		{"-1,2,355,356,0", []int{1, 2, 355}},
		{"1 2 words 3 4", []int{1, 2}},
		{"1 2 汉字 3 4 5", []int{3, 4, 5}},
		{"1 2 9999999999999999999999999999999999 3", []int{1, 2, 3}},
		{"٤ ６ ９", nil},
	} {
		if got := ParseNumbers(test.Text); !slices.Equal(got, test.Want) {
			t.Fatalf("%q: %v != %v", test.Text, got, test.Want)
		}
	}
}

func TestChallengeProfile(t *testing.T) {
	for range 30 {
		challenges, err := Challenges()
		if err != nil {
			t.Fatal(err)
		}
		if len(challenges) != 3 {
			t.Fatal("not three challenges")
		}
		seen := map[int]bool{}
		for _, c := range challenges {
			if c.ExpectedCount < 292 || c.ExpectedCount > 332 || seen[c.ExpectedCount] || len(c.Prompt) < 100 || strings.Contains(c.Prompt, "{action}") || strings.Contains(c.Prompt, "{count}") {
				t.Fatalf("invalid challenge: %+v", c)
			}
			seen[c.ExpectedCount] = true
		}
	}
}
