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
	e := newGroupEnv(t)
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
	server := e.liveGateway()

	send := func(model string, stream bool) *http.Response {
		t.Helper()
		body := fmt.Sprintf(`{"model":%q,"stream":%v}`, model, stream)
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
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
	stream := e.openStream(server, secret, "/v1/chat/completions", `{"model":"gemini-test","stream":true}`, nil)
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

// streamTo sends a streaming chat request and returns the recorder.
func streamTo(e *env, secret, model string) int {
	e.t.Helper()
	return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(fmt.Sprintf(`{"model":%q,"stream":true}`, model)), nil).Code
}

func setRouting(e *env, consumer person, body map[string]any) {
	e.t.Helper()
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", body)
}

// Fastest-first ranks by the first-token median of each channel's streaming
// calls, counted in memory as the calls ended: it does not matter which channel
// was created first, and the calls table is not consulted.
func TestFastestModeRanksByFirstTokenTimesCountedInMemory(t *testing.T) {
	for _, fastCreatedFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("fast channel created first=%v", fastCreatedFirst), func(t *testing.T) {
			e := newGroupEnv(t)
			consumer := e.member("consumer", "1000")
			sharer := e.member("sharer", "0")
			fast := newScriptedUpstream(t, 0, "")
			slow := newScriptedUpstream(t, 250*time.Millisecond, "")
			create := func(name string, u *scriptedUpstream) string {
				return e.createChannel(sharer, channelSpec{name: name, upstream: u.upstream, multiplier: "1"})
			}
			var fastID, slowID string
			if fastCreatedFirst {
				fastID = create("fast", fast)
				slowID = create("slow", slow)
			} else {
				slowID = create("slow", slow)
				fastID = create("fast", fast)
			}
			_, secret := e.defaultKey(consumer)

			// Reach the new minimum with real model-scoped streaming observations;
			// one legacy first-byte sample must no longer decide latency ranking.
			for _, id := range []string{slowID, fastID} {
				setRouting(e, consumer, map[string]any{"mode": "manual", "order": []string{id}})
				for range 20 {
					if code := streamTo(e, secret, "gpt-test"); code != http.StatusOK {
						t.Fatalf("seeding call = %d", code)
					}
				}
			}
			setRouting(e, consumer, map[string]any{"mode": "fastest"})
			e.engine.RefreshStats()
			stats := e.engine.ModelStats("gpt-test", channel.FormatOpenAIChat, "", true)
			if stats[fastID].FirstOutputSamples != 20 || stats[slowID].FirstOutputSamples != 20 || stats[fastID].FirstOutputP50MS == nil || stats[slowID].FirstOutputP50MS == nil || *stats[fastID].FirstOutputP50MS >= *stats[slowID].FirstOutputP50MS {
				t.Fatalf("first-token medians: fast %+v slow %+v", stats[fastID], stats[slowID])
			}

			// With the calls table emptied, the memory alone has to decide.
			if _, err := e.pool.Exec(context.Background(), `DELETE FROM calls`); err != nil {
				t.Fatal(err)
			}
			fastBefore, slowBefore := len(fast.inference()), len(slow.inference())
			for range 6 {
				if code := streamTo(e, secret, "gpt-test"); code != http.StatusOK {
					t.Fatalf("call = %d", code)
				}
			}
			if fastHits, slowHits := len(fast.inference())-fastBefore, len(slow.inference())-slowBefore; fastHits != 6 || slowHits != 0 {
				t.Fatalf("fastest mode sent %d calls to the fast channel and %d to the slow one", fastHits, slowHits)
			}
		})
	}
}

// Stable-first ranks by the success rate of the last 24 hours once a channel
// has 20 attempts. The figures follow the traffic: when the leading channel
// starts to fail, its failures are counted as they happen and the other takes
// the lead as soon as the stats are republished.
func TestReliableModeFollowsTheSuccessRatesCountedInMemory(t *testing.T) {
	book := gateway.NewStatsBook()
	e := newGroupEnvWith(t, func(d *gateway.Dependencies) { d.StatsBook = book })
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	steady := newScriptedUpstream(t, 0, overloadedBody)
	wobbly := newScriptedUpstream(t, 0, overloadedBody)
	noCooldown := map[string]any{"cooldown_failures": 100}
	steadyID := e.createChannel(sharer, channelSpec{name: "steady", upstream: steady.upstream, multiplier: "1", advanced: noCooldown})
	wobblyID := e.createChannel(sharer, channelSpec{name: "wobbly", upstream: wobbly.upstream, multiplier: "1", advanced: noCooldown})
	_, secret := e.defaultKey(consumer)
	setRouting(e, consumer, map[string]any{"mode": "reliable"})

	// Seed versioned synthetic history through the real startup aggregate;
	// old whole-channel counters cannot populate the new ranking scope.
	for _, entry := range []struct {
		id         string
		ok, failed int
	}{{steadyID, 28, 2}, {wobblyID, 20, 10}} {
		_, err := e.pool.Exec(context.Background(), `INSERT INTO calls(account_id,model_id,requested_model,format,stream,outcome,attempts,created_at,completed_at)
SELECT $1,'gpt-test','gpt-test','openai_chat',false,CASE WHEN n<=$3 THEN 'succeeded' ELSE 'upstream_failed' END,
jsonb_build_array(jsonb_build_object('metrics_version',2,'metric_protocol','openai-chat-completions','channel',jsonb_build_object('id',$2::text,'name','synthetic'),'end_reason',CASE WHEN n<=$3 THEN 'completed' ELSE 'upstream_error' END)),
now()-interval '2 minutes',now()-interval '2 minutes' FROM generate_series(1,$4::int) AS n`, consumer.id, entry.id, entry.ok, entry.ok+entry.failed)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := e.engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}

	hits := func() [2]int { return [2]int{len(steady.inference()), len(wobbly.inference())} }
	send := func() {
		t.Helper()
		if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusOK {
			t.Fatalf("call = %d", code)
		}
		e.engine.RefreshStats()
	}
	for range 4 {
		send()
	}
	if got := hits(); got != [2]int{4, 0} {
		t.Fatalf("hits (steady, wobbly) = %v, want the steadier channel to take every call", got)
	}

	// The steady channel breaks. Each call tries it first, fails there (counted
	// against it), and is answered by the other channel.
	steady.status.Store(http.StatusServiceUnavailable)
	for range 40 {
		send()
	}
	stats := e.engine.Stats()
	if stats[steadyID].Attempts <= 30 || stats[steadyID].Successes != 28+4 {
		t.Fatalf("steady stats = %+v: its failures were not counted as they happened", stats[steadyID])
	}
	// Once its success rate has fallen below the other's, the calls skip it.
	before := hits()
	for range 5 {
		send()
	}
	after := hits()
	if after[0] != before[0] || after[1] != before[1]+5 {
		t.Fatalf("hits before %v after %v: the broken channel is still tried first", before, after)
	}
	if e.callsTableHoldsAttemptsOf(steadyID) == 0 {
		t.Fatal("the calls table should hold the live attempts too")
	}
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

// A channel that answers every request for a model with a 4xx (the cheapest
// one, say, with a wrong upstream model name the upstream does not recognise
// as such) takes every call, because a 4xx is the request's own fault and is
// never retried elsewhere. Once it has 20 attempts and nothing speaks against
// the idea that the channel is at fault, it goes to the back of the line, its
// owner and the administrators get a channel event, and the next call is
// answered by another channel. It is not excluded.
func TestChannelAnsweringEverythingWith4xxGoesLastAndIsReported(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	broken := newScriptedUpstream(t, 0, overloadedBody)
	broken.status.Store(http.StatusBadRequest)
	healthy := newScriptedUpstream(t, 0, overloadedBody)
	brokenID := e.createChannel(sharer, channelSpec{name: "broken", upstream: broken.upstream, multiplier: "1"})
	healthyID := e.createChannel(sharer, channelSpec{name: "healthy", upstream: healthy.upstream, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	call := func() int {
		return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code
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

	// From now on the other channel goes first.
	for range 3 {
		if code := call(); code != http.StatusOK {
			t.Fatalf("call after the anomaly = %d", code)
		}
	}
	if got := len(healthy.inference()); got != 3 || len(broken.inference()) != 20 {
		t.Fatalf("healthy channel hit %d times (want 3), broken one %d times (want still 20)", got, len(broken.inference()))
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
	detail := e.apiJSON(sharer, http.StatusOK, http.MethodGet, "/api/group-channels/"+brokenID+"/events", nil)
	var seen bool
	for _, entry := range detail["items"].([]any) {
		event := entry.(map[string]any)
		if event["kind"] == channel.EventAnomalousClientErrors && event["reason"] == "gpt-test: 4xx 20/20 (100%)" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("channel page events = %v", detail["items"])
	}
	adminDetail := e.apiJSON(e.admin, http.StatusOK, http.MethodGet, "/api/group-channels/"+brokenID+"/events", nil)
	if fmt.Sprint(adminDetail["items"]) == "[]" {
		t.Fatalf("administrator's channel page has no events: %v", adminDetail)
	}

	// Demoted, not excluded: when the healthy channel fails, the broken one is
	// still tried, and its 400 is what the client gets.
	healthy.status.Store(http.StatusServiceUnavailable)
	brokenBefore, healthyBefore := len(broken.inference()), len(healthy.inference())
	if code := call(); code != http.StatusBadRequest {
		t.Fatalf("call with the healthy channel down = %d, want the broken channel's 400", code)
	}
	if len(healthy.inference()) != healthyBefore+1 || len(broken.inference()) != brokenBefore+1 {
		t.Fatalf("the healthy channel was tried %d times and the broken one %d times", len(healthy.inference())-healthyBefore, len(broken.inference())-brokenBefore)
	}
}

// When every channel of the model answers with a 4xx the clients are at fault,
// not one channel: the first channel may be flagged while it is the only one
// with data, and the flag is lifted once the others have shown the same.
func TestFourXXFromEveryChannelIsNotHeldAgainstOne(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	first := newScriptedUpstream(t, 0, invalidRequestBody)
	first.status.Store(http.StatusBadRequest)
	second := newScriptedUpstream(t, 0, invalidRequestBody)
	second.status.Store(http.StatusBadRequest)
	firstID := e.createChannel(sharer, channelSpec{name: "first", upstream: first.upstream, multiplier: "1"})
	secondID := e.createChannel(sharer, channelSpec{name: "second", upstream: second.upstream, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	call := func() {
		t.Helper()
		if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusBadRequest {
			t.Fatalf("call = %d", code)
		}
	}
	for range 20 {
		call()
	}
	e.engine.RefreshStats()
	// The first channel is the only one with data, so it is moved back: the next calls reach the second.
	for range 20 {
		call()
	}
	if len(second.inference()) != 20 || len(first.inference()) != 20 {
		t.Fatalf("first channel hit %d times, second %d times: the demotion did not send the calls to the second", len(first.inference()), len(second.inference()))
	}
	e.engine.RefreshStats()
	// Both answer 4xx every time: neither stands out, the order is by price again.
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
	e := newGroupEnvOn(t, traced, storepg.New(traced), nil)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	_, secret := e.defaultKey(consumer)

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
