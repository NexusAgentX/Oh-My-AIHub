package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// keyEchoTransport records the upstream key of each request and answers with
// a status of its choice; a non-2xx answer repeats the key in its body, the
// way some upstreams do when they reject one.
type keyEchoTransport struct {
	mutex  sync.Mutex
	status int
	keys   []string
}

func (t *keyEchoTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	key := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	t.mutex.Lock()
	t.keys = append(t.keys, key)
	status := t.status
	t.mutex.Unlock()
	body := `{"id":"ok"}`
	if status != http.StatusOK {
		body = `{"error":{"message":"Incorrect API key provided: ` + key + `"}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func (t *keyEchoTransport) seen() []string {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return append([]string(nil), t.keys...)
}

type credentialRun struct {
	t         *testing.T
	keyring   *channel.Keyring
	engine    *Engine
	transport *keyEchoTransport
	logs      *bytes.Buffer
}

func newCredentialRun(t *testing.T, status int) *credentialRun {
	t.Helper()
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	keyring, err := channel.ParseKeyring("test="+base64.StdEncoding.EncodeToString(secret), "test")
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	transport := &keyEchoTransport{status: status}
	engine := NewEngine(Dependencies{
		Keyring: keyring, Outbound: fakeOutbound{transport: transport},
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return &credentialRun{t: t, keyring: keyring, engine: engine, transport: transport, logs: logs}
}

// attempt makes one non-streaming attempt on a channel holding the given
// sealed key.
func (r *credentialRun) attempt(channelID string, sealed channel.EncryptedCredential) attemptResult {
	r.t.Helper()
	body := []byte(`{"model":"gpt-test"}`)
	_, scan, _ := ScanRequest(body)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	call := &callState{format: channel.FormatOpenAIChat}
	candidate := Candidate{ChannelID: channelID, BaseURL: "https://relay.example", UpstreamModel: "gpt-up", Credential: sealed}
	result := r.engine.attempt(context.Background(), request, call, candidate, Limits{},
		settings.Settings{DefaultRequestTimeoutMS: 600000, DefaultIdleTimeoutMS: 600000, DefaultTTFTTimeoutMS: 600000}, r.engine.Outbound, body, scan, authBearer, attemptTimeouts{request: 600 * time.Second, idle: 600 * time.Second, ttft: 600 * time.Second})
	if result.cancel != nil {
		r.t.Cleanup(func() { result.cancel(nil) })
	}
	return result
}

func (r *credentialRun) seal(channelID, plaintext string) channel.EncryptedCredential {
	r.t.Helper()
	sealed, err := r.keyring.Encrypt(channelID, 1, plaintext)
	if err != nil {
		r.t.Fatal(err)
	}
	return sealed
}

func TestAttemptsOnAChannelDecryptItsKeyOnce(t *testing.T) {
	run := newCredentialRun(t, http.StatusOK)
	sealed := run.seal("channel-a", "sk-first-key")
	for range 3 {
		if result := run.attempt("channel-a", sealed); result.response == nil {
			t.Fatalf("attempt failed: %+v", result.record)
		}
	}
	if got := run.keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("decrypted %d times for three attempts on one sealed key", got)
	}
	for index, key := range run.transport.seen() {
		if key != "sk-first-key" {
			t.Fatalf("request %d carried key %q", index, key)
		}
	}

	// The channel's key is replaced: the next attempt must use the new one,
	// never the cached old one.
	replaced := run.seal("channel-a", "sk-second-key")
	for range 2 {
		run.attempt("channel-a", replaced)
	}
	if got := run.keyring.CredentialDecryptions(); got != 2 {
		t.Fatalf("decrypted %d times after the key was replaced, want 2", got)
	}
	keys := run.transport.seen()
	if len(keys) != 5 || keys[3] != "sk-second-key" || keys[4] != "sk-second-key" {
		t.Fatalf("keys sent upstream: %q", keys)
	}

	// Another channel has its own entry.
	other := run.seal("channel-b", "sk-other-key")
	run.attempt("channel-b", other)
	run.attempt("channel-a", replaced)
	if got := run.keyring.CredentialDecryptions(); got != 3 {
		t.Fatalf("decrypted %d times after a second channel, want 3", got)
	}
}

func TestForgettingAChannelMakesTheNextAttemptDecryptAgain(t *testing.T) {
	run := newCredentialRun(t, http.StatusOK)
	sealed := run.seal("channel-a", "sk-first-key")
	run.attempt("channel-a", sealed)
	// Deleting the channel drops its entry; an attempt that still carries the
	// sealed key decrypts it again rather than finding it in memory.
	run.keyring.ForgetCredential("channel-a")
	run.attempt("channel-a", sealed)
	if got := run.keyring.CredentialDecryptions(); got != 2 {
		t.Fatalf("decrypted %d times, want 2: the entry survived the deletion", got)
	}
}

func TestACachedKeyIsStillScrubbedFromErrorsAndNeverLogged(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	run := newCredentialRun(t, http.StatusInternalServerError)
	sealed := run.seal("channel-a", key)
	for attemptNumber := range 2 {
		result := run.attempt("channel-a", sealed)
		if result.failure == nil || result.record.ErrorMessage == nil {
			t.Fatalf("attempt %d did not fail as expected: %+v", attemptNumber, result.record)
		}
		message := *result.record.ErrorMessage
		if strings.Contains(message, key) || !strings.Contains(message, "***") {
			t.Fatalf("attempt %d: recorded error %q was not scrubbed", attemptNumber, message)
		}
	}
	if got := run.keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("decrypted %d times", got)
	}

	// A key that cannot be opened reports why in the log, without the key.
	foreign, err := channel.ParseKeyring("other="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "other")
	if err != nil {
		t.Fatal(err)
	}
	unopenable, err := foreign.Encrypt("channel-b", 1, key)
	if err != nil {
		t.Fatal(err)
	}
	result := run.attempt("channel-b", unopenable)
	if result.record.ErrorCode == nil || *result.record.ErrorCode != "credential_unavailable" {
		t.Fatalf("record = %+v", result.record)
	}
	if run.logs.Len() == 0 {
		t.Fatal("test setup: the unavailable key was not logged at all")
	}
	if strings.Contains(run.logs.String(), key) {
		t.Fatalf("log carries the key: %s", run.logs.String())
	}
	if run.keyring.CredentialDecryptions() != 2 {
		t.Fatal("a failed open should have been tried once, not cached")
	}
	run.attempt("channel-b", unopenable)
	if run.keyring.CredentialDecryptions() != 3 {
		t.Fatal("a failed open was answered from memory")
	}
}
