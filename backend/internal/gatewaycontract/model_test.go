package gatewaycontract

import (
	"strings"
	"testing"
)

func TestNativeModelIDsPreserveNamespaces(t *testing.T) {
	for _, id := range []string{"gpt-6.1-sol", "vendor/Model:latest", "a+b", "model@revision"} {
		if !ValidModelID(id) {
			t.Errorf("safe model rejected %q", id)
		}
	}
	for _, id := range []string{"", "a/../b", "../x", "a//b", "a/", "https://evil.invalid", "a?key=x", "a#fragment", "a%2Fb", "a\\b", "a\nb", strings.Repeat("a", 257)} {
		if ValidModelID(id) {
			t.Errorf("unsafe model accepted %q", id)
		}
	}
}
