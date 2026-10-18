package fingerprint

import (
	"errors"
	"math"
	"slices"
)

// Sample is transient. Callers must never serialize it into a detection report.
type Sample struct {
	Text          string
	ExpectedCount int
}

// Samples outside this bounded scoring input are rejected, never truncated into
// a valid fingerprint. A normal 332-number answer is far below this limit.
const MaxSampleBytes = 256 << 10

type Diagnostic struct {
	Index          int  `json:"index"`
	ParsedNumbers  int  `json:"parsed_numbers"`
	MinimumNumbers int  `json:"minimum_numbers"`
	Accepted       bool `json:"accepted"`
	OutOfRange     int  `json:"raw_out_of_range_count"`
}
type Ranking struct {
	Model       string   `json:"model"`
	DisplayName string   `json:"display_name"`
	Family      string   `json:"family"`
	FamilyName  string   `json:"family_name"`
	Score       float64  `json:"score"`
	Probability *float64 `json:"probability"`
}
type Report struct {
	Version           string       `json:"version"`
	Commit            string       `json:"commit"`
	DetectorSHA256    string       `json:"detector_sha256"`
	ReferenceSHA256   string       `json:"reference_sha256"`
	BaseSHA256        string       `json:"base_sha256"`
	BuiltAt           string       `json:"reference_built_at"`
	CalibrationSHA256 string       `json:"calibration_sha256"`
	Decision          string       `json:"decision"`
	UsedOutputs       int          `json:"used_outputs"`
	Diagnostics       []Diagnostic `json:"diagnostics"`
	ProbabilityScope  string       `json:"probability_scope"`
	Results           []Ranking    `json:"results"`
}

func (d *detector) transform(blocks matrix, params []preprocessing, weights vector) vector {
	var out vector
	for j, block := range blocks {
		m, s := d.v(params[j].Mean), d.v(params[j].Scale)
		centered := make(vector, len(block))
		for i, x := range block {
			centered[i] = (x - m[i]) / s[i]
		}
		part := normalize(centered)
		for i := range part {
			part[i] *= math.Sqrt(weights[j])
		}
		out = append(out, part...)
	}
	return out
}
func (d *detector) centered(v vector, p featureBank) vector {
	m, s := d.v(p.Mean), d.v(p.Scale)
	out := make(vector, len(v))
	for i, x := range v {
		out[i] = (x - m[i]) / s[i]
	}
	return out
}
func (d *detector) baseline(blocks matrix) vector {
	h, o := d.Ranker.Bank.Hellinger, d.Ranker.Bank.Ordered
	hUnit := normalize(subtract(d.centered(blocks[0], h), d.rows(h.Basis)))
	marginal := make(vector, len(h.Centroids))
	for i, row := range h.Centroids {
		marginal[i] = dot(hUnit, d.v(row))
	}
	oCentered := d.centered(blocks[1], o)
	raw, projected := normalize(oCentered), normalize(subtract(oCentered, d.rows(o.Basis)))
	templates, nuisance := make(vector, len(o.Centroids)), make(vector, len(o.Centroids))
	for i, row := range o.Centroids {
		templates[i] = math.Inf(-1)
		for _, env := range o.Environments {
			templates[i] = math.Max(templates[i], dot(raw, d.v(env[i])))
		}
		nuisance[i] = dot(projected, d.v(row))
	}
	tz, nz := z(templates), z(nuisance)
	for i := range tz {
		tz[i] = .5*tz[i] + .5*nz[i]
	}
	ordered, mz := z(tz), z(marginal)
	for i := range mz {
		mz[i] = .5*mz[i] + .5*ordered[i]
	}
	return mz
}
func median(a vector) float64 {
	v := slices.Clone(a)
	slices.Sort(v)
	i := len(v) / 2
	if len(v)%2 != 0 {
		return v[i]
	}
	return (v[i-1] + v[i]) / 2
}
func (d *detector) nearest(x vector, model int) float64 {
	refs := d.Ranker.References[model]
	xx := dot(x, x)
	distances := make(vector, len(refs))
	for i, r := range refs {
		distances[i] = math.Max(0, xx+d.referenceNorms[model][i]-2*dot(x, d.v(r)))
	}
	slices.Sort(distances)
	return mean(distances[:min(7, len(distances))])
}
func (d *detector) positional(numbers []int) vector {
	a := d.Ranker.Positional
	x := d.transform(matrix{hellinger(numbers), ordered(numbers), positionKernel(numbers), sequence(numbers)}, a.Params, vector{.6, .15, .125, .125})
	bias := d.v(a.Bias)
	out := make(vector, len(a.Weights))
	// The positional implementation begins accumulation at bias; the LDA below
	// adds bias after dot. Preserve both orders for parity with the pinned code.
	for i, row := range a.Weights {
		sum := bias[i]
		for j, w := range d.v(row) {
			sum += w * x[j]
		}
		out[i] = sum
	}
	return out
}
func (d *detector) rank(numbers [][]int) vector {
	a := d.Ranker
	ax, lda, base, pos := make(matrix, len(numbers)), make(matrix, len(numbers)), make(matrix, len(numbers)), make(matrix, len(numbers))
	for j, n := range numbers {
		blocks := matrix{hellinger(n), ordered(n)}
		ax[j] = d.transform(blocks, a.Full, vector{.75, .25})
		head := n[:min(128, len(n))]
		x := d.transform(matrix{hellinger(head), ordered(head)}, a.Head, vector{.75, .25})
		decision := make(vector, len(a.LDA))
		bias := d.v(a.Bias)
		for i, row := range a.LDA {
			decision[i] = dot(d.v(row), x) + bias[i]
		}
		lda[j] = z(decision)
		base[j] = d.baseline(blocks)
		pos[j] = z(d.positional(n))
	}
	l := z(columnMean(lda))
	nearest := make(vector, len(a.References))
	for i := range nearest {
		ds := make(vector, len(ax))
		for j, x := range ax {
			ds[j] = -d.nearest(x, i)
		}
		nearest[i] = median(ds)
	}
	near, b := z(nearest), z(columnMean(base))
	f := make(vector, len(l))
	for i, x := range l {
		f[i] = .5*x + .25*near[i] + .25*b[i]
	}
	f = z(f)
	positional := z(columnMean(pos))
	for i := range f {
		f[i] = a.Blend.Frequency*f[i] + a.Blend.Positional*positional[i]
	}
	return f
}

// Analyze follows the fixed strict three-sample profile. Fewer than three valid
// samples produce diagnostics, never a calibrated or partially invented score.
func Analyze(samples []Sample) (Report, error) {
	d, err := loadPinned()
	if err != nil {
		return Report{}, err
	}
	r := Report{Version: Version, Commit: Commit, DetectorSHA256: DetectorSHA256, ReferenceSHA256: ReferenceSHA256, BaseSHA256: d.Base, BuiltAt: d.BuiltAt, CalibrationSHA256: d.CalibrationSHA256, Decision: "unscorable", ProbabilityScope: "unavailable", Results: []Ranking{}, Diagnostics: []Diagnostic{}}
	if len(samples) > 3 {
		return r, errors.New("fingerprint requires exactly three samples")
	}
	numbers := make([][]int, len(samples))
	for i, s := range samples {
		if s.ExpectedCount < 292 || s.ExpectedCount > 332 || len(s.Text) > MaxSampleBytes {
			return r, errors.New("fingerprint sample outside pinned profile")
		}
		numbers[i] = ParseNumbers(s.Text)
		minimum := max(80, int(math.Ceil(float64(s.ExpectedCount)*.55)))
		diag := Diagnostic{Index: i, ParsedNumbers: len(numbers[i]), MinimumNumbers: minimum, Accepted: len(numbers[i]) >= minimum}
		// Signed raw diagnostics do not alter the pinned positive-digit parser.
		for _, loc := range digits.FindAllStringIndex(s.Text, -1) {
			value := s.Text[loc[0]:loc[1]]
			if len(value) > 3 {
				diag.OutOfRange++
				continue
			}
			var n int
			for _, c := range value {
				n = n*10 + int(c-'0')
			}
			if n < 1 || n > 355 || (loc[0] > 0 && s.Text[loc[0]-1] == '-') {
				diag.OutOfRange++
			}
		}
		if diag.Accepted {
			r.UsedOutputs++
		}
		r.Diagnostics = append(r.Diagnostics, diag)
	}
	if len(samples) != 3 || r.UsedOutputs != 3 {
		return r, nil
	}
	scores := d.rank(numbers)
	probabilities := make(vector, len(scores))
	calibrated := d.calibrated()
	for _, s := range scores {
		if math.IsNaN(s) || math.IsInf(s, 0) {
			return r, errors.New("non-finite fingerprint ranking")
		}
	}
	if calibrated {
		tau := d.Calibration.Tau
		maximum := slices.Max(scores) * tau
		var total float64
		for i, s := range scores {
			probabilities[i] = math.Exp(tau*s - maximum)
			total += probabilities[i]
		}
		for i := range probabilities {
			probabilities[i] /= total
		}
		r.ProbabilityScope = "reference-closed-set"
	}
	order := make([]int, len(scores))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(i, j int) int {
		if scores[j] > scores[i] {
			return 1
		}
		if scores[j] < scores[i] {
			return -1
		}
		return 0
	})
	for _, i := range order {
		m := d.models[i]
		item := Ranking{Model: m.ID, DisplayName: m.DisplayName, Family: m.Family, FamilyName: m.FamilyName, Score: scores[i]}
		if calibrated {
			p := probabilities[i]
			item.Probability = &p
		}
		r.Results = append(r.Results, item)
	}
	r.Decision = "ranked"
	return r, nil
}

// ValidReport checks the metadata-only representation accepted by detection
// storage. It cannot re-prove samples that are intentionally never persisted.
func ValidReport(r Report) bool {
	d, err := loadPinned()
	if err != nil {
		return false
	}
	if r.Version != Version || r.Commit != Commit || r.DetectorSHA256 != DetectorSHA256 || r.ReferenceSHA256 != ReferenceSHA256 || r.BaseSHA256 != d.Base || r.BuiltAt != d.BuiltAt || r.CalibrationSHA256 != d.CalibrationSHA256 || len(r.Diagnostics) > 3 || r.UsedOutputs < 0 || r.UsedOutputs > 3 {
		return false
	}
	accepted := 0
	for i, v := range r.Diagnostics {
		if v.Index != i || v.ParsedNumbers < 0 || v.ParsedNumbers > MaxSampleBytes || v.MinimumNumbers < 161 || v.MinimumNumbers > 183 || v.OutOfRange < 0 || v.OutOfRange > MaxSampleBytes || v.Accepted != (v.ParsedNumbers >= v.MinimumNumbers) {
			return false
		}
		if v.Accepted {
			accepted++
		}
	}
	if accepted != r.UsedOutputs {
		return false
	}
	if r.Decision == "unscorable" {
		return len(r.Results) == 0 && r.ProbabilityScope == "unavailable" && (len(r.Diagnostics) < 3 || accepted < 3)
	}
	if r.Decision != "ranked" || accepted != 3 || len(r.Results) != len(d.models) {
		return false
	}
	models := map[string]model{}
	for _, m := range d.models {
		models[m.ID] = m
	}
	seen := map[string]bool{}
	var total float64
	for i, item := range r.Results {
		m, ok := models[item.Model]
		if !ok || seen[item.Model] || item.DisplayName != m.DisplayName || item.Family != m.Family || item.FamilyName != m.FamilyName || math.IsNaN(item.Score) || math.IsInf(item.Score, 0) || (i > 0 && item.Score > r.Results[i-1].Score) {
			return false
		}
		seen[item.Model] = true
		if r.ProbabilityScope == "reference-closed-set" {
			if !d.calibrated() || item.Probability == nil || math.IsNaN(*item.Probability) || math.IsInf(*item.Probability, 0) || *item.Probability < 0 || *item.Probability > 1 {
				return false
			}
			total += *item.Probability
		} else if r.ProbabilityScope != "unavailable" || item.Probability != nil {
			return false
		}
	}
	return r.ProbabilityScope != "reference-closed-set" || math.Abs(total-1) < 1e-9
}
