package secretguard

import (
	"net/url"
	"unicode/utf16"
	"unicode/utf8"
)

// maxExpansion is the most bytes one byte of a credential can take up once an
// upstream has written it out: percent-encoded (three characters) with each of
// those characters then written as a JSON \uXXXX escape (six bytes).
const maxExpansion = 18

// MaxEncodedLen is the most bytes one occurrence of the credential can take up
// in text that Redact recognises it in. A caller that cuts text to a length
// limit reads this much beyond the limit first, redacts, and cuts afterwards,
// so that an occurrence straddling the limit is replaced instead of leaving
// its beginning behind.
func MaxEncodedLen(credential string) int { return len(credential) * maxExpansion }

// Redact replaces every occurrence of the exact credential in text with
// placeholder and reports whether there was any. An occurrence is the
// credential as is, percent-encoded as a URL query or path would carry it, or
// written inside a JSON string with any of the escapes JSON allows (\/, \uXXXX
// and so on), however an upstream's encoder happens to write it. Text without
// an occurrence is returned as it is, the same slice, and anything that is not
// an occurrence is never touched: no pattern-based guessing.
//
// A credential that an upstream masks or shortens ("sk-...abcd") is not the
// credential and is not recognised.
func Redact(text []byte, credential, placeholder string) ([]byte, bool) {
	if credential == "" || len(text) == 0 {
		return text, false
	}
	forms := []string{credential}
	for _, encoded := range []string{url.QueryEscape(credential), url.PathEscape(credential)} {
		if !contains(forms, encoded) {
			forms = append(forms, encoded)
		}
	}
	// An occurrence starts with the first byte of a form or, if that byte is
	// written as an escape, with the backslash.
	var starts [256]bool
	starts['\\'] = true
	for _, form := range forms {
		starts[form[0]] = true
	}
	var out []byte
	copied := 0
	for index := 0; index < len(text); {
		if !starts[text[index]] {
			index++
			continue
		}
		longest := 0
		for _, form := range forms {
			longest = max(longest, matchForm(text, index, form))
		}
		if longest == 0 {
			index++
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(text))
		}
		out = append(out, text[copied:index]...)
		out = append(out, placeholder...)
		index += longest
		copied = index
	}
	if out == nil {
		return text, false
	}
	return append(out, text[copied:]...), true
}

// RedactString is Redact for a string.
func RedactString(text, credential, placeholder string) (string, bool) {
	out, found := Redact([]byte(text), credential, placeholder)
	if !found {
		return text, false
	}
	return string(out), true
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// matchForm returns how many bytes of text, from start, spell form, or 0. Each
// character of form may be written as itself or as a JSON string escape.
func matchForm(text []byte, start int, form string) int {
	index := start
	for position := 0; position < len(form); {
		if index >= len(text) {
			return 0
		}
		if text[index] == '\\' {
			if escaped, size := jsonEscape(text[index:]); size > 0 {
				want, width := utf8.DecodeRuneInString(form[position:])
				if escaped == want && !(want == utf8.RuneError && width == 1) {
					index += size
					position += width
					continue
				}
			}
		}
		if text[index] != form[position] {
			return 0
		}
		index++
		position++
	}
	return index - start
}

// jsonEscape decodes the JSON string escape at the start of b, returning the
// character and the length of the escape, or 0 if b does not start with one.
func jsonEscape(b []byte) (rune, int) {
	if len(b) < 2 || b[0] != '\\' {
		return 0, 0
	}
	switch b[1] {
	case '"', '\\', '/':
		return rune(b[1]), 2
	case 'b':
		return '\b', 2
	case 'f':
		return '\f', 2
	case 'n':
		return '\n', 2
	case 'r':
		return '\r', 2
	case 't':
		return '\t', 2
	case 'u':
		first, ok := hex4(b[2:])
		if !ok {
			return 0, 0
		}
		if !utf16.IsSurrogate(first) {
			return first, 6
		}
		// A high surrogate must be followed by a low one to make a character.
		if first < 0xDC00 && len(b) >= 12 && b[6] == '\\' && b[7] == 'u' {
			if second, ok := hex4(b[8:]); ok {
				if joined := utf16.DecodeRune(first, second); joined != utf8.RuneError {
					return joined, 12
				}
			}
		}
	}
	return 0, 0
}

func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var value rune
	for _, c := range b[:4] {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, false
		}
		value = value<<4 | rune(c)
	}
	return value, true
}
