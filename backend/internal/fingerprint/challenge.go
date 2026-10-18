package fingerprint

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"strings"
)

type language struct {
	Space      string   `json:"space"`
	Openings   []string `json:"openings"`
	Actions    []string `json:"actions"`
	Request    string   `json:"request"`
	Rules      string   `json:"rules"`
	Endings    []string `json:"endings"`
	Separators []string `json:"separators"`
	Start      string   `json:"start"`
}
type Challenge struct {
	ExpectedCount int
	Prompt        string
}

// Challenges mirrors the pinned multilingual generator with independent
// cryptographic choices and three distinct lengths, inclusive 292..332. Prompts
// exist only in the worker and are not report fields.
func Challenges() ([]Challenge, error) {
	body, err := assets.ReadFile("data/challenges.json")
	if err != nil {
		return nil, err
	}
	var languages []language
	if err = json.Unmarshal(body, &languages); err != nil {
		return nil, err
	}
	chooseIndex := func(n int) (int, error) {
		x, e := rand.Int(rand.Reader, big.NewInt(int64(n)))
		if e != nil {
			return 0, e
		}
		return int(x.Int64()), nil
	}
	choose := func(values []string) (string, error) {
		i, e := chooseIndex(len(values))
		if e != nil {
			return "", e
		}
		return values[i], nil
	}
	lengths := make([]int, 41)
	for i := range lengths {
		lengths[i] = 292 + i
	}
	out := make([]Challenge, 0, 3)
	for range 3 {
		i, e := chooseIndex(len(lengths))
		if e != nil {
			return nil, e
		}
		count := lengths[i]
		lengths = append(lengths[:i], lengths[i+1:]...)
		li, e := chooseIndex(len(languages))
		if e != nil {
			return nil, e
		}
		l := languages[li]
		opening, e := choose(l.Openings)
		if e != nil {
			return nil, e
		}
		action, e := choose(l.Actions)
		if e != nil {
			return nil, e
		}
		ending, e := choose(l.Endings)
		if e != nil {
			return nil, e
		}
		separator, e := choose(l.Separators)
		if e != nil {
			return nil, e
		}
		request := strings.NewReplacer("{action}", action, "{count}", big.NewInt(int64(count)).String()).Replace(l.Request)
		out = append(out, Challenge{ExpectedCount: count, Prompt: strings.Join([]string{opening, request, l.Rules, ending, separator, l.Start}, l.Space)})
	}
	return out, nil
}
