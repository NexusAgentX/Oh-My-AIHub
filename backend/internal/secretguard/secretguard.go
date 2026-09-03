package secretguard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	CredentialErrorCode    = "upstream_error_contained_credential"
	CredentialErrorMessage = "upstream error contained a credential and was blocked"
)

// ContainsExact reports only whether one of the exact, non-empty credentials
// occurs in an untrusted upstream value. It intentionally does not perform
// pattern-based redaction: ordinary URLs, model identifiers, and error text
// remain untouched unless they contain the credential used for this request.
func ContainsExact(value string, credentials ...string) bool {
	for _, credential := range credentials {
		if credential != "" && strings.Contains(value, credential) {
			return true
		}
	}
	return false
}

func ContainsExactOrJSONEscaped(value string, credentials ...string) bool {
	if ContainsExact(value, credentials...) {
		return true
	}
	for _, credential := range credentials {
		if credential == "" {
			continue
		}
		encoded, err := json.Marshal(credential)
		if err != nil || len(encoded) < 2 {
			continue
		}
		escaped := string(encoded[1 : len(encoded)-1])
		if escaped != credential && strings.Contains(value, escaped) {
			return true
		}
		for _, encodedCredential := range []string{url.QueryEscape(credential), url.PathEscape(credential)} {
			if encodedCredential != credential && strings.Contains(value, encodedCredential) {
				return true
			}
		}
	}
	for _, unescape := range []func(string) (string, error){url.QueryUnescape, url.PathUnescape} {
		decoded, err := unescape(value)
		if err == nil && decoded != value && ContainsExact(decoded, credentials...) {
			return true
		}
	}
	if decoded := decodeJSONEscapesLenient(value); decoded != value && ContainsExact(decoded, credentials...) {
		return true
	}
	return false
}

func decodeJSONEscapesLenient(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] != '\\' || index+1 >= len(value) {
			result.WriteByte(value[index])
			index++
			continue
		}
		next := value[index+1]
		switch next {
		case '"', '\\', '/':
			result.WriteByte(next)
			index += 2
		case 'b':
			result.WriteByte('\b')
			index += 2
		case 'f':
			result.WriteByte('\f')
			index += 2
		case 'n':
			result.WriteByte('\n')
			index += 2
		case 'r':
			result.WriteByte('\r')
			index += 2
		case 't':
			result.WriteByte('\t')
			index += 2
		case 'u':
			if index+6 > len(value) {
				result.WriteByte(value[index])
				index++
				continue
			}
			parsed, err := strconv.ParseUint(value[index+2:index+6], 16, 16)
			if err != nil {
				result.WriteByte(value[index])
				index++
				continue
			}
			r := rune(parsed)
			if utf8.ValidRune(r) {
				result.WriteRune(r)
				index += 6
				continue
			}
			result.WriteByte(value[index])
			index++
		default:
			result.WriteByte(value[index])
			index++
		}
	}
	return result.String()
}

// ContainsExactInJSON decodes one complete JSON value before checking every
// string key and leaf. This catches semantically identical escape forms such
// as \uXXXX and \/ without treating arbitrary non-JSON text as sensitive.
func ContainsExactInJSON(encoded []byte, credentials ...string) bool {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false
	}
	return jsonValueContainsExact(value, credentials...)
}

func jsonValueContainsExact(value any, credentials ...string) bool {
	switch typed := value.(type) {
	case string:
		return ContainsExact(typed, credentials...)
	case []any:
		for _, item := range typed {
			if jsonValueContainsExact(item, credentials...) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if ContainsExact(key, credentials...) || jsonValueContainsExact(item, credentials...) {
				return true
			}
		}
	}
	return false
}

func ProtectUpstreamError(code, message string, credentials ...string) (string, string, bool) {
	if ContainsExactOrJSONEscaped(code, credentials...) || ContainsExactOrJSONEscaped(message, credentials...) {
		return CredentialErrorCode, CredentialErrorMessage, true
	}
	return code, message, false
}
