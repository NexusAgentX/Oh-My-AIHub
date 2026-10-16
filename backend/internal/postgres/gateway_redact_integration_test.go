package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ADR-0041: an upstream that echoes the channel's key in an error must not
// show it to the consumer. The key has characters that JSON encoders and URLs
// treat specially.
const (
	echoKeyA = "up-secret/KEY+alpha=="
	echoKeyB = "up-secret/KEY+bravo=="
)

// attemptErrorMessage is the error text stored on one attempt of a call.
func (e *env) attemptErrorMessage(callID string, index int) string {
	e.t.Helper()
	var message *string
	err := e.pool.QueryRow(context.Background(), `SELECT attempts->$2::int->>'error_message' FROM calls WHERE id = $1`, callID, index).Scan(&message)
	if err != nil {
		e.t.Fatalf("attempt %d of %s: %v", index, callID, err)
	}
	if message == nil {
		return ""
	}
	return *message
}

// echoingUpstream rejects every request with the given status and a body that
// repeats the key the request carried, wherever it carried it. escapeSlashes
// writes the key the way a PHP encoder would.
func echoingUpstream(t *testing.T, status int, escapeSlashes bool) *upstream {
	return newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		sent := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		for _, name := range []string{"X-Api-Key", "X-Goog-Api-Key"} {
			if value := r.Header.Get(name); value != "" {
				sent = value
			}
		}
		where := "header"
		if r.URL.Query().Get("key") != "" {
			// A relay that quotes the URL it was called on.
			sent, where = r.URL.RawQuery, "query"
		}
		encoded, _ := json.Marshal(map[string]string{"message": "invalid credential " + sent, "where": where})
		if escapeSlashes {
			encoded = []byte(strings.ReplaceAll(string(encoded), "/", `\/`))
		}
		body := append(encoded, '\n')
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("X-Upstream", "kept")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func TestGatewayDoesNotShowTheConsumerTheKeyAnUpstreamEchoesInAnError(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		escapeSlashes bool
		failure       bool
	}{
		{"400 that is the request's own fault", http.StatusBadRequest, false, false},
		{"500", http.StatusInternalServerError, false, true},
		{"401", http.StatusUnauthorized, false, true},
		{"400 with escaped slashes", http.StatusBadRequest, true, false},
		{"502 with escaped slashes", http.StatusBadGateway, true, true},
	} {
		for _, request := range allFormats {
			t.Run(tc.name+"/"+request.name, func(t *testing.T) {
				e := newEnv(t)
				consumer := e.member("consumer", "100")
				sharer := e.member("sharer", "0")
				relay := echoingUpstream(t, tc.status, tc.escapeSlashes)
				e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: echoKeyA})
				_, secret := e.defaultKey(consumer)
				e.selectFixtureChannels(consumer)

				recorder := e.call(secret, http.MethodPost, request.path, []byte(request.body), request.headers)
				body := recorder.Body.String()
				if recorder.Code != tc.status {
					t.Fatalf("status = %d, body %q", recorder.Code, body)
				}
				for _, form := range []string{echoKeyA, strings.ReplaceAll(echoKeyA, "/", `\/`), url.QueryEscape(echoKeyA), "KEY+alpha"} {
					if strings.Contains(body, form) {
						t.Fatalf("the key reached the client as %q: %s", form, body)
					}
				}
				// Only the key changed: the rest of the body, the length and the
				// other headers are what the upstream sent.
				var parsed map[string]string
				if err := json.Unmarshal([]byte(body), &parsed); err != nil || parsed["message"] != "invalid credential [redacted]" || parsed["where"] != "header" {
					t.Fatalf("body = %q (%v)", body, err)
				}
				if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
					t.Fatalf("Content-Length = %q for a body of %d bytes", got, len(body))
				}
				if recorder.Header().Get("X-Upstream") != "kept" || recorder.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("headers = %v", recorder.Header())
				}

				id := recorder.Header().Get("X-AIHub-Request-Id")
				row := e.callRow(id)
				if row.Outcome != "upstream_failed" || row.HasLedgerTx || row.CostNano != 0 {
					t.Fatalf("call = %+v", row)
				}
				if !tc.failure && row.ResponseBytes != int64(len(body)) {
					t.Fatalf("response_bytes = %d for a body of %d bytes", row.ResponseBytes, len(body))
				}
				// What is logged and stored is scrubbed as before.
				if stored := e.attemptErrorMessage(id, 0); tc.failure && (strings.Contains(stored, "up-secret") || !strings.Contains(stored, "***")) {
					t.Fatalf("stored error text = %q", stored)
				}
				if strings.Contains(e.logs.String(), "up-secret") {
					t.Fatalf("log carries the key: %s", e.logs.String())
				}
			})
		}
	}
}

func TestGatewayHidesAKeyTheUpstreamQuotesFromTheRequestURL(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := echoingUpstream(t, http.StatusBadRequest, false)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: echoKeyA})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	// The client authenticates with ?key=, so the upstream key goes back into
	// the query of the upstream URL, percent-encoded.
	recorder := e.call("", http.MethodPost, "/v1/responses?key="+url.QueryEscape(secret), []byte(allFormats[0].body), nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("answer = %d %s", recorder.Code, recorder.Body.String())
	}
	if requests := relay.inference(); len(requests) != 1 || !strings.Contains(requests[0].Query, url.QueryEscape(echoKeyA)) {
		t.Fatalf("the upstream was not called with the key in the query: %+v", relay.requests())
	}
	var parsed map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body = %q: %v", recorder.Body.String(), err)
	}
	if parsed["message"] != "invalid credential key=[redacted]" || parsed["where"] != "query" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestGatewayHidesEachChannelsKeyWhenEveryCandidateFails(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	first := echoingUpstream(t, http.StatusInternalServerError, false)
	last := echoingUpstream(t, http.StatusBadGateway, true)
	e.createChannel(sharer, channelSpec{name: "first", upstream: first, key: echoKeyA, multiplier: "1"})
	e.createChannel(sharer, channelSpec{name: "last", upstream: last, key: echoKeyB, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	body := recorder.Body.String()
	// The answer is the last candidate's, and neither key is in it.
	if recorder.Code != http.StatusBadGateway || len(first.inference()) != 1 || len(last.inference()) != 1 {
		t.Fatalf("answer = %d %s (attempts %d, %d)", recorder.Code, body, len(first.inference()), len(last.inference()))
	}
	for _, leaked := range []string{"KEY+alpha", "KEY+bravo", `KEY+bravo`, "up-secret"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the body carries %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, "invalid credential [redacted]") || recorder.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("body = %q, Content-Length %q", body, recorder.Header().Get("Content-Length"))
	}
	id := recorder.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(id); row.Outcome != "upstream_failed" || row.Attempts != 2 || row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	for index := range 2 {
		if stored := e.attemptErrorMessage(id, index); strings.Contains(stored, "up-secret") || !strings.Contains(stored, "***") {
			t.Fatalf("attempt %d stored error text = %q", index, stored)
		}
	}
	if strings.Contains(e.logs.String(), "up-secret") {
		t.Fatalf("log carries a key: %s", e.logs.String())
	}
	e.assertZeroSum()
}

func TestGatewayLeavesAnErrorWithoutTheKeyUntouchedEvenWhenItLooksLikeOne(t *testing.T) {
	const answer = "{\n  \"error\": {\"message\": \"Incorrect API key provided: up-secret/KEY+*****. Find yours at https:\\/\\/example.test\\/keys\"}\n}\n"
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(answer)))
		w.Header().Set("Content-Md5", "kept-digest")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, answer)
	})
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: echoKeyA})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != answer {
		t.Fatalf("answer = %d %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Length") != strconv.Itoa(len(answer)) || recorder.Header().Get("Content-Md5") != "kept-digest" {
		t.Fatalf("headers = %v", recorder.Header())
	}
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.ResponseBytes != int64(len(answer)) {
		t.Fatalf("call = %+v", row)
	}
}
