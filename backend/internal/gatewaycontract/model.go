package gatewaycontract

import (
	"errors"
	"net/url"
	"strings"
)

var ErrModel = errors.New("invalid model identifier")

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

// ResolveModel applies exactly one Key alias before permission and candidate
// checks. Display names and supplier upstream names never define aggregation.
func ResolveModel(client string, aliases map[string]string) (string, error) {
	if !ValidModelID(client) {
		return "", ErrModel
	}
	if target, exists := aliases[client]; exists {
		client = target
	}
	if !ValidModelID(client) {
		return "", ErrModel
	}
	return client, nil
}

// ModelActionPath encodes the model as a single segment; callers concatenate it
// only with their validated fixed base URL. Neither model nor action is a URL.
func ModelActionPath(model, action string) (string, error) {
	if !ValidModelID(model) || (action != "generateContent" && action != "streamGenerateContent" && action != "countTokens") {
		return "", ErrModel
	}
	return "/v1beta/models/" + url.PathEscape(model) + ":" + action, nil
}
