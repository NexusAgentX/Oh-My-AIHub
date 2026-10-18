// Package fingerprint implements the pinned LMFPD shared-detector-v2 ranker.
// It never performs I/O to upstreams, retains sample text, or certifies identity.
// Algorithm and embedded reference attribution: data/source.json and
// data/LMFPD-LICENSE.txt (MIT, Copyright (c) 2026 xqy2006).
package fingerprint

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
)

const (
	Version         = "lmfpd/0.0.1792242250521"
	Commit          = "50a364bc686279094265f34f18f5501eac1d73f4"
	DetectorSHA256  = "9117330f225a525d39d75db2616e90657e6b5d9ea1af46770389530de0a418fe"
	ReferenceSHA256 = "3b0bc4477609f0fc0e98fb25b26a1e7393e550e4c65a40fe65813aa0a81301f5"
)

//go:embed data/*
var assets embed.FS

//go:embed *.go
var implementation embed.FS

// ImplementationVersion also invalidates evidence when this Go port changes,
// even if the upstream algorithm/reference pin stays the same.
func ImplementationVersion() string {
	h := sha256.New()
	entries, _ := implementation.ReadDir(".")
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, _ := implementation.ReadFile(e.Name())
		h.Write([]byte(e.Name()))
		h.Write([]byte{0})
		h.Write(body)
	}
	for _, name := range []string{"data/challenges.json", "data/reference.json", "data/source.json"} {
		body, _ := assets.ReadFile(name)
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type vector []float64
type matrix []vector
type ref struct {
	Offset int `json:"$f32"`
	N      int `json:"n"`
}
type preprocessing struct {
	Mean  ref `json:"mean"`
	Scale ref `json:"scale"`
}
type featureBank struct {
	Mean         ref     `json:"feature_mean"`
	Scale        ref     `json:"feature_scale"`
	Basis        []ref   `json:"nuisance_basis"`
	Centroids    []ref   `json:"centroids"`
	Environments [][]ref `json:"environment_centroids"`
}
type model struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Family        string `json:"family"`
	FamilyName    string `json:"family_name"`
	ResponseCount int    `json:"response_count"`
}
type artifact struct {
	Schema            string   `json:"schema"`
	Base              string   `json:"base_sha256"`
	SourceReference   string   `json:"source_reference_sha256"`
	BuiltAt           string   `json:"bank_built_at"`
	ModelIDs          []string `json:"model_ids"`
	Counts            ref      `json:"response_counts"`
	CalibrationSHA256 string   `json:"calibration_sha256"`
	Calibration       *struct {
		Schema  string  `json:"schema"`
		Method  string  `json:"method"`
		Tau     float64 `json:"tau"`
		Run     string  `json:"calibration_run"`
		Binding struct {
			Base      string   `json:"base_sha256"`
			Reference string   `json:"reference_sha256"`
			Models    []string `json:"model_ids"`
		} `json:"binding"`
	} `json:"calibration"`
	Ranker struct {
		Head       []preprocessing `json:"head_params"`
		Full       []preprocessing `json:"full_params"`
		LDA        []ref           `json:"lda_weights"`
		Bias       ref             `json:"lda_bias"`
		References [][]ref         `json:"references"`
		Bank       struct {
			Hellinger featureBank `json:"hellinger"`
			Ordered   featureBank `json:"ordered_blocks"`
		} `json:"bank"`
		Blend struct {
			Frequency  float64 `json:"frequency"`
			Positional float64 `json:"positional"`
		} `json:"blend"`
		Positional struct {
			Params  []preprocessing `json:"params"`
			Weights []ref           `json:"weights"`
			Bias    ref             `json:"bias"`
		} `json:"positional"`
	} `json:"ranker"`
}
type detector struct {
	artifact
	values         vector
	models         []model
	referenceNorms matrix
}

var loadPinned = sync.OnceValues(func() (*detector, error) {
	compressed, err := assets.ReadFile("data/shared_detector.bin.gz")
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, 5<<20))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != DetectorSHA256 || len(raw) < 4 {
		return nil, errors.New("fingerprint artifact checksum")
	}
	n := int(binary.LittleEndian.Uint32(raw[:4]))
	start := (4 + n + 3) / 4 * 4
	if n < 1 || start > len(raw) || (len(raw)-start)%4 != 0 {
		return nil, errors.New("fingerprint artifact layout")
	}
	d := new(detector)
	if err := json.Unmarshal(raw[4:4+n], &d.artifact); err != nil {
		return nil, err
	}
	count := (len(raw) - start) / 4
	d.values = make(vector, count)
	for i := range d.values {
		bits := uint32(raw[start+i]) | uint32(raw[start+count+i])<<8 | uint32(raw[start+2*count+i])<<16 | uint32(raw[start+3*count+i])<<24
		d.values[i] = float64(math.Float32frombits(bits))
	}
	// Validate all numeric references before any slice operation. The artifact is
	// checksum pinned; this also catches accidental decoder/layout regressions.
	var header any
	if err := json.Unmarshal(raw[4:4+n], &header); err != nil {
		return nil, err
	}
	var valid func(any) bool
	valid = func(x any) bool {
		switch v := x.(type) {
		case map[string]any:
			if offset, ok := v["$f32"].(float64); ok {
				length, ok := v["n"].(float64)
				return ok && offset >= 0 && length >= 0 && offset+length <= float64(count) && math.Trunc(offset) == offset && math.Trunc(length) == length
			}
			for _, child := range v {
				if !valid(child) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !valid(child) {
					return false
				}
			}
		}
		return true
	}
	if !valid(header) {
		return nil, errors.New("fingerprint artifact references")
	}
	metadata, err := assets.ReadFile("data/reference.json")
	if err != nil {
		return nil, err
	}
	var bank struct {
		BuiltAt string  `json:"built_at"`
		Models  []model `json:"models"`
	}
	if err := json.Unmarshal(metadata, &bank); err != nil {
		return nil, err
	}
	if d.Schema != "shared-detector-v2" || d.BuiltAt != bank.BuiltAt || len(d.ModelIDs) != len(bank.Models) || d.Counts.N != len(bank.Models) {
		return nil, errors.New("fingerprint reference mismatch")
	}
	for i, m := range bank.Models {
		if m.ID != d.ModelIDs[i] || float64(m.ResponseCount) != d.v(d.Counts)[i] {
			return nil, errors.New("fingerprint reference model mismatch")
		}
	}
	d.models = bank.Models
	for _, refs := range d.Ranker.References {
		norms := make(vector, len(refs))
		for i, r := range refs {
			norms[i] = dot(d.v(r), d.v(r))
		}
		d.referenceNorms = append(d.referenceNorms, norms)
	}
	return d, nil
})

func (d *detector) v(r ref) vector { return d.values[r.Offset : r.Offset+r.N] }
func (d *detector) rows(refs []ref) matrix {
	out := make(matrix, len(refs))
	for i, r := range refs {
		out[i] = d.v(r)
	}
	return out
}
func (d *detector) calibrated() bool {
	c := d.Calibration
	return c != nil && c.Schema == "shared-confidence-v2" && c.Method == "ranking-temperature" && !math.IsNaN(c.Tau) && !math.IsInf(c.Tau, 0) && c.Tau >= .001 && c.Tau <= 1000 && c.Binding.Base == d.Base && c.Binding.Reference == d.SourceReference && slices.Equal(c.Binding.Models, d.ModelIDs)
}
