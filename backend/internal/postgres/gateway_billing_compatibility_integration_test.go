package postgres_test

import (
	"context"
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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

func TestRegionalPricesFreezeAtCallStartAndPersistInSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer, sharer := e.member("region-consumer", "100"), e.member("region-sharer", "0")
	geo := map[string]int64{"us": 1_100_000_000}
	tiers := []ledger.PriceTier{{Name: "priority us", ServiceTier: "anthropic:priority", InputPrice: mustAmount(t, "1"), OutputPrice: mustAmount(t, "2"), GeoMultipliers: map[string]int64{"us": 1_200_000_000}}}
	saved, err := e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{GeoMultipliers: &geo, PriceTiers: &tiers})
	if err != nil || saved.GeoMultipliers["us"] != geo["us"] || saved.PriceTiers[0].GeoMultipliers["us"] != 1_200_000_000 {
		t.Fatalf("save=%+v %v", saved, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"usage":{"input_tokens":1000,"output_tokens":500,"service_tier":"priority","inference_geo":"us","server_tool_use":{"web_search_requests":2}}}`)
	})
	e.createChannel(sharer, channelSpec{name: "regional", upstream: upstream, multiplier: "1"})
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
	empty := map[string]int64{}
	_, err = e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{GeoMultipliers: &empty, PriceTiers: &[]ledger.PriceTier{}})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	id := <-done
	row := e.callRow(id)
	if !row.HasLedgerTx || row.CostNano != mustAmount(t, "0.0024").Nano() {
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
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Detail.InferenceGeo != "us" || snapshot.Selected.Geo["us"] != "1.2" || snapshot.Detail.Units["web_search"] != 2*ledger.FixedPointScale {
		t.Fatalf("snapshot=%s err=%v", raw, err)
	}
	if e.balance(consumer) != "-0.0024024" {
		t.Fatalf("unexpected tool fee: %s", e.balance(consumer))
	}
	e.assertZeroSum()
}
