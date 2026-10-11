package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// upstreamReply is what a stand-in upstream answers: the status, headers and
// body, built from the key it was called with.
type upstreamReply struct {
	status int
	header http.Header
	body   []byte
}

type replyTransport struct {
	reply func(key string) upstreamReply
}

func (t replyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	reply := t.reply(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	header := reply.header.Clone()
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: reply.status, Header: header, ContentLength: int64(len(reply.body)),
		Body: io.NopCloser(bytes.NewReader(reply.body)), Request: request}, nil
}

const echoedKey = "sk-live-AbC/0123+xyz=="

// answerOf makes one attempt against an upstream that answers with reply and
// returns what the client would be sent, with the attempt itself.
func answerOf(t *testing.T, reply func(key string) upstreamReply) (*httptest.ResponseRecorder, attemptResult) {
	t.Helper()
	run := newCredentialRun(t, http.StatusOK)
	run.engine.Outbound = fakeOutbound{transport: replyTransport{reply: reply}}
	result := run.attempt("channel-a", run.seal("channel-a", echoedKey))
	answer := result.failure
	if answer == nil {
		answer = result.passthrough
	}
	if answer == nil {
		t.Fatalf("the attempt produced no upstream error answer: %+v", result.record)
	}
	recorder := httptest.NewRecorder()
	answer.write(recorder, "req-1")
	return recorder, result
}

func jsonHeader(extra ...string) http.Header {
	header := http.Header{"Content-Type": {"application/json"}}
	for index := 0; index+1 < len(extra); index += 2 {
		header.Set(extra[index], extra[index+1])
	}
	return header
}

func TestAnUpstreamErrorThatEchoesTheKeyIsHandedToTheClientWithoutIt(t *testing.T) {
	escaped := strings.ReplaceAll(echoedKey, "/", `\/`)
	queryEscaped := url.QueryEscape(echoedKey)
	for _, tc := range []struct {
		name   string
		status int
		body   func(key string) string
		want   string
		// failure says whether the answer is a failure to fall back from (as
		// opposed to a request error handed back as it is).
		failure bool
	}{
		{"400 request error", http.StatusBadRequest,
			func(key string) string { return `{"error":{"message":"Incorrect API key provided: ` + key + `."}}` },
			`{"error":{"message":"Incorrect API key provided: [redacted]."}}`, false},
		{"500", http.StatusInternalServerError,
			func(key string) string { return "upstream auth failed for " + key + "\n" },
			"upstream auth failed for [redacted]\n", true},
		{"429", http.StatusTooManyRequests,
			func(key string) string { return `{"error":"` + key + ` is rate limited"}` },
			`{"error":"[redacted] is rate limited"}`, true},
		{"404 model unavailable", http.StatusNotFound,
			func(key string) string {
				return `{"error":{"code":"model_not_found","message":"no model for ` + key + `"}}`
			},
			`{"error":{"code":"model_not_found","message":"no model for [redacted]"}}`, true},
		{"400 that says the model is missing", http.StatusBadRequest,
			func(key string) string { return `{"error":{"code":"model_not_found","message":"` + key + `"}}` },
			`{"error":{"code":"model_not_found","message":"[redacted]"}}`, true},
		{"key written with escaped slashes", http.StatusBadRequest,
			func(string) string { return `{"error":"bad key ` + escaped + `"}` },
			`{"error":"bad key [redacted]"}`, false},
		{"key in a URL", http.StatusBadGateway,
			func(string) string { return `GET /v1/models?key=` + queryEscaped + ` failed` },
			`GET /v1/models?key=[redacted] failed`, true},
		{"key more than once", http.StatusBadRequest,
			func(key string) string { return key + "|" + key },
			`[redacted]|[redacted]`, false},
		{"key and nothing else", http.StatusUnauthorized,
			func(key string) string { return key },
			`[redacted]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder, result := answerOf(t, func(key string) upstreamReply {
				body := []byte(tc.body(key))
				return upstreamReply{status: tc.status, body: body, header: jsonHeader(
					"Content-Length", strconv.Itoa(len(body)), "X-Upstream-Request", "abc", "Retry-After", "7")}
			})
			if recorder.Code != tc.status || recorder.Body.String() != tc.want {
				t.Fatalf("answer = %d %q, want %d %q", recorder.Code, recorder.Body.String(), tc.status, tc.want)
			}
			if strings.Contains(recorder.Body.String(), "sk-live") || strings.Contains(recorder.Body.String(), "0123") {
				t.Fatalf("a part of the key reached the client: %q", recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(tc.want)) {
				t.Fatalf("Content-Length = %q, want %d", got, len(tc.want))
			}
			if recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("X-Upstream-Request") != "abc" ||
				recorder.Header().Get("Retry-After") != "7" || recorder.Header().Get("X-AIHub-Request-Id") != "req-1" {
				t.Fatalf("headers = %v", recorder.Header())
			}
			if (result.failure != nil) != tc.failure || (result.passthrough != nil) == tc.failure {
				t.Fatalf("failure = %v, passthrough = %v, want failure %v", result.failure != nil, result.passthrough != nil, tc.failure)
			}
			// The text recorded on the attempt is scrubbed as before.
			if tc.failure {
				message := *result.record.ErrorMessage
				if strings.Contains(message, "sk-live") || strings.Contains(message, echoedKey) || !strings.Contains(message, "***") {
					t.Fatalf("recorded error %q", message)
				}
			}
		})
	}
}

func TestAnUpstreamErrorWithoutTheKeyIsHandedBackByteForByte(t *testing.T) {
	body := []byte("{\n  \"error\": {\"message\": \"sk-live-AbC is not \\\"this\\\" key\", \"code\": \"x\\u0041\"}\n}\n\xff")
	digest := "md5-of-the-upstream-bytes"
	recorder, result := answerOf(t, func(string) upstreamReply {
		return upstreamReply{status: http.StatusBadRequest, body: body, header: jsonHeader(
			"Content-Length", strconv.Itoa(len(body)), "Content-Md5", digest, "Content-Encoding", "identity",
			"WWW-Authenticate", `Bearer realm="relay", error="invalid_request"`)}
	})
	if result.passthrough == nil || recorder.Code != http.StatusBadRequest || !bytes.Equal(recorder.Body.Bytes(), body) {
		t.Fatalf("answer = %d %q", recorder.Code, recorder.Body.Bytes())
	}
	want := http.Header{
		"Content-Type": {"application/json"}, "Content-Length": {strconv.Itoa(len(body))}, "Content-Md5": {digest},
		"Content-Encoding": {"identity"}, "Www-Authenticate": {`Bearer realm="relay", error="invalid_request"`}, "X-Aihub-Request-Id": {"req-1"},
	}
	if got := recorder.Header(); !headersEqual(got, want) {
		t.Fatalf("headers = %v, want %v", got, want)
	}
}

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for name, values := range a {
		if strings.Join(values, "\x00") != strings.Join(b[name], "\x00") {
			return false
		}
	}
	return true
}

func TestAnEchoedKeyIsRemovedFromHeadersAndDigestsOfChangedBodiesAreDropped(t *testing.T) {
	recorder, _ := answerOf(t, func(key string) upstreamReply {
		body := []byte(`{"error":"` + key + `"}`)
		return upstreamReply{status: http.StatusUnauthorized, body: body, header: jsonHeader(
			"Content-Length", strconv.Itoa(len(body)), "Content-Md5", "stale", "Digest", "sha-256=stale", "Content-Digest", "sha-256=:stale:", "Repr-Digest", "sha-256=:stale:",
			"WWW-Authenticate", `Bearer error="invalid_token", error_description="`+key+` is unknown"`,
			"Location", "https://relay.example/auth?key="+url.QueryEscape(key))}
	})
	header := recorder.Header()
	if got := header.Get("WWW-Authenticate"); got != `Bearer error="invalid_token", error_description="[redacted] is unknown"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	if got := header.Get("Location"); got != "https://relay.example/auth?key=[redacted]" {
		t.Fatalf("Location = %q", got)
	}
	for _, name := range []string{"Content-Md5", "Digest", "Content-Digest", "Repr-Digest"} {
		if _, kept := header[name]; kept {
			t.Fatalf("%s still vouches for bytes that were changed: %v", name, header)
		}
	}
	for name, values := range header {
		if strings.Contains(strings.Join(values, ","), "sk-live") {
			t.Fatalf("header %s carries the key: %v", name, values)
		}
	}
}

func TestAnUpstreamErrorWithoutALengthHeaderGetsNone(t *testing.T) {
	recorder, _ := answerOf(t, func(key string) upstreamReply {
		return upstreamReply{status: http.StatusInternalServerError, body: []byte("bad " + key), header: jsonHeader()}
	})
	if recorder.Body.String() != "bad [redacted]" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if _, set := recorder.Header()["Content-Length"]; set {
		t.Fatalf("a Content-Length appeared: %v", recorder.Header())
	}
}

// A key that the 1 MiB limit cuts in two must not leave its beginning behind.
func TestAKeyAtTheEdgeOfTheErrorBodyLimitIsRemovedWhole(t *testing.T) {
	for _, offset := range []int{0, 1, 5, len(echoedKey) / 2, len(echoedKey) - 1, len(echoedKey), len(echoedKey) + 3} {
		filler := bytes.Repeat([]byte("x"), maxFailureBody-offset)
		recorder, _ := answerOf(t, func(key string) upstreamReply {
			body := append(append(append([]byte(nil), filler...), key...), []byte(" and the rest of a long answer")...)
			return upstreamReply{status: http.StatusInternalServerError, body: body, header: jsonHeader("Content-Length", strconv.Itoa(len(body)))}
		})
		body := recorder.Body.Bytes()
		if len(body) > maxFailureBody {
			t.Fatalf("offset %d: %d bytes, over the limit", offset, len(body))
		}
		for length := 4; length <= len(echoedKey); length++ {
			if bytes.HasSuffix(body, []byte(echoedKey[:length])) || bytes.Contains(body, []byte(echoedKey[:length])) {
				t.Fatalf("offset %d: the first %d characters of the key reached the client", offset, length)
			}
		}
		if !bytes.HasPrefix(body, filler[:maxFailureBody-offset]) {
			t.Fatalf("offset %d: the filler was changed", offset)
		}
		if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
			t.Fatalf("offset %d: Content-Length %q for %d bytes", offset, got, len(body))
		}
	}
}

func TestAnErrorBodyOverTheLimitIsCutUnchangedAndKeepsAnHonestLength(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789"), maxFailureBody/10+1000)
	recorder, _ := answerOf(t, func(string) upstreamReply {
		return upstreamReply{status: http.StatusInternalServerError, body: big, header: jsonHeader("Content-Length", strconv.Itoa(len(big)))}
	})
	if !bytes.Equal(recorder.Body.Bytes(), big[:maxFailureBody]) {
		t.Fatalf("body = %d bytes, not the first %d of the upstream's", recorder.Body.Len(), maxFailureBody)
	}
	if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(maxFailureBody) {
		t.Fatalf("Content-Length = %q", got)
	}
}

// A body that the transport did not decode cannot be searched for the key, so
// it is not passed on (a compliant upstream never sends one: the gateway
// only offers gzip, which the transport decodes).
func TestAnEncodedErrorBodyTheTransportDidNotDecodeIsNotPassedOn(t *testing.T) {
	recorder, result := answerOf(t, func(key string) upstreamReply {
		body := append([]byte{0x1b, 0x2a, 0x00}, key...) // stands in for a brotli stream holding the key
		return upstreamReply{status: http.StatusInternalServerError, body: body, header: jsonHeader(
			"Content-Encoding", "br", "Content-Length", strconv.Itoa(len(body)), "X-Upstream-Request", "abc")}
	})
	if recorder.Code != http.StatusInternalServerError || recorder.Body.Len() != 0 {
		t.Fatalf("answer = %d %q", recorder.Code, recorder.Body.Bytes())
	}
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Header().Get("Content-Length") != "0" || recorder.Header().Get("X-Upstream-Request") != "abc" {
		t.Fatalf("headers = %v", recorder.Header())
	}
	if result.failure == nil {
		t.Fatal("the status still counts as an upstream failure")
	}
}

func TestScrubRemovesTheKeyFromStoredTextInEveryForm(t *testing.T) {
	const key = "sk-live-AbC/0123+xyz=="
	for _, tc := range []struct{ name, text, want string }{
		{"as is", "bad " + key + "!", "bad ***!"},
		{"query escaped", "GET ?key=" + url.QueryEscape(key), "GET ?key=***"},
		{"json escaped", `{"m":"` + strings.ReplaceAll(key, "/", `\/`) + `"}`, `{"m":"***"}`},
		{"unicode escaped", `{"m":"sk-live-AbC/0123+xyz=="}`, `{"m":"***"}`},
		{"invalid utf-8 dropped", "a\xffb " + key, "ab ***"},
		{"nothing to remove", "plain", "plain"},
	} {
		if got := scrub(tc.text, key); got != tc.want {
			t.Errorf("%s: scrub(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
	if got := scrub("text", ""); got != "text" {
		t.Errorf("an empty key changed the text: %q", got)
	}
}
