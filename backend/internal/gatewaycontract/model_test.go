package gatewaycontract

import (
	"net/url"
	"strings"
	"testing"
)

func TestNativeModelIDsAndEncodedPaths(t *testing.T) {
	for _, id := range []string{"gpt-6.1-sol", "vendor/Model:latest", "a+b", "model@revision"} {
		p, err := ModelActionPath(id, "generateContent")
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse("https://gateway.invalid" + p)
		if err != nil {
			t.Fatal(err)
		}
		segment := strings.TrimSuffix(strings.TrimPrefix(u.EscapedPath(), "/v1beta/models/"), ":generateContent")
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded != id || u.Host != "gateway.invalid" || u.RawQuery != "" {
			t.Fatalf("path changed identity: %s", p)
		}
	}
	for _, id := range []string{"", "a/../b", "../x", "a//b", "a/", "https://evil.invalid", "a?key=x", "a#fragment", "a%2Fb", "a\\b", "a\nb", strings.Repeat("a", 257)} {
		if ValidModelID(id) {
			t.Errorf("unsafe model accepted %q", id)
		}
	}
	if _, err := ModelActionPath("safe", "../../files"); err == nil {
		t.Fatal("unsafe action accepted")
	}
}

func TestAliasResolutionIsSingleHopAndCaseSensitive(t *testing.T) {
	aliases := map[string]string{"friendly": "vendor/Model:v1", "vendor/Model:v1": "other", "bad": "../../files"}
	if got, err := ResolveModel("friendly", aliases); err != nil || got != "vendor/Model:v1" {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := ResolveModel("Friendly", aliases); got != "Friendly" {
		t.Fatal("case folded")
	}
	if _, err := ResolveModel("bad", aliases); err == nil {
		t.Fatal("unsafe alias target accepted")
	}
}
