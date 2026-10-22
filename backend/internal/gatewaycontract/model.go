package gatewaycontract

import "strings"

// ValidModelID preserves exact case and native namespaces. It does not imply
// that the model exists or that a platform Key may call it.
func ValidModelID(id string) bool {
	if len(id) == 0 || len(id) > 256 || !alphaNumeric(id[0]) {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if !alphaNumeric(c) && !strings.ContainsRune("._-/:@+", rune(c)) {
			return false
		}
	}
	for _, part := range strings.Split(id, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func alphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
