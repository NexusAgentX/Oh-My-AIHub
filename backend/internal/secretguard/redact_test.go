package secretguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

const redacted = "[redacted]"

func TestRedactReplacesEveryFormTheUpstreamMayEchoTheCredentialIn(t *testing.T) {
	const key = "sk-AbC/12+x=y"
	for _, tc := range []struct{ name, text, want string }{
		{"as is", `{"error":"bad key sk-AbC/12+x=y"}`, `{"error":"bad key [redacted]"}`},
		{"twice", `sk-AbC/12+x=y and sk-AbC/12+x=y`, `[redacted] and [redacted]`},
		{"at the edges", `sk-AbC/12+x=y`, `[redacted]`},
		{"escaped slash", `{"error":"bad key sk-AbC\/12+x=y"}`, `{"error":"bad key [redacted]"}`},
		{"unicode escapes", `{"error":"bad key sk-AbC/12+x=y"}`, `{"error":"bad key [redacted]"}`},
		{"unicode escapes in capitals", `{"error":"sk-AbC/12+x=y"}`, `{"error":"[redacted]"}`},
		{"every character escaped", `"sk-AbC\/12+x=y"`, `"[redacted]"`},
		{"query escaped", `GET /v1?key=sk-AbC%2F12%2Bx%3Dy HTTP/1.1`, `GET /v1?key=[redacted] HTTP/1.1`},
		{"query escaped inside json", `{"url":"https:\/\/u.example\/v1?key=sk-AbC%2F12%2Bx%3Dy&a=1"}`, `{"url":"https:\/\/u.example\/v1?key=[redacted]&a=1"}`},
		{"query escaped with an escaped percent", `{"url":"key=sk-AbC%2F12%2Bx%3Dy"}`, `{"url":"key=[redacted]"}`},
		{"authorization header echoed", `{"headers":{"Authorization":"Bearer sk-AbC\/12+x=y"}}`, `{"headers":{"Authorization":"Bearer [redacted]"}}`},
		{"an escaped last character", `{"error":"sk-AbC/12+x=\u0079"}`, `{"error":"[redacted]"}`},
		{"next to its own look-alike", `sk-AbC/12+x=ysk-AbC/12+x=y`, `[redacted][redacted]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := Redact([]byte(tc.text), key, redacted)
			if !found || string(got) != tc.want {
				t.Fatalf("Redact(%q) = %q, %v; want %q", tc.text, got, found, tc.want)
			}
		})
	}
}

func TestRedactLeavesTextWithoutTheCredentialAlone(t *testing.T) {
	const key = "sk-secret-0123456789"
	for _, text := range []string{
		``,
		`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`,
		`{"error":"sk-secret-012345678"}`,          // one character short
		`{"error":"sk-secret-0123456780"}`,         // one character different
		`{"error":"SK-SECRET-0123456789"}`,         // another case
		`sk-secret-`,                               // only its beginning
		`a\\b \n \" \u00e9 \ud83d\ude00 \ud83d \q`, // escapes that spell nothing of it
		`sk-secret-0123\`,                          // cut short in the middle of an escape
		`sk-secret-0123\u00`,
		strings.Repeat(`\`, 100) + `sk-secret-012345`,
	} {
		if out, found := Redact([]byte(text), key, redacted); found || string(out) != text {
			t.Fatalf("Redact(%q) = %q, %v", text, out, found)
		}
	}
}

func TestRedactReturnsTheSameSliceWhenNothingIsFound(t *testing.T) {
	text := []byte(`{"error":"nothing here"}`)
	out, found := Redact(text, "sk-secret", redacted)
	if found || &out[0] != &text[0] {
		t.Fatal("text without the credential was copied or reported changed")
	}
	if out, found := Redact(text, "", redacted); found || &out[0] != &text[0] {
		t.Fatal("an empty credential matched")
	}
}

func TestRedactDoesNotModifyItsInput(t *testing.T) {
	text := []byte(`a sk-secret b`)
	before := string(text)
	if _, found := Redact(text, "sk-secret", redacted); !found || string(text) != before {
		t.Fatalf("input changed to %q", text)
	}
}

func TestRedactStringMatchesRedact(t *testing.T) {
	got, found := RedactString("Bearer sk-secret", "sk-secret", "***")
	if !found || got != "Bearer ***" {
		t.Fatalf("got %q, %v", got, found)
	}
	if got, found := RedactString("Bearer other", "sk-secret", "***"); found || got != "Bearer other" {
		t.Fatalf("got %q, %v", got, found)
	}
}

// Whatever way a JSON encoder writes the credential into a string, once the
// result is decoded the credential must be gone, and the rest of the document
// must still be the same JSON.
func TestRedactRemovesCredentialsFromJSONWrittenByEveryKindOfEncoder(t *testing.T) {
	credentials := []string{
		"sk-abc123", "AIzaSyA-b_c", "key/with+base64==", "a<b>&c", `quote"back\slash`, "café-中文", "emoji-\U0001F600-key",
		"sk-proj/x y", "100%-sure",
	}
	encoders := map[string]func(string) string{
		"go":               func(s string) string { b, _ := json.Marshal(s); return string(b) },
		"go without html":  func(s string) string { return marshalNoHTML(s) },
		"slashes escaped":  func(s string) string { return strings.ReplaceAll(marshalNoHTML(s), "/", `\/`) },
		"ascii only":       asciiOnly,
		"everything":       everythingEscaped,
		"percent encoding": func(s string) string { b, _ := json.Marshal(url.QueryEscape(s)); return string(b) },
	}
	for _, credential := range credentials {
		for name, encode := range encoders {
			text := `{"error":{"message":"rejected ` + strings.Trim(encode(credential), `"`) + ` as a key","n":1}}`
			got, found := Redact([]byte(text), credential, redacted)
			if !found {
				t.Errorf("%s: %q not recognised in %s", name, credential, text)
				continue
			}
			var document struct {
				Error struct {
					Message string `json:"message"`
					N       int    `json:"n"`
				} `json:"error"`
			}
			if err := json.Unmarshal(got, &document); err != nil {
				t.Errorf("%s: %q: redacted text is no longer JSON: %v: %s", name, credential, err, got)
				continue
			}
			if document.Error.Message != "rejected [redacted] as a key" || document.Error.N != 1 {
				t.Errorf("%s: %q: message = %q", name, credential, document.Error.Message)
			}
		}
	}
}

func marshalNoHTML(s string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(buffer.String(), "\n")
}

// asciiOnly escapes every character above 0x7e as \uXXXX, with surrogate pairs.
func asciiOnly(s string) string {
	var out strings.Builder
	for _, r := range marshalNoHTML(s) {
		switch {
		case r < 0x7f:
			out.WriteRune(r)
		case r >= 0x10000:
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3ff))
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String()
}

// everythingEscaped writes every character of the string as a \u escape.
func everythingEscaped(s string) string {
	quoted := marshalNoHTML(s)
	var out strings.Builder
	out.WriteByte('"')
	for index := 1; index < len(quoted)-1; {
		if quoted[index] == '\\' { // an escape the plain encoder wrote: keep it
			next := 2
			if quoted[index+1] == 'u' {
				next = 6
			}
			out.WriteString(quoted[index : index+next])
			index += next
			continue
		}
		r, size := utf8.DecodeRuneInString(quoted[index:])
		if r >= 0x10000 {
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04X\u%04X`, 0xD800+(r>>10), 0xDC00+(r&0x3ff))
		} else {
			fmt.Fprintf(&out, `\u%04X`, r)
		}
		index += size
	}
	out.WriteByte('"')
	return out.String()
}

func TestMaxEncodedLenCoversTheLongestFormARedactionRecognises(t *testing.T) {
	credential := "k/ +é"
	worst := everythingEscaped(url.QueryEscape(credential))
	worst = strings.Trim(worst, `"`)
	if _, found := Redact([]byte(worst), credential, redacted); !found {
		t.Fatalf("the longest form %q was not recognised", worst)
	}
	if len(worst) > MaxEncodedLen(credential) {
		t.Fatalf("the longest form is %d bytes, MaxEncodedLen is %d", len(worst), MaxEncodedLen(credential))
	}
}

func FuzzRedactLeavesNoPlainOccurrenceBehind(f *testing.F) {
	f.Add("prefix sk-secret suffix", "sk-secret")
	f.Add(`sk-secret \/ \\`, "sk-secret")
	f.Add("aaaa", "aa")
	f.Add("ababa", "aba")
	f.Fuzz(func(t *testing.T, text, credential string) {
		if credential == "" || strings.Contains(credential, "#") || strings.ContainsAny(credential, "\\") {
			t.Skip()
		}
		out, found := Redact([]byte(text), credential, "#")
		if !found {
			if string(out) != text {
				t.Fatalf("unchanged text came back different: %q", out)
			}
			if strings.Contains(text, credential) {
				t.Fatalf("%q holds %q but nothing was found", text, credential)
			}
			return
		}
		if strings.Contains(string(out), credential) {
			t.Fatalf("Redact(%q, %q) = %q still holds the credential", text, credential, out)
		}
	})
}

func TestRedactSurvivesLargeTextFullOfEscapes(t *testing.T) {
	text := bytes.Repeat([]byte(`\n\"\\`), 1<<18)
	rng := rand.New(rand.NewSource(1))
	credential := fmt.Sprintf("sk-%d", rng.Int())
	if _, found := Redact(text, credential, redacted); found {
		t.Fatal("found a credential in text made of escapes")
	}
}
