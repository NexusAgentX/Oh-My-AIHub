package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestProxyTrustAndCanonicalAddresses(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	for _, tc := range []struct{ remote, header, want string }{
		{"198.51.100.2:90", "8.8.8.8", "198.51.100.2"},
		{"192.0.2.1:90", "::ffff:8.8.8.8", "8.8.8.8"},
		{"192.0.2.1:90", "8.8.8.8, 1.1.1.1", "192.0.2.1"},
		{"[2001:db8::1]:90", "8.8.8.8", "2001:db8::1"},
		{"192.0.2.1:90", "fe80::1%eth0", "192.0.2.1"},
		{"invalid", "8.8.8.8", ""},
	} {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Real-IP", tc.header)
		if got := Address(r, prefixes); got != tc.want {
			t.Errorf("%s %s: got %q want %q", tc.remote, tc.header, got, tc.want)
		}
	}
}
