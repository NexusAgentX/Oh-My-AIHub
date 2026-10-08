package main

import "testing"

func TestParseTrustedProxyCIDRsMasksAndRejectsInvalidEntries(t *testing.T) {
	prefixes, err := parseTrustedProxyCIDRs(" 10.1.2.3/8 , 192.168.0.0/16")
	if err != nil || len(prefixes) != 2 || prefixes[0].String() != "10.0.0.0/8" {
		t.Fatalf("prefixes = %v, %v", prefixes, err)
	}
	if _, err := parseTrustedProxyCIDRs("not-a-cidr"); err == nil {
		t.Fatal("invalid CIDR accepted")
	}
	if prefixes, err := parseTrustedProxyCIDRs(" "); err != nil || prefixes != nil {
		t.Fatalf("empty = %v, %v", prefixes, err)
	}
}

func TestParseCommaSeparatedTrimsEmptyEntries(t *testing.T) {
	values := parseCommaSeparated(" 443, ,8443 ")
	if len(values) != 2 || values[0] != "443" || values[1] != "8443" {
		t.Fatalf("values = %v", values)
	}
}
