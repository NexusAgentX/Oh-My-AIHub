package postgres_test

import (
	"net/http"
	"testing"
)

// The gateway opens a channel's upstream key once and keeps the plaintext in
// memory (ADR-0036, decision 8) until the channel gets another key or is
// deleted: what reaches the upstream always follows the database.
func TestGatewayOpensAChannelKeyOnceUntilTheKeyChangesOrTheChannelIsDeleted(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: "sk-first-0001"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	request := []byte(`{"model":"gpt-test"}`)
	callOK := func() {
		t.Helper()
		if recorder := e.call(secret, http.MethodPost, "/v1/responses", request, nil); recorder.Code != http.StatusOK {
			t.Fatalf("call = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	sent := func() []string {
		var keys []string
		for _, record := range relay.inference() {
			keys = append(keys, record.Header.Get("Authorization"))
		}
		return keys
	}

	for range 3 {
		callOK()
	}
	if got := e.keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("decrypted the channel key %d times for three calls", got)
	}

	// Another key in the database means another key upstream, at once.
	e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"api_key": "sk-second-0002"})
	callOK()
	callOK()
	keys := sent()
	if len(keys) != 5 {
		t.Fatalf("upstream requests = %q", keys)
	}
	for index, want := range []string{"Bearer sk-first-0001", "Bearer sk-first-0001", "Bearer sk-first-0001", "Bearer sk-second-0002", "Bearer sk-second-0002"} {
		if keys[index] != want {
			t.Fatalf("upstream request %d carried %q, want %q", index, keys[index], want)
		}
	}
	if got := e.keyring.CredentialDecryptions(); got != 2 {
		t.Fatalf("decrypted %d times after the key changed, want 2", got)
	}

	// A deleted channel is not routed to any more, and nothing is decrypted for it.
	if recorder := e.api(sharer, http.MethodDelete, "/api/channels/"+channelID, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d", recorder.Code)
	}
	if code := e.call(secret, http.MethodPost, "/v1/responses", request, nil).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("call to a deleted channel = %d", code)
	}
	if len(relay.inference()) != 5 || e.keyring.CredentialDecryptions() != 2 {
		t.Fatalf("the deleted channel was used: %d upstream requests, %d decryptions", len(relay.inference()), e.keyring.CredentialDecryptions())
	}
}
