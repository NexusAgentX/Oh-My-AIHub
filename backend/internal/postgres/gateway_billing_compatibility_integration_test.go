package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestGatewayContainerReceiptsAreAtomicAcrossConcurrentCalls(t *testing.T) {
	e := newEnv(t)
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	units := map[string]money.Amount{"container_1gb": mustAmount(t, "0.03")}
	if _, err := e.catalog.Update(context.Background(), e.admin.id, "gpt-test", catalog.ModelPatch{UnitPrices: &units}); err != nil {
		t.Fatal(err)
	}
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_container","status":"completed","usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"ci_one","type":"code_interpreter_call","container_id":"cn_shared"}]}`)
	})
	e.createChannel(sharer, channelSpec{name: "container", upstream: upstream, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
	const count = 4
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			got := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","tools":[{"type":"code_interpreter","container":{"type":"auto"}}]}`), nil)
			if got.Code != 200 {
				t.Errorf("call=%d %s", got.Code, got.Body.String())
				return
			}
			ids <- got.Header().Get("X-AIHub-Request-Id")
		})
	}
	wg.Wait()
	close(ids)
	var total int64
	for id := range ids {
		row := e.callRow(id)
		total += row.CostNano
		if !row.HasLedgerTx {
			t.Errorf("unbooked %+v", row)
		}
	}
	// Existing fixture prices: input=10/output=20 points per million.
	expected := int64(count)*mustAmount(t, "0.0014").Nano() + mustAmount(t, "0.03").Nano()
	if total != expected {
		t.Fatalf("total=%d want=%d", total, expected)
	}
	var receipts int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM gateway_unit_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts=%d err=%v", receipts, err)
	}
	e.assertZeroSum()
}

func TestGatewayBackgroundSurvivesRestartAndCredentialEdit(t *testing.T) {
	e := newEnv(t)
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
	e.apiJSON(sharer, 200, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"api_key": "new-secret"})
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
