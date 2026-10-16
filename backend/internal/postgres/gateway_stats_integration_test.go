package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// The channel statistics are counted in the gateway's memory (Issue G8.3). These
// tests hold them to what the database query they replaced reports on the same
// calls, and exercise what ranking does with them.

// legacyChannelStats is ChannelStats24h as it ran once a minute before the
// statistics moved into memory, kept here as the reference the in-memory
// figures are compared with.
const legacyChannelStats = `
SELECT (att->'channel'->>'id')::uuid::text AS channel_id,
       count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY (att->>'ttft_ms')::double precision)
            FILTER (WHERE c.stream AND att->>'end_reason' = 'completed' AND att->>'ttft_ms' IS NOT NULL), -1)::double precision AS ttft_p50_ms
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) AS att
WHERE c.created_at >= now() - interval '24 hours'
  AND att->'channel' IS NOT NULL AND jsonb_typeof(att->'channel') = 'object'
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
GROUP BY 1`

type legacyRow struct {
	attempts, successes int64
	p50                 float64 // -1 for none
}

func legacyStats(t *testing.T, pool *pgxpool.Pool) map[string]legacyRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), legacyChannelStats)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]legacyRow{}
	for rows.Next() {
		var id string
		var row legacyRow
		if err := rows.Scan(&id, &row.attempts, &row.successes, &row.p50); err != nil {
			t.Fatal(err)
		}
		result[id] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// medianTolerance is how far the histogram's median may be from the exact one.
const medianTolerance = 0.046

// assertMatchesLegacy compares the in-memory statistics with the old query's
// answer: the same channels, the same counts, and a first-token median within
// the histogram's error.
func assertMatchesLegacy(t *testing.T, got map[string]gateway.ChannelStats, want map[string]legacyRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("channels in memory %d, in the old query %d\nmemory: %+v\nquery:  %+v", len(got), len(want), got, want)
	}
	for id, row := range want {
		stats, found := got[id]
		if !found {
			t.Errorf("channel %s missing in memory", id)
			continue
		}
		if stats.Attempts != row.attempts || stats.Successes != row.successes {
			t.Errorf("channel %s: %d of %d in memory, %d of %d in the old query", id, stats.Successes, stats.Attempts, row.successes, row.attempts)
		}
		switch {
		case row.p50 < 0 && stats.TTFTP50MS != nil:
			t.Errorf("channel %s: first-token median %v in memory, none in the old query", id, *stats.TTFTP50MS)
		case row.p50 >= 0 && stats.TTFTP50MS == nil:
			t.Errorf("channel %s: no first-token median in memory, %v in the old query", id, row.p50)
		case row.p50 >= 0:
			if diff := math.Abs(*stats.TTFTP50MS-row.p50) / math.Max(row.p50, 1); diff > medianTolerance {
				t.Errorf("channel %s: first-token median %.1f in memory, %.1f in the old query (%.1f%% apart)", id, *stats.TTFTP50MS, row.p50, diff*100)
			}
		}
	}
}

// ---- the warm-up read ----

type syntheticAttempt struct {
	channel, reason string
	ttft            *int
}

type syntheticCall struct {
	model    string
	stream   bool
	created  time.Time
	attempts []syntheticAttempt
}

func ttftBinIn(edges []int32, ms int) int {
	return sort.Search(len(edges), func(i int) bool { return int(edges[i]) > ms }) - 1
}

// A deterministic history of calls over the last day and a bit more: several
// channels and models, every way an attempt can end, streaming and not, a few
// attempts without a first-token time, and calls older than the window.
func syntheticHistory(now time.Time) []syntheticCall {
	random := rand.New(rand.NewPCG(11, 13))
	channels := []string{
		"a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002",
		"a0000000-0000-4000-8000-000000000003", "a0000000-0000-4000-8000-000000000004",
	}
	models := []string{"gpt-test", "claude-test", "gemini-test"}
	reasons := []string{
		"completed", "completed", "completed", "completed", "completed", "upstream_error", "timeout_ttft", "interrupted",
		"connect_error", "client_disconnected", "client_error", "client_error", "timeout_total",
	}
	var calls []syntheticCall
	for range 2500 {
		offset := time.Duration(random.IntN(23*60+50)) * time.Minute // within 23 h 50 min
		if random.IntN(12) == 0 {
			offset = 24*time.Hour + 10*time.Minute + time.Duration(random.IntN(120))*time.Minute // beyond the window
		}
		call := syntheticCall{model: models[random.IntN(len(models))], stream: random.IntN(3) > 0, created: now.Add(-offset).Truncate(time.Second)}
		for range 1 + random.IntN(3) {
			attempt := syntheticAttempt{channel: channels[random.IntN(len(channels))], reason: reasons[random.IntN(len(reasons))]}
			if random.IntN(20) > 0 {
				ttft := 1 + int(math.Exp(random.NormFloat64()*0.8+math.Log(350)))
				attempt.ttft = &ttft
			}
			call.attempts = append(call.attempts, attempt)
		}
		calls = append(calls, call)
	}
	return calls
}

func insertHistory(t *testing.T, e *env, accountID string, calls []syntheticCall) {
	t.Helper()
	batch := &pgx.Batch{}
	for _, call := range calls {
		var attempts []map[string]any
		for _, attempt := range call.attempts {
			entry := map[string]any{"channel": map[string]any{"id": attempt.channel, "name": "synthetic"}, "end_reason": attempt.reason}
			if attempt.ttft != nil {
				entry["ttft_ms"] = *attempt.ttft
			}
			attempts = append(attempts, entry)
		}
		encoded, err := json.Marshal(attempts)
		if err != nil {
			t.Fatal(err)
		}
		batch.Queue(`INSERT INTO calls (account_id, model_id, requested_model, format, stream, outcome, attempts, created_at)
			VALUES ($1, $2, $2, 'openai_chat', $3, 'succeeded', $4::jsonb, to_timestamp($5))`, accountID, call.model, call.stream, string(encoded), call.created.Unix())
	}
	results := e.pool.SendBatch(context.Background(), batch)
	for range calls {
		if _, err := results.Exec(); err != nil {
			t.Fatal(err)
		}
	}
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
}

type historyKey struct {
	channel, model string
	bucket         int64
	bin            int
}

// expectedHistory groups the attempts that fall in the window the way the
// AttemptHistory query is meant to.
func expectedHistory(calls []syntheticCall, since time.Time) map[historyKey][3]int64 {
	edges := gateway.TTFTBinEdges()
	expected := map[historyKey][3]int64{}
	for _, call := range calls {
		if call.created.Before(since) {
			continue
		}
		for _, attempt := range call.attempts {
			if attempt.reason == "client_disconnected" {
				continue
			}
			key := historyKey{channel: attempt.channel, model: call.model, bucket: call.created.Unix() / 300, bin: -1}
			if call.stream && attempt.reason == "completed" && attempt.ttft != nil {
				key.bin = ttftBinIn(edges, *attempt.ttft)
			}
			counts := expected[key]
			if attempt.reason != "client_error" {
				counts[0]++
			}
			if attempt.reason == "completed" {
				counts[1]++
			}
			if attempt.reason == "client_error" {
				counts[2]++
			}
			expected[key] = counts
		}
	}
	return expected
}

func TestStatisticsWindowIsReadFromTheDatabaseOnceAndMatchesTheOldQuery(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	now := time.Now()
	calls := syntheticHistory(now)
	insertHistory(t, e, consumer.id, calls)

	since := time.Unix((now.Unix()/300-287)*300, 0)
	rows, err := e.store.Gateway.AttemptHistory(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	got := map[historyKey][3]int64{}
	for _, row := range rows {
		key := historyKey{channel: row.ChannelID, model: row.ModelID, bucket: row.Bucket, bin: row.TTFTBin}
		if _, duplicate := got[key]; duplicate {
			t.Fatalf("group %+v reported twice", key)
		}
		got[key] = [3]int64{row.Counted, row.Successes, row.ClientErrors}
	}
	want := expectedHistory(calls, since)
	if len(got) != len(want) {
		t.Fatalf("%d groups from the database, %d expected", len(got), len(want))
	}
	for key, counts := range want {
		if got[key] != counts {
			t.Errorf("group %+v = %v, want %v", key, got[key], counts)
		}
	}

	// A fresh engine seeded from those rows reports what the old query did.
	engine := gateway.NewEngine(gateway.Dependencies{Store: e.store.Gateway})
	if err := engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	reference := legacyStats(t, e.pool)
	if len(reference) != 4 {
		t.Fatalf("the old query found %d channels, want the 4 of the history", len(reference))
	}
	assertMatchesLegacy(t, engine.Stats(), reference)
}

// ---- counting while requests run ----

// An upstream whose answers are chosen by the test: a status, or a good chat
// answer, streaming or not, after an optional wait before the first byte.
type scriptedUpstream struct {
	*upstream
	status atomic.Int32 // 0 means answer well
}

func newScriptedUpstream(t *testing.T, wait time.Duration, failBody string) *scriptedUpstream {
	s := &scriptedUpstream{}
	s.upstream = newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if status := int(s.status.Load()); status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, failBody)
			return
		}
		time.Sleep(wait)
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, chatStreamBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatBody)
	})
	return s
}

const (
	invalidRequestBody = `{"error":{"message":"invalid request: messages must not be empty","type":"invalid_request_error","code":"invalid_request"}}`
	overloadedBody     = `{"error":{"message":"overloaded","type":"server_error"}}`
)

// Real requests are counted where each attempt ends, and the figures equal what
// the old query makes of the calls those requests left in the database.
func TestCountingWhileRequestsRunEqualsTheOldQuery(t *testing.T) {
	e := newDrainEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	flaky := newScriptedUpstream(t, 30*time.Millisecond, overloadedBody)
	solid := newScriptedUpstream(t, 80*time.Millisecond, overloadedBody)
	strict := newScriptedUpstream(t, 0, invalidRequestBody)
	strict.status.Store(http.StatusBadRequest)
	tail := &slowTail{}
	hangingUp := newUpstream(t, tail.handler(5*time.Second, chatUsageTail))
	noCooldown := map[string]any{"cooldown_failures": 100}
	flakyID := e.createChannel(sharer, channelSpec{name: "flaky", upstream: flaky.upstream, multiplier: "1", advanced: noCooldown})
	solidID := e.createChannel(sharer, channelSpec{name: "solid", upstream: solid.upstream, multiplier: "2", advanced: noCooldown})
	strictID := e.createChannel(sharer, channelSpec{name: "strict", upstream: strict.upstream, model: "claude-test", advanced: noCooldown})
	tailID := e.createChannel(sharer, channelSpec{name: "tail", upstream: hangingUp, model: "gemini-test"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	server := e.liveGateway()

	send := func(model string, stream bool) *http.Response {
		t.Helper()
		body := fmt.Sprintf(`{"model":%q,"stream":%v}`, model, stream)
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+secret)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response
	}
	for round := range 8 {
		flaky.status.Store(0)
		if round%3 == 2 {
			flaky.status.Store(http.StatusServiceUnavailable) // falls back to solid and counts against flaky
		}
		for _, stream := range []bool{false, true} {
			if response := send("gpt-test", stream); response.StatusCode != http.StatusOK {
				t.Fatalf("round %d stream %v = %d", round, stream, response.StatusCode)
			}
		}
	}
	for range 5 {
		if response := send("claude-test", false); response.StatusCode != http.StatusBadRequest {
			t.Fatalf("strict channel = %d, want its 400 passed through", response.StatusCode)
		}
	}
	// A client that hangs up while the answer is still coming says nothing about the channel.
	flaky.status.Store(0)
	stream := e.openStream(server, secret, "/v1/responses", `{"model":"gemini-test","stream":true}`, nil)
	stream.event()
	stream.hangUp()
	e.waitFinished(stream.requestID(), time.Now())

	e.engine.RefreshStats()
	reference := legacyStats(t, e.pool)
	if len(reference) != 2 {
		t.Fatalf("the old query found channels %v, want flaky and solid only (strict only ever answered 4xx)", reference)
	}
	assertMatchesLegacy(t, e.engine.Stats(), reference)
	stats := e.engine.Stats()
	if stats[flakyID].Attempts <= stats[flakyID].Successes || stats[solidID].Attempts == 0 {
		t.Fatalf("flaky %+v solid %+v: expected some failures counted against flaky", stats[flakyID], stats[solidID])
	}
	for name, id := range map[string]string{"strict (only 4xx)": strictID, "tail (client left)": tailID} {
		if _, listed := stats[id]; listed {
			t.Fatalf("channel %s has health figures: %+v", name, stats[id])
		}
	}
	if stats[flakyID].TTFTP50MS == nil || stats[solidID].TTFTP50MS == nil {
		t.Fatalf("streaming calls left no first-token median: flaky %+v solid %+v", stats[flakyID], stats[solidID])
	}
}

// ---- ranking ----

func setRouting(e *env, consumer person, body map[string]any) {
	e.t.Helper()
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", body)
}

func (e *env) callsTableHoldsAttemptsOf(channelID string) int {
	var n int
	err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls c CROSS JOIN LATERAL jsonb_array_elements(c.attempts) a WHERE a->'channel'->>'id' = $1`, channelID).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// ---- the 4xx anomaly rule, end to end ----

func eventReasons(e *env, channelID, kind string) []string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT reason FROM channel_events WHERE channel_id = $1 AND kind = $2 ORDER BY id`, channelID, kind)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var reasons []string
	for rows.Next() {
		var reason string
		_ = rows.Scan(&reason)
		reasons = append(reasons, reason)
	}
	return reasons
}

func TestChannelAnomalyIsReportedWithoutChangingManualOrder(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	broken := newScriptedUpstream(t, 0, overloadedBody)
	broken.status.Store(http.StatusBadRequest)
	healthy := newScriptedUpstream(t, 0, overloadedBody)
	brokenID := e.createChannel(sharer, channelSpec{name: "broken", upstream: broken.upstream, multiplier: "1"})
	healthyID := e.createChannel(sharer, channelSpec{name: "healthy", upstream: healthy.upstream, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	call := func() int {
		return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code
	}

	for index := range 19 {
		if code := call(); code != http.StatusBadRequest {
			t.Fatalf("call %d = %d, want the cheapest channel's 400 passed through", index, code)
		}
	}
	e.engine.RefreshStats()
	if reasons := eventReasons(e, brokenID, channel.EventAnomalousClientErrors); len(reasons) != 0 {
		t.Fatalf("an event with only 19 samples: %v", reasons)
	}
	if code := call(); code != http.StatusBadRequest {
		t.Fatalf("call 20 = %d", code)
	}
	e.engine.RefreshStats()
	reasons := eventReasons(e, brokenID, channel.EventAnomalousClientErrors)
	if len(reasons) != 1 || reasons[0] != "gpt-test: 4xx 20/20 (100%)" {
		t.Fatalf("events of the broken channel = %v", reasons)
	}
	if len(healthy.inference()) != 0 {
		t.Fatalf("the healthy channel was reached %d times before the anomaly", len(healthy.inference()))
	}

	// Anomaly observations do not override the user's explicit order.
	for range 3 {
		if code := call(); code != 400 {
			t.Fatal(code)
		}
	}
	if len(healthy.inference()) != 0 || len(broken.inference()) != 23 {
		t.Fatal("manual order changed")
	}
	for range 5 {
		e.engine.RefreshStats()
	}
	if reasons := eventReasons(e, brokenID, channel.EventAnomalousClientErrors); len(reasons) != 1 {
		t.Fatalf("events after more refreshes = %v: it must be announced once", reasons)
	}
	if reasons := eventReasons(e, healthyID, channel.EventAnomalousClientErrors); len(reasons) != 0 {
		t.Fatalf("the healthy channel was reported: %v", reasons)
	}

	// The owner sees the event on the channel page (the OpenAPI contract
	// accepts the new kind) and so does the administrator.
	detail := e.apiJSON(sharer, http.StatusOK, http.MethodGet, "/api/channels/"+brokenID, nil)
	var seen bool
	for _, entry := range detail["events"].([]any) {
		event := entry.(map[string]any)
		if event["kind"] == channel.EventAnomalousClientErrors && event["reason"] == "gpt-test: 4xx 20/20 (100%)" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("channel page events = %v", detail["events"])
	}
	adminDetail := e.apiJSON(e.admin, http.StatusOK, http.MethodGet, "/api/admin/channels/"+brokenID, nil)
	if fmt.Sprint(adminDetail["events"]) == "[]" {
		t.Fatalf("administrator's channel page has no events: %v", adminDetail)
	}

	e.apiJSON(consumer, 200, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{healthyID, brokenID}})
	healthy.status.Store(http.StatusServiceUnavailable)
	healthyBefore, brokenBefore := len(healthy.inference()), len(broken.inference())
	if code := call(); code != 400 {
		t.Fatal(code)
	}
	if len(healthy.inference()) != healthyBefore+1 || len(broken.inference()) != brokenBefore+1 {
		t.Fatal("explicit fallback order lost")
	}

}

// When every channel of the model answers with a 4xx the clients are at fault,
// not one channel: the first channel may be flagged while it is the only one
// with data, and the flag is lifted once the others have shown the same.
func TestFourXXFromEveryChannelIsNotHeldAgainstOne(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	first := newScriptedUpstream(t, 0, invalidRequestBody)
	first.status.Store(http.StatusBadRequest)
	second := newScriptedUpstream(t, 0, invalidRequestBody)
	second.status.Store(http.StatusBadRequest)
	firstID := e.createChannel(sharer, channelSpec{name: "first", upstream: first.upstream, multiplier: "1"})
	secondID := e.createChannel(sharer, channelSpec{name: "second", upstream: second.upstream, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	call := func() {
		t.Helper()
		if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusBadRequest {
			t.Fatalf("call = %d", code)
		}
	}
	for range 20 {
		call()
	}
	e.engine.RefreshStats()
	// Both channels receive samples only after an explicit order change.
	setRouting(e, consumer, map[string]any{"mode": "manual", "order": []string{secondID, firstID}})
	for range 20 {
		call()
	}
	if len(second.inference()) != 20 || len(first.inference()) != 20 {
		t.Fatalf("first channel hit %d times, second %d times: the demotion did not send the calls to the second", len(first.inference()), len(second.inference()))
	}
	e.engine.RefreshStats()
	// Restore the explicit first-channel order.
	setRouting(e, consumer, map[string]any{"mode": "manual", "order": []string{firstID, secondID}})
	firstBefore := len(first.inference())
	call()
	if len(first.inference()) != firstBefore+1 {
		t.Fatal("the first channel is still held back although the second shows the same 4xx")
	}
	if reasons := eventReasons(e, secondID, channel.EventAnomalousClientErrors); len(reasons) != 0 {
		t.Fatalf("the second channel was reported: %v", reasons)
	}
	if reasons := eventReasons(e, firstID, channel.EventAnomalousClientErrors); len(reasons) != 1 {
		t.Fatalf("the first channel's events = %v, want the one it got while it was alone", reasons)
	}
}

// ---- no periodic reads ----

// The statistics ask the database once, at startup. Neither the refresh, nor
// requests, nor the old query (gone from the code) touch it again.
func TestStatisticsAreReadFromTheDatabaseOnlyAtStartup(t *testing.T) {
	pool, _ := isolatedDatabase(t)
	counter := gatewaybench.NewQueryCounter()
	config := pool.Config()
	config.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	e := newEnvOn(t, traced, storepg.New(traced), nil)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	if err := e.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := counter.Counts()
	for range 10 {
		if recorder := chatRequest(e, secret); recorder.Code != http.StatusOK {
			t.Fatalf("request = %d", recorder.Code)
		}
	}
	// A minute of the refresh loop, many times over.
	for range 60 {
		e.engine.RefreshStats()
	}
	after, _ := counter.Counts()
	statements := gatewaybench.Since(after, before)
	for _, name := range []string{"AttemptHistory", "ChannelStats24h", "ChannelStats"} {
		if statements[name] != 0 {
			t.Errorf("%d x %s after startup", statements[name], name)
		}
	}
	if total, _ := counter.Counts(); total["AttemptHistory"] != 1 {
		t.Errorf("AttemptHistory ran %d times in all, want the startup read only", total["AttemptHistory"])
	}
	if got := e.engine.Stats(); len(got) != 1 {
		t.Fatalf("stats = %+v", got)
	}
}
