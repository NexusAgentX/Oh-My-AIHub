package main

import "testing"

func TestParseTrustedProxyCIDRs(t *testing.T) {
	prefixes, err := parseTrustedProxyCIDRs("172.16.0.0/12, 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 || prefixes[0].String() != "172.16.0.0/12" || prefixes[1].String() != "2001:db8::/32" {
		t.Fatalf("unexpected prefixes: %v", prefixes)
	}
	if _, err := parseTrustedProxyCIDRs("not-a-cidr"); err == nil {
		t.Fatal("invalid trusted proxy CIDR unexpectedly succeeded")
	}
}
