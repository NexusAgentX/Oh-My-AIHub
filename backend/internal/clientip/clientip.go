// Package clientip applies the same proxy trust boundary to login and gateway requests.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func Remote(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return ip.Unmap(), err == nil && ip.Zone() == ""
}

func Trusted(r *http.Request, prefixes []netip.Prefix) bool {
	ip, ok := Remote(r)
	if !ok {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// Address only accepts X-Real-IP from an explicitly trusted immediate peer.
func Address(r *http.Request, prefixes []netip.Prefix) string {
	if Trusted(r, prefixes) {
		if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil && ip.Zone() == "" {
			return ip.Unmap().String()
		}
	}
	if ip, ok := Remote(r); ok {
		return ip.String()
	}
	return ""
}
