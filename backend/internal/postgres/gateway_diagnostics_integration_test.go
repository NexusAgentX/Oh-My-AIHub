package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func waitForSettlementTiming(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ready bool
		if err := pool.QueryRow(context.Background(), `SELECT coalesce(diagnostics->'stages' @> '[{"stage":"settlement"}]'::jsonb, false) FROM calls WHERE id=$1`, id).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("settlement timing not flushed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The real traced transaction, including deferred constraints and COMMIT, must
// never grow its serialized tail just to publish observational detail.
func TestGatewayDiagnosticsKeepLedgerLockTailAtTwoStatements(t *testing.T) {
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
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	e.createChannel(sharer, channelSpec{name: "relay", upstream: okUpstream(t), upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)
	counter.WatchHold("LockLedgerAccounts")
	response := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	id := response.Header().Get("X-AIHub-Request-Id")
	holds := counter.Holds()
	if holds.Count != 1 || holds.Statements() != 2 || holds.Steps["BookEntries"].Count != 1 || holds.Steps["(commit)"].Count != 1 {
		t.Fatalf("hold tail %+v", holds)
	}
	row := e.callRow(id)
	if !row.HasLedgerTx || row.CostNano != 40_000_000 {
		t.Fatalf("billing was not synchronous: %+v", row)
	}
	var raw []byte
	if err := traced.QueryRow(context.Background(), "SELECT diagnostics FROM calls WHERE id=$1", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var diagnostics map[string]any
	if err := json.Unmarshal(raw, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics["delivery"] != "complete" || diagnostics["usage_status"] != "reported" {
		t.Fatalf("diagnostic facts not committed: %s", raw)
	}
	waitForSettlementTiming(t, traced, id)
	_, background := counter.Counts()
	if background["SetCallSettlements"] == 0 {
		t.Fatal("batch writer not included in background accounting")
	}
	e.assertZeroSum()
}

func TestGatewaySettlesWhileCallAnalysesCompete(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	e.createChannel(sharer, channelSpec{name: "relay", upstream: okUpstream(t), upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	var readers sync.WaitGroup
	var reports atomic.Int64
	for i := 0; i < 8; i++ {
		readers.Go(func() {
			for ctx.Err() == nil {
				_, err := e.observe.Analysis(ctx, observe.CallFilter{AccountID: consumer.id, From: &from, To: &to}, "model", "all", 3600)
				if err == nil {
					reports.Add(1)
				} else if ctx.Err() == nil && !errors.Is(err, observe.ErrAnalysisBusy) && !errors.Is(err, observe.ErrAnalysisTimeout) {
					t.Errorf("analysis: %v", err)
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		})
	}
	defer func() { cancel(); readers.Wait() }()
	for i := 0; i < 10; i++ {
		response := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
		if response.Code != http.StatusOK {
			t.Fatalf("gateway under analysis load: %d %s", response.Code, response.Body.String())
		}
		if row := e.callRow(response.Header().Get("X-AIHub-Request-Id")); !row.HasLedgerTx {
			t.Fatalf("unbooked call: %+v", row)
		}
	}
	cancel()
	readers.Wait()
	if reports.Load() == 0 {
		t.Fatal("no analysis completed alongside gateway calls")
	}
	e.assertZeroSum()
}
