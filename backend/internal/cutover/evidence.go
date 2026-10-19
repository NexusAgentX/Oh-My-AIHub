package cutover

import (
	"encoding/json"
	"io"
)

const MaxEvidenceBytes = 4 << 20

// ReadEvidence requires an independently retained digest. A truncated, edited,
// extra-field or trailing document is never accepted as a complete baseline.
func ReadEvidence(r io.Reader, expectedSHA256 string) (Manifest, error) {
	var m Manifest
	if len(expectedSHA256) != 64 {
		return m, ErrInvalid
	}
	limited := &io.LimitedReader{R: r, N: MaxEvidenceBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || limited.N <= 0 {
		return Manifest{}, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || limited.N <= 0 {
		return Manifest{}, ErrInvalid
	}
	sum, err := m.SHA256()
	if err != nil || sum != expectedSHA256 {
		return Manifest{}, ErrInvalid
	}
	return m, nil
}
