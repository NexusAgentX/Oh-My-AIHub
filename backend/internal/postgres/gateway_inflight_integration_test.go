package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// setInflightLimit changes the per-account in-flight cap the way an
// administrator does. It must run before the first gateway request or channel
// call of the test: the engine reads platform settings from a snapshot that
// production refreshes every few seconds and tests load once.
func (e *env) setInflightLimit(limit int) {
	e.t.Helper()
	current := e.apiJSON(e.admin, http.StatusOK, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	for _, readOnly := range []string{"updated_at", "usd_cny_rate_source", "usd_cny_rate_updated_at", "fx_sync_attempted_at", "fx_sync_error"} {
		delete(current, readOnly)
	}
	current["account_max_inflight_calls"] = limit
	saved := e.apiJSON(e.admin, http.StatusOK, http.MethodPut, "/api/admin/settings", current)["settings"].(map[string]any)
	if saved["account_max_inflight_calls"] != float64(limit) {
		e.t.Fatalf("saved settings = %v", saved)
	}
}

// heldStream is an upstream whose streaming answers send their first event and
// then wait: while a call is parked there, it is in flight.
type heldStream struct {
	*upstream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldStream(t *testing.T) *heldStream {
	h := &heldStream{started: make(chan struct{}, 100), release: make(chan struct{})}
	h.upstream = newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if !strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, chatBody)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		first, rest, _ := strings.Cut(chatStreamBody, "\n\n")
		_, _ = io.WriteString(w, first+"\n\n")
		w.(http.Flusher).Flush()
		h.started <- struct{}{}
		select {
		case <-h.release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, rest)
	})
	t.Cleanup(h.finish)
	return h
}

// finish lets every parked stream complete; calling it twice is harmless.
func (h *heldStream) finish() { h.once.Do(func() { close(h.release) }) }

func (h *heldStream) waitStarted(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-h.started:
		case <-time.After(10 * time.Second):
			t.Fatal("an upstream stream never started")
		}
	}
}

// stream sends a streaming chat request in the background and returns what it
// will answer.
func (e *env) stream(secret string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","stream":true}`), nil)
	}()
	return done
}

func (e *env) countOutcome(accountID, outcome string) int {
	e.t.Helper()
	var count int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls WHERE account_id = $1 AND outcome = $2`, accountID, outcome).Scan(&count); err != nil {
		e.t.Fatal(err)
	}
	return count
}

func TestGatewayRefusesAnAccountsCallsOverTheInflightLimitWithoutCharge(t *testing.T) {
	e := newEnv(t)
	e.setInflightLimit(1)
	consumer := e.member("consumer", "100")
	bystander := e.member("bystander", "100")
	sharer := e.member("sharer", "0")
	relay := newHeldStream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay.upstream})
	_, secret := e.defaultKey(consumer)
	_, bystanderSecret := e.defaultKey(bystander)
	secondKey := e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": "second"})["secret"].(string)

	// One streaming call is in flight: it holds the account's only slot.
	first := e.stream(secret)
	relay.waitStarted(t, 1)

	refused := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if refused.Code != http.StatusTooManyRequests || body.Error.Code != "too_many_inflight_calls" || body.Error.Type != "rate_limit_error" ||
		body.Error.Message != "同时进行的调用过多，请稍后重试" || refused.Header().Get("X-AIHub-Request-Id") == "" {
		t.Fatalf("second concurrent call = %d %s", refused.Code, refused.Body.String())
	}
	row := e.callRow(refused.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "rejected_inflight" || row.CostNano != 0 || row.FeeNano != 0 || row.HasLedgerTx || row.Attempts != 0 || row.FinalChannel != "" {
		t.Fatalf("refused call = %+v", row)
	}

	// The limit is per account, not per key, and the client's own error format is used.
	other := e.call(secondKey, http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-test"}`), map[string]string{"anthropic-version": "2023-06-01"})
	var claude struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &claude); err != nil {
		t.Fatal(err)
	}
	if other.Code != http.StatusTooManyRequests || claude.Type != "error" || claude.Error.Type != "rate_limit_error" {
		t.Fatalf("another key of the same account = %d %s", other.Code, other.Body.String())
	}

	// Other accounts are not affected.
	if code := e.call(bystanderSecret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusOK {
		t.Fatalf("another account's call = %d", code)
	}

	// Nothing was charged for the refusals and the upstream never saw them.
	if got := e.balance(consumer); got != "0" {
		t.Fatalf("balance = %s while the first call is still running", got)
	}
	if got := len(relay.inference()); got != 2 { // the held stream and the bystander
		t.Fatalf("upstream requests = %d, want 2", got)
	}

	// The slot comes back once the call has finished and been booked.
	relay.finish()
	finished := <-first
	if finished.Code != http.StatusOK {
		t.Fatalf("held call = %d %s", finished.Code, finished.Body.String())
	}
	if row := e.callRow(finished.Header().Get("X-AIHub-Request-Id")); row.Outcome != "succeeded" || !row.HasLedgerTx {
		t.Fatalf("held call row = %+v", row)
	}
	if next := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil); next.Code != http.StatusOK {
		t.Fatalf("call after the first one ended = %d %s", next.Code, next.Body.String())
	}
	if got := e.balance(consumer); got != "-0.08008" { // the stream and the final call, nothing for the refusals
		t.Fatalf("balance = %s, want two charges", got)
	}
	if got := e.countOutcome(consumer.id, "rejected_inflight"); got != 2 {
		t.Fatalf("rejected_inflight calls = %d, want 2", got)
	}
	e.assertZeroSum()
}

func TestGatewayDoesNotLimitInflightCallsWhenTheLimitIsZero(t *testing.T) {
	e := newEnv(t)
	e.setInflightLimit(0)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := newHeldStream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay.upstream})
	_, secret := e.defaultKey(consumer)

	const parallel = 25 // more than the default cap of 20
	var running []<-chan *httptest.ResponseRecorder
	for range parallel {
		running = append(running, e.stream(secret))
	}
	relay.waitStarted(t, parallel)
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusOK {
		t.Fatalf("call next to %d running ones = %d", parallel, code)
	}
	relay.finish()
	for _, done := range running {
		if recorder := <-done; recorder.Code != http.StatusOK {
			t.Fatalf("held call = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	if got := e.countOutcome(consumer.id, "rejected_inflight"); got != 0 {
		t.Fatalf("rejected_inflight calls = %d, want none", got)
	}
}

func TestGatewayInflightLimitDefaultsToTwentyAndIsAuditedWhenChanged(t *testing.T) {
	e := newEnv(t)
	current := e.apiJSON(e.admin, http.StatusOK, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	if current["account_max_inflight_calls"] != float64(20) {
		t.Fatalf("default in-flight limit = %v", current["account_max_inflight_calls"])
	}
	e.setInflightLimit(7)
	var detail string
	err := e.pool.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action = 'settings.updated' ORDER BY id DESC LIMIT 1`).Scan(&detail)
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Before map[string]any `json:"before"`
		After  map[string]any `json:"after"`
	}
	if err := json.Unmarshal([]byte(detail), &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Before["account_max_inflight_calls"] != float64(20) || recorded.After["account_max_inflight_calls"] != float64(7) {
		t.Fatalf("audit detail = %s", detail)
	}
	current["account_max_inflight_calls"] = 1001
	delete(current, "updated_at")
	for _, readOnly := range []string{"usd_cny_rate_source", "usd_cny_rate_updated_at", "fx_sync_attempted_at", "fx_sync_error"} {
		delete(current, readOnly)
	}
	if recorder := e.api(e.admin, http.MethodPut, "/api/admin/settings", current); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("limit above 1000 = %d", recorder.Code)
	}
}

// A client that hangs up after the answer began does not free its slot: the
// gateway reads on (ADR-0035 rule 6) and the slot is given back only when the
// call has been booked. Both ways the reading ends are covered: the upstream
// finishes inside the drain window, and the drain window runs out.
func TestGatewayKeepsTheInflightSlotWhileItReadsOnAfterTheClientLeftUntilTheCallIsBooked(t *testing.T) {
	e := newEnvWith(t, func(d *gateway.Dependencies) { d.DrainWindow = 2 * time.Second })
	e.setInflightLimit(1)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	var streams atomic.Int32
	finishFirst := make(chan struct{})
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if !strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, chatBody)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chatFrame)
		w.(http.Flusher).Flush()
		if streams.Add(1) == 1 {
			select { // the first answer ends, usage included, once the test says so
			case <-finishFirst:
				_, _ = io.WriteString(w, chatUsageTail[len(chatFrame):])
			case <-r.Context().Done():
			}
			return
		}
		<-r.Context().Done() // the second never ends: only the drain window stops it
	})
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	_, secret := e.defaultKey(consumer)
	server := e.liveGateway()
	chat := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	}
	assertRefused := func(when string) {
		t.Helper()
		refused := chat()
		if refused.Code != http.StatusTooManyRequests || !strings.Contains(refused.Body.String(), `"too_many_inflight_calls"`) {
			t.Fatalf("call %s = %d %s", when, refused.Code, refused.Body.String())
		}
	}
	assertAdmittedSoon := func(when string) {
		t.Helper()
		// The slot is released right after the booking, so allow a moment for
		// Serve to return.
		deadline := time.Now().Add(2 * time.Second)
		for {
			recorder := chat()
			if recorder.Code == http.StatusOK {
				return
			}
			if recorder.Code != http.StatusTooManyRequests || time.Now().After(deadline) {
				t.Fatalf("call %s = %d %s", when, recorder.Code, recorder.Body.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	inProgress := func(callID string) bool {
		t.Helper()
		var outcome string
		if err := e.pool.QueryRow(context.Background(), `SELECT outcome FROM calls WHERE id = $1`, callID).Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		return outcome == "in_progress"
	}

	// 1. The upstream ends, usage included, while the gateway reads on.
	first := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	first.event()
	left := time.Now()
	first.hangUp()
	time.Sleep(300 * time.Millisecond)
	if !inProgress(first.requestID()) {
		t.Fatal("setup: the call must still be reading on")
	}
	assertRefused("while the gateway reads on after the client left")
	if !inProgress(first.requestID()) {
		t.Fatal("setup: the call was booked before the upstream finished")
	}
	close(finishFirst)
	e.waitFinished(first.requestID(), left)
	if row := e.callRow(first.requestID()); row.Outcome != "client_disconnected" || row.CostNano == 0 || !row.HasLedgerTx {
		t.Fatalf("drained call = %+v: its usage must be billed", row)
	}
	assertAdmittedSoon("after the drained call was booked")

	// 2. The drain window runs out.
	second := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gpt-test","stream":true}`, nil)
	second.event()
	left = time.Now()
	second.hangUp()
	time.Sleep(300 * time.Millisecond)
	assertRefused("before the drain window is over")
	if !inProgress(second.requestID()) {
		t.Fatal("setup: the call was booked inside the drain window")
	}
	e.waitFinished(second.requestID(), left)
	if row := e.callRow(second.requestID()); row.Outcome != "client_disconnected" || row.CostNano != 0 {
		t.Fatalf("call whose window ran out = %+v", row)
	}
	assertAdmittedSoon("after the drain window was over")
	e.assertZeroSum()
}
