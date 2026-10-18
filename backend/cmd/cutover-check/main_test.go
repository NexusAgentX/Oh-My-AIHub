package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
)

type evidenceReader struct {
	m        cutover.Manifest
	err      error
	baseline *cutover.Manifest
}

func (r *evidenceReader) Inspect(_ context.Context, before *cutover.Manifest) (cutover.Manifest, error) {
	r.baseline = before
	return r.m, r.err
}
func connectReader(r *evidenceReader) opener {
	return func(context.Context, string) (inspector, func(), error) { return r, func() {}, nil }
}
func TestInspectionCreatesPrivateEvidenceAndCannotOverwriteIt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "before.json")
	r := &evidenceReader{m: cutover.Manifest{Format: cutover.Format, Tables: []cutover.Digest{}}}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--out", file}, "synthetic", &output, connectReader(r)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("evidence permissions", info, err)
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cutover.ReadEvidence(bytes.NewReader(contents), strings.TrimSpace(output.String())); err != nil {
		t.Fatal(err)
	}
	if err = run(context.Background(), []string{"--out", file}, "synthetic", &output, connectReader(r)); err == nil {
		t.Fatal("baseline overwritten")
	}
	unchanged, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(contents, unchanged) {
		t.Fatal("baseline changed", err)
	}
}
func TestVerificationRejectsTamperedBaselineBeforeConnecting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "before.json")
	if err := os.WriteFile(file, []byte(`{"format":"market-preservation.v1","migration_version":0,"tables":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	connected := false
	connect := func(context.Context, string) (inspector, func(), error) {
		connected = true
		return nil, nil, errors.New("should not connect")
	}
	if err := run(context.Background(), []string{"--baseline", file, "--sha256", strings.Repeat("a", 64)}, "synthetic", &bytes.Buffer{}, connect); err == nil || connected {
		t.Fatal("corrupt baseline reached database", err, connected)
	}
}
func TestVerificationCarriesProtectedScopesToOneReadOnlyInspection(t *testing.T) {
	m := cutover.Manifest{Format: cutover.Format, Tables: []cutover.Digest{}}
	sum, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "before.json")
	if err = os.WriteFile(file, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	r := &evidenceReader{m: m}
	var out bytes.Buffer
	if err = run(context.Background(), []string{"--baseline", file, "--sha256", sum}, "synthetic", &out, connectReader(r)); err != nil || r.baseline == nil || !strings.Contains(out.String(), `"matches":true`) {
		t.Fatal("verification failed", err, out.String())
	}
	for _, args := range [][]string{{}, {"--out", file, "--baseline", file, "--sha256", sum}, {"--cleanup"}, {"--out", file, "extra"}} {
		if err = run(context.Background(), args, "synthetic", &out, connectReader(r)); err == nil {
			t.Fatal("ambiguous or mutation operation allowed", args)
		}
	}
}
