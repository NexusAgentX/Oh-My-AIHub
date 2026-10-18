package postgres_test

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/gatewaypg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGatewayBackgroundSurvivesRestartAndCredentialEdit(t *testing.T) {
	e := newGroupEnv(t)
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Header.Get("Authorization") != "Bearer frozen-secret" {
			t.Errorf("unexpected credential on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses" {
			io.WriteString(w, `{"id":"resp_durable","background":true,"status":"queued"}`)
		} else if strings.HasSuffix(r.URL.Path, "/cancel") {
			io.WriteString(w, `{"id":"resp_durable","status":"cancelled"}`)
		} else {
			io.WriteString(w, `{"id":"resp_durable","status":"completed","usage":{"input_tokens":100,"output_tokens":20}}`)
		}
	})
	channelID := e.createChannel(sharer, channelSpec{name: "background", upstream: upstream, key: "frozen-secret"})
	e.declareGroupedCapabilities(channelID,
		"upstream_protocol/openai-responses.background/background_create",
		"upstream_protocol/openai-responses.background/owned_cancel",
		"upstream_protocol/openai-responses.state/owned_response_retrieve_input_items_delete")
	_, secret := e.defaultKey(consumer)
	ack := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","background":true,"input":"private synthetic input"}`), nil)
	if ack.Code != 200 {
		t.Fatalf("ack=%d %s", ack.Code, ack.Body.String())
	}
	id := ack.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(id); row.Outcome != "in_progress" || row.HasLedgerTx {
		t.Fatal(row)
	}
	var metadata []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT metadata FROM gateway_background_jobs WHERE call_id=$1`, id).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(metadata) || strings.Contains(string(metadata), "frozen-secret") || strings.Contains(string(metadata), "private synthetic input") {
		t.Fatal("sensitive plaintext persisted")
	}
	e.replaceGroupedChannelKey(sharer, channelID, "new-secret")
	e.restartGateway()
	if err := e.engine.RestoreBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.call(secret, http.MethodGet, "/v1/responses/resp_durable", nil, nil)
	if got.Code != 200 || got.Header().Get("X-AIHub-Request-Id") != id {
		t.Fatalf("retrieve=%d %s", got.Code, got.Body.String())
	}
	if cancel := e.call(secret, http.MethodPost, "/v1/responses/resp_durable/cancel", nil, nil); cancel.Code != 200 {
		t.Fatal(cancel.Code)
	}
	// Retrieval and cancellation are protocol operations, never new ledger calls.
	if row := e.callRow(id); row.Outcome != "in_progress" {
		t.Fatal(row)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.engine.RunBackground(ctx) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		row := e.callRow(id)
		if row.Outcome != "in_progress" {
			if !row.HasLedgerTx || row.CostNano != mustAmount(t, "0.0028").Nano() {
				t.Fatal(row)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var calls, jobs int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM calls),(SELECT count(*) FROM gateway_background_jobs WHERE settled_at IS NOT NULL)`).Scan(&calls, &jobs); err != nil || calls != 1 || jobs != 1 {
		t.Fatalf("calls=%d settled=%d err=%v", calls, jobs, err)
	}
	e.assertZeroSum()
}

type concurrentBackgroundFinish struct {
	gateway.Store
	entered chan struct{}
	release <-chan struct{}
}

func (s concurrentBackgroundFinish) FinishCall(ctx context.Context, finish gateway.CallFinish) (gateway.FinishResult, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.Store.FinishCall(ctx, finish)
}

func TestBackgroundStreamAndPollSettleConcurrentlyInOneLedgerTransaction(t *testing.T) {
	e := newGroupEnv(t)
	consumer, sharer := e.member("parallel-consumer", "100"), e.member("parallel-sharer", "0")
	releaseBodies, releaseSettlement := make(chan struct{}), make(chan struct{})
	var bodyOnce, settlementOnce sync.Once
	// Unblock handlers before their httptest server cleanups on every failure.
	defer bodyOnce.Do(func() { close(releaseBodies) })
	defer settlementOnce.Do(func() { close(releaseSettlement) })
	pollEntered := make(chan struct{}, 1)
	final := `{"id":"resp_parallel","object":"response","status":"completed","usage":{"input_tokens":100,"output_tokens":20},"output":[{"type":"message","content":[{"type":"output_text","text":"private-parallel-answer"}]}]}`
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: "+`{"type":"response.created","response":{"id":"resp_parallel","status":"in_progress","background":true}}`+"\n\n")
			w.(http.Flusher).Flush()
			<-releaseBodies
			io.WriteString(w, "data: "+`{"type":"response.completed","response":`+final+"}\n\n")
		} else {
			pollEntered <- struct{}{}
			<-releaseBodies
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, final)
		}
	})
	channelID := e.createChannel(sharer, channelSpec{name: "parallel-background", upstream: upstream})
	e.declareGroupedCapabilities(channelID,
		"upstream_protocol/openai-responses.background/background_create",
		"upstream_protocol/openai-responses.state/owned_response_retrieve_input_items_delete")
	_, secret := e.defaultKey(consumer)
	finishEntered := make(chan struct{}, 2)
	e.engine.Store = concurrentBackgroundFinish{Store: e.engine.Store, entered: finishEntered, release: releaseSettlement}
	proxy := httptest.NewServer(e.handler)
	defer func() {
		bodyOnce.Do(func() { close(releaseBodies) })
		settlementOnce.Do(func() { close(releaseSettlement) })
		proxy.Close()
	}()
	request, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-test","background":true,"stream":true,"input":"private-parallel-prompt"}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	client := proxy.Client()
	client.Timeout = 10 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), "response.created") {
		t.Fatal("initial stream event not delivered")
	}
	id := response.Header.Get("X-AIHub-Request-Id")
	// The database and application clocks can differ slightly; make the saved
	// job explicitly due instead of racing the poller's five-second tick.
	if _, err := e.pool.Exec(context.Background(), `UPDATE gateway_background_jobs SET next_poll_at=now()-interval '1 minute' WHERE call_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.engine.RunBackground(ctx) }()
	defer func() {
		bodyOnce.Do(func() { close(releaseBodies) })
		settlementOnce.Do(func() { close(releaseSettlement) })
		stop()
		<-done
	}()
	select {
	case <-pollEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not overlap the original stream")
	}
	bodyOnce.Do(func() { close(releaseBodies) })
	for range 2 {
		select {
		case <-finishEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("both original stream and poll must attempt settlement")
		}
	}
	settlementOnce.Do(func() { close(releaseSettlement) })
	for scanner.Scan() {
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	stop()
	<-done
	// The cleanup can consume the already-closed channel again.
	row := e.callRow(id)
	var count int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_transactions WHERE idempotency_key=$1`, "call:"+id).Scan(&count); err != nil || count != 1 || !row.HasLedgerTx || row.CostNano != mustAmount(t, "0.0028").Nano() {
		t.Fatalf("ledger count=%d row=%+v err=%v", count, row, err)
	}
	e.assertZeroSum()
	var saved string
	if err := e.pool.QueryRow(context.Background(), `SELECT j.metadata::text || coalesce(c.price_snapshot::text,'') || coalesce(c.diagnostics::text,'') || c.attempts::text FROM gateway_background_jobs j JOIN calls c ON c.id=j.call_id WHERE c.id=$1`, id).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-parallel-prompt", "private-parallel-answer"} {
		if strings.Contains(saved, private) {
			t.Fatal("stream or polling persisted request/response content")
		}
	}
}

type backgroundCredentialQueries struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (q *backgroundCredentialQueries) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.sql == "" && strings.Contains(data.SQL, "gateway_background_jobs") {
		q.sql, q.args = data.SQL, append([]any(nil), data.Args...)
	}
	return ctx
}

func (*backgroundCredentialQueries) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestBackgroundHistoricalCredentialsAreVerifiedAcrossPages(t *testing.T) {
	e := newGroupEnv(t)
	consumer, sharer := e.member("history-consumer", "100"), e.member("history-sharer", "0")
	upstream := newUpstream(t, func(http.ResponseWriter, *http.Request, []byte) {})
	channelID := e.createChannel(sharer, channelSpec{name: "historical", upstream: upstream})
	keyID, _ := e.defaultKey(consumer)
	old := upstreamKeyringOf(t, "v1", "v1")
	rotated := upstreamKeyringOf(t, "v2", "v1", "v2")
	withoutOld := upstreamKeyringOf(t, "v2", "v2")
	credential, err := old.Encrypt(channelID, 1, "private-historical-key")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"Candidate": gateway.Candidate{ChannelID: channelID, Credential: credential}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = e.pool.Exec(ctx, `WITH inserted AS (
 INSERT INTO calls(account_id,format,outcome) SELECT $1,'openai_responses','succeeded' FROM generate_series(1,1025) RETURNING id
 ) INSERT INTO gateway_background_jobs(call_id,account_id,key_id,channel_id,response_id,metadata,deadline_at,settled_at)
 SELECT id,$1,$2,$3,'resp_'||id::text,$4,now(),now() FROM inserted`, consumer.id, keyID, channelID, metadata)
	if err != nil {
		t.Fatal(err)
	}
	// Bulk inserts into a fresh isolated schema have no planner statistics yet.
	// Check the steady-state access path after the same analysis autovacuum
	// performs on an established production table; never force an index plan.
	if _, err := e.pool.Exec(ctx, `ANALYZE gateway_background_jobs`); err != nil {
		t.Fatal(err)
	}
	queries := &backgroundCredentialQueries{}
	config := e.pool.Config()
	config.ConnConfig.Tracer = queries
	tracedPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool.Close()
	verifier := gatewaypg.NewStore(tracedPool, nil)
	started := time.Now()
	if err := verifier.VerifyBackgroundCredentials(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified 1025 historical ciphertexts across nine indexed pages in %s", time.Since(started))
	if jobs, err := e.store.Gateway.PendingBackground(ctx); err != nil || len(jobs) != 0 {
		t.Fatal("completed history unexpectedly restored as running tasks")
	}
	if err := verifier.VerifyBackgroundCredentials(ctx, withoutOld); err == nil || strings.Contains(err.Error(), "private-historical-key") {
		t.Fatalf("missing historical key must prevent startup without leaking plaintext: %v", err)
	}
	var last string
	if err := e.pool.QueryRow(ctx, `SELECT call_id::text FROM gateway_background_jobs ORDER BY call_id DESC LIMIT 1`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	credential, err = withoutOld.Encrypt(channelID, 2, "private-last-page-key")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ = json.Marshal(map[string]any{"Candidate": gateway.Candidate{ChannelID: channelID, Credential: credential}})
	if _, err := e.pool.Exec(ctx, `UPDATE gateway_background_jobs SET metadata=$2 WHERE call_id=$1`, last, metadata); err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyBackgroundCredentials(ctx, old); err == nil || !strings.Contains(err.Error(), last) || strings.Contains(err.Error(), "private-last-page-key") {
		t.Fatalf("credential failure on last page was missed or leaked plaintext: %v", err)
	}
	var plan string
	queries.mu.Lock()
	sql, args := queries.sql, queries.args
	queries.mu.Unlock()
	if sql == "" {
		t.Fatal("no credential verification query was captured")
	}
	if err := e.pool.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Index Scan") || strings.Contains(plan, `"Node Type": "Sort"`) {
		t.Fatalf("history verification must page through the primary key without re-sorting: %s", plan)
	}
}

func TestServiceTierPricesFreezeAtCallStartAndRegionRemainsObservation(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer, sharer := e.member("region-consumer", "100"), e.member("region-sharer", "0")
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"usage":{"input_tokens":1000,"output_tokens":500,"service_tier":"priority","inference_geo":"us","server_tool_use":{"web_search_requests":2}}}`)
	})
	channelID := e.createChannel(sharer, channelSpec{name: "regional", upstream: upstream, multiplier: "1"})
	saved := e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) {
		q.Tiers = []market.Tier{{Name: "priority us", Conditions: market.Conditions{ServiceTier: "anthropic:priority"}, Prices: market.TokenPrices{Input: mustAmount(t, "1"), Output: mustAmount(t, "2")}}}
	})
	if len(saved.Tiers) != 1 || saved.Tiers[0].Conditions.ServiceTier != "anthropic:priority" {
		t.Fatal("tier quote lost", saved)
	}
	_, secret := e.defaultKey(consumer)
	done := make(chan string, 1)
	go func() {
		got := e.call(secret, http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-test"}`), nil)
		if got.Code != 200 {
			t.Errorf("call=%d %s", got.Code, got.Body.String())
		}
		done <- got.Header().Get("X-AIHub-Request-Id")
	}()
	select {
	case <-entered:
	case id := <-done:
		t.Fatalf("call ended before upstream: %s", id)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("upstream was not reached")
	}
	e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) { q.Tiers = nil })
	close(release)
	id := <-done
	row := e.callRow(id)
	if !row.HasLedgerTx || row.CostNano != mustAmount(t, "0.002").Nano() {
		t.Fatalf("price did not freeze: %+v", row)
	}
	var raw []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Detail   ledger.UsageDetail `json:"detail"`
		Selected struct {
			Geo map[string]string `json:"geo_multipliers"`
		} `json:"selected_prices"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Detail.InferenceGeo != "us" || len(snapshot.Selected.Geo) != 0 || snapshot.Detail.Units["web_search"] != 2*ledger.FixedPointScale {
		t.Fatalf("snapshot=%s err=%v", raw, err)
	}
	if e.balance(consumer) != "-0.002002" {
		t.Fatalf("unexpected tool fee: %s", e.balance(consumer))
	}
	e.assertZeroSum()
}
