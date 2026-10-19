package cutover

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEvidenceRequiresCompletePinnedBaseline(t *testing.T) {
	m := manifest(t, 25, digest(t, `{"id":"a","balance":0}`))
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadEvidence(bytes.NewReader(encoded), sum); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{string(encoded[:len(encoded)/2]), string(encoded) + ` {}`, strings.Replace(string(encoded), `"migration_version":25`, `"migration_version":24`, 1), strings.Replace(string(encoded), `"format":`, `"unknown":true,"format":`, 1), strings.Repeat(" ", MaxEvidenceBytes+1) + string(encoded)} {
		if _, err = ReadEvidence(strings.NewReader(input), sum); !errors.Is(err, ErrInvalid) {
			t.Fatal("corrupt baseline accepted", err)
		}
	}
	if _, err = ReadEvidence(bytes.NewReader(encoded), strings.Repeat("f", 64)); err == nil {
		t.Fatal("unpinned baseline accepted")
	}
}
func TestLedgerGuardChangesCannotHideBehindIdenticalRows(t *testing.T) {
	before := manifest(t, 25, digest(t, "synthetic"))
	after := manifest(t, 26, digest(t, "synthetic"))
	after.Guards[0].SHA256 = strings.Repeat("b", 64)
	differences, err := Compare(before, after)
	if err != nil || len(differences) != 1 || differences[0].Table != "ledger_guards" {
		t.Fatal(differences, err)
	}
	after.Guards[0].Enabled = "D"
	if !errors.Is(after.Validate(), ErrIntegrity) {
		t.Fatal("disabled immutable trigger accepted")
	}
	after = manifest(t, 26, digest(t, "synthetic"))
	after.Checks[1].Violations = 2
	if !errors.Is(after.Validate(), ErrIntegrity) {
		t.Fatal("balance inconsistency accepted")
	}
	after = manifest(t, 26, digest(t, "synthetic"))
	after.Guards = after.Guards[1:]
	if !errors.Is(after.Validate(), ErrIncomplete) {
		t.Fatal("missing ledger guard accepted")
	}
}

func TestHistoryConstraintChangesAreDetectedWithIdenticalRows(t *testing.T) {
	before := manifest(t, 14, digest(t, "synthetic"))
	after := manifest(t, 26, digest(t, "synthetic"))
	after.Constraints[0].SHA256 = strings.Repeat("d", 64)
	differences, err := Compare(before, after)
	if err != nil || len(differences) != 1 || differences[0].Reason != "constraint_changed" {
		t.Fatal("schema protection silently lost", differences, err)
	}
	after.Constraints = after.Constraints[1:]
	if !errors.Is(after.Validate(), ErrIncomplete) {
		t.Fatal("required identity/ledger constraint missing")
	}
}
