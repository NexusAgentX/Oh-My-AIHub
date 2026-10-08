package apikey

import (
	"strings"
	"testing"
)

func TestGeneratedKeysAreUniquePrefixedAndHashedForLookup(t *testing.T) {
	first, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Generate()
	if first == second || !strings.HasPrefix(first, Prefix) || len(first) < len(Prefix)+40 {
		t.Fatalf("keys %q %q", first, second)
	}
	if string(Hash(first)) == string(Hash(second)) || len(Hash(first)) != 32 {
		t.Fatal("hash must be a distinct SHA-256 digest")
	}
	display := DisplayPrefix(first)
	if !strings.HasPrefix(first, display) || len(display) != len(Prefix)+4 {
		t.Fatalf("display prefix = %q", display)
	}
}
