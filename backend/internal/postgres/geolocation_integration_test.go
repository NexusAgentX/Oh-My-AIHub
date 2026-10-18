package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/geolocation"
)

func TestGeoCacheQuotaAndEnrichmentPreserveCallFacts(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	e.createChannel(sharer, channelSpec{name: "relay", upstream: okUpstream(t), upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)
	response := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	id := response.Header().Get("X-AIHub-Request-Id")
	// Two nodes sharing an IP exercise both JSON paths, including preserved phases.
	network := gateway.Network{Client: gateway.NetworkNode{IP: "8.8.8.8"}, Attempts: []*gateway.NetworkAttempt{{Node: gateway.NetworkNode{IP: "8.8.8.8"}, Phases: []gateway.NetworkPhase{{Stage: "send", OffsetMS: 1, DurationMS: 2}}}}}
	raw, _ := json.Marshal(network)
	if _, err := e.pool.Exec(ctx, "UPDATE calls SET diagnostics=jsonb_set(diagnostics,'{network}',$2) WHERE id=$1", id, raw); err != nil {
		t.Fatal(err)
	}
	pos := &geolocation.Location{Label: "Example", Latitude: 20, Longitude: 30}
	if err := e.store.Gateway.SaveGeoCache(ctx, geolocation.Entry{IP: "8.8.8.8", Location: pos, Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	entries, err := e.store.Gateway.LoadGeoCache(ctx)
	if err != nil || len(entries) != 1 || entries[0].Location.Label != "Example" {
		t.Fatalf("cache: %v %v", entries, err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	for i := range 3 {
		ok, err := e.store.Gateway.ReserveGeoLookup(ctx, day, 2)
		if err != nil || ok != (i < 2) {
			t.Fatalf("reservation %d: %v %v", i, ok, err)
		}
	}
	if err := e.store.Gateway.EnrichCallLocations(ctx, id, map[string]*geolocation.Location{"8.8.8.8": pos}); err != nil {
		t.Fatal(err)
	}
	var diagnostics gateway.Diagnostics
	var stored []byte
	if err := e.pool.QueryRow(ctx, "SELECT diagnostics FROM calls WHERE id=$1", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stored, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Network.Client.Location.Label != "Example" || diagnostics.Network.Attempts[0].Node.Location.Label != "Example" || len(diagnostics.Network.Attempts[0].Phases) != 1 || diagnostics.Delivery != "complete" {
		t.Fatalf("enrichment damaged facts: %s", stored)
	}
	row := e.callRow(id)
	if !row.HasLedgerTx || row.CostNano != 40_000_000 {
		t.Fatalf("ledger changed: %+v", row)
	}
	e.assertZeroSum()
}
