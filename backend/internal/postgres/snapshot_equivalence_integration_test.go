package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// snapshotWorld is a data set that touches every rule of the snapshots: listed,
// unlisted, suspended and deleted channels, a channel whose owner is disabled,
// custom models and a custom model turned official (pending its multiplier),
// USD models with and without the exchange-rate discount, price tiers, keys
// that are enabled, disabled, deleted, expired, of a disabled account, with
// aliases, allowed models and budgets, and routing preferences of the account
// and of a key.
type snapshotWorld struct {
	e                                   *env
	consumer, sharerA, sharerB, sharerC person
	channels                            map[string]string // name -> id
	keys                                map[string]string // name -> platform key (secret)
	keyIDs                              map[string]string // name -> key id
}

func buildSnapshotWorld(t *testing.T) *snapshotWorld {
	t.Helper()
	e := newEnv(t)
	w := &snapshotWorld{e: e, channels: map[string]string{}, keys: map[string]string{}, keyIDs: map[string]string{}}
	w.consumer = e.member("consumer", "1000")
	w.sharerA, w.sharerB, w.sharerC = e.member("sharera", "0"), e.member("sharerb", "0"), e.member("sharerc", "0")
	relay := okUpstream(t)
	prices := map[string]string{"input": "1", "output": "2", "cache_write": "0", "cache_read": "0"}

	// Models: a USD one, tiers and fine-grained prices on gpt-test.
	e.apiJSON(e.admin, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "usd-model", "display_name": "USD model", "price_currency": "USD", "sort_order": 3, "base_prices": prices,
	})
	minimum := int64(900)
	specific := map[string]money.Amount{"input_audio": 0, "output_audio": mustAmount(t, "40")}
	tiers := []ledger.PriceTier{{Name: "long", MinPromptTokens: &minimum, InputPrice: mustAmount(t, "40"), OutputPrice: mustAmount(t, "20"), TokenPrices: specific}}
	if _, err := e.catalog.Update(context.Background(), e.admin.id, "gpt-test", catalog.ModelPatch{TokenPrices: &specific, PriceTiers: &tiers}); err != nil {
		t.Fatal(err)
	}

	create := func(owner person, name string, body map[string]any) {
		body["name"], body["base_url"], body["api_key"] = name, relay.server.URL, "upstream-key-"+name
		created := e.apiJSON(owner, http.StatusCreated, http.MethodPost, "/api/channels", body)
		w.channels[name] = created["channel"].(map[string]any)["id"].(string)
	}
	custom := func(id string, input string, formats ...string) map[string]any {
		return map[string]any{"model_id": id, "custom_prices": map[string]string{"input": input, "output": "2", "cache_write": "0", "cache_read": "0"}}
	}
	create(w.sharerA, "a", map[string]any{
		"models": []any{
			map[string]any{"model_id": "gpt-test", "multiplier": "1"},
			map[string]any{"model_id": "claude-test", "multiplier": "3"},
			map[string]any{"model_id": "gemini-test", "multiplier": "2", "upstream_model": "gem-up"},
			map[string]any{"model_id": "usd-model", "multiplier": "1.2", "fx_discount": true},
		},
		"advanced": map[string]any{
			"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}},
			"user_agent":    "ua-a", "concurrency_limit": 3, "rpm_limit": 60, "daily_revenue_cap": "50", "ttft_timeout_ms": 5000,
			"request_timeout_ms": 20000, "idle_timeout_ms": 3000, "cooldown_failures": 4, "cooldown_seconds": 60,
			"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-A", "value": "1"}}, "remove": []any{"X-B"}},
		},
	})
	create(w.sharerB, "b", map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{
		map[string]any{"model_id": "gpt-test", "multiplier": "2"},
		map[string]any{"model_id": "usd-model", "multiplier": "1"},
		custom("my-custom", "1", "openai_chat", "anthropic"),
		custom("convert-me", "1", "openai_chat"),
	}})
	create(w.sharerB, "c", map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{custom("my-custom", "3", "openai_chat")}})
	create(w.sharerA, "unlisted", map[string]any{"status": "unlisted", "advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{map[string]any{"model_id": "gpt-test"}}})
	create(w.sharerA, "suspended", map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{map[string]any{"model_id": "gpt-test"}}})
	create(w.sharerA, "deleted", map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{map[string]any{"model_id": "gpt-test"}}})
	create(w.sharerC, "ownerless", map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream", "stream"}}}, "models": []any{map[string]any{"model_id": "gpt-test"}}})
	e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+w.channels["suspended"]+"/suspend", map[string]any{"reason": "test"})
	if recorder := e.api(w.sharerA, http.MethodDelete, "/api/channels/"+w.channels["deleted"], nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d", recorder.Code)
	}
	// A new catalog model takes the custom model of the same name over.
	e.apiJSON(e.admin, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{"id": "convert-me", "display_name": "Convert", "base_prices": prices})
	// A disabled model keeps its channels.
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/models/claude-test", map[string]any{"enabled": false})

	// Keys.
	defaultID, defaultSecret := e.defaultKey(w.consumer)
	w.keyIDs["default"], w.keys["default"] = defaultID, defaultSecret
	newKey := func(name string, body map[string]any) {
		body["name"] = name
		created := e.apiJSON(w.consumer, http.StatusCreated, http.MethodPost, "/api/keys", body)
		w.keys[name], w.keyIDs[name] = created["secret"].(string), created["key"].(map[string]any)["id"].(string)
	}
	newKey("rich", map[string]any{
		"allowed_models": []string{"gpt-test", "usd-model"}, "model_aliases": map[string]string{"short": "gpt-test"},
		"budget_daily": "10", "budget_monthly": "100", "budget_total": "1000", "expires_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
	})
	newKey("disabled", map[string]any{"status": "disabled"})
	newKey("deleted", map[string]any{})
	newKey("expired", map[string]any{})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+w.keyIDs["expired"], map[string]any{"expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	if recorder := e.api(w.consumer, http.MethodDelete, "/api/keys/"+w.keyIDs["deleted"], nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete key = %d", recorder.Code)
	}
	ownerlessID, ownerlessSecret := e.defaultKey(w.sharerC)
	w.keyIDs["ownerless"], w.keys["ownerless"] = ownerlessID, ownerlessSecret
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+w.sharerC.id, map[string]any{"status": "disabled"})

	// Routing preferences.
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{
		"mode": "manual", "order": []string{w.channels["b"], w.channels["a"]}, "excluded": []string{w.channels["c"]}, "max_attempts": 4,
	})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/routing/my-custom", map[string]any{"mode": "manual", "ttft_timeout_ms": 7000, "idle_timeout_ms": 2000, "request_timeout_ms": 12000})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/keys/"+w.keyIDs["rich"]+"/routing/gpt-test", map[string]any{"mode": "manual", "max_attempts": 2, "ttft_timeout_ms": 5000, "idle_timeout_ms": 3000, "request_timeout_ms": 15000})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/keys/"+defaultID+"/routing/usd-model", map[string]any{"mode": "manual", "order": []string{w.channels["b"]}})
	e.apiJSON(w.sharerA, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "excluded": []string{w.channels["b"]}})
	return w
}

// compare checks every read the gateway makes through the snapshots against
// the database store, and reports the differences.
func (w *snapshotWorld) compare(t *testing.T, label string, cached *storepg.Cached) {
	t.Helper()
	ctx := context.Background()
	raw := w.e.store
	rawCatalog := catalog.NewService(raw.Catalog)

	modelNames := []string{"gpt-test", "claude-test", "gemini-test", "usd-model", "convert-me", "my-custom", "nope"}
	formats := []channel.Format{channel.FormatOpenAIResponses, channel.FormatOpenAIImages, "bogus"}

	nonEmpty := 0
	for _, model := range modelNames {
		for _, format := range formats {
			want, err := raw.Gateway.Candidates(ctx, model, format)
			if err != nil {
				t.Fatal(err)
			}
			got, err := cached.Gateway.Candidates(ctx, model, format)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: candidates of %s/%s differ:\n snapshot %+v\n database %+v", label, model, format, got, want)
			}
			if len(want) > 0 {
				nonEmpty++
			}
		}
		wantCustom, _ := raw.Gateway.CustomModelOnline(ctx, model)
		if gotCustom, err := cached.Gateway.CustomModelOnline(ctx, model); err != nil || gotCustom != wantCustom {
			t.Errorf("%s: CustomModelOnline(%s) = %v, %v; database %v", label, model, gotCustom, err, wantCustom)
		}
		wantModel, wantErr := rawCatalog.Get(ctx, model)
		gotModel, gotErr := cached.Models.Get(ctx, model)
		if !errors.Is(gotErr, wantErr) && !(gotErr == nil && wantErr == nil) || !reflect.DeepEqual(gotModel, wantModel) {
			t.Errorf("%s: model %s differs:\n snapshot %+v (%v)\n database %+v (%v)", label, model, gotModel, gotErr, wantModel, wantErr)
		}
	}
	if nonEmpty < 5 {
		t.Fatalf("%s: only %d (model, format) pairs have candidates: the data set is too thin", label, nonEmpty)
	}
	if got, _ := cached.Models.Get(ctx, "  gpt-test "); got.ID != "gpt-test" {
		t.Errorf("%s: a padded name is not found", label)
	}
	wantOnline, err := raw.Gateway.OnlineModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotOnline, err := cached.Gateway.OnlineModels(ctx); err != nil || !reflect.DeepEqual(gotOnline, wantOnline) {
		t.Errorf("%s: online models differ:\n snapshot %+v (%v)\n database %+v", label, gotOnline, err, wantOnline)
	}
	for _, includeDisabled := range []bool{false, true} {
		want, err := rawCatalog.List(ctx, includeDisabled)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := cached.Models.List(ctx, includeDisabled); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: catalog list (disabled %v) differs:\n snapshot %+v (%v)\n database %+v", label, includeDisabled, got, err, want)
		}
	}

	known := 0
	for name, secret := range w.keys {
		want, wantErr := raw.Gateway.LookupKey(ctx, apikey.Hash(secret))
		got, gotErr := cached.Gateway.LookupKey(ctx, apikey.Hash(secret))
		if !errors.Is(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: key %s differs:\n snapshot %+v (%v)\n database %+v (%v)", label, name, got, gotErr, want, wantErr)
		}
		if wantErr == nil {
			known++
		}
	}
	if _, err := cached.Gateway.LookupKey(ctx, apikey.Hash("sk-aih-never-issued")); !errors.Is(err, gateway.ErrKeyNotFound) {
		t.Errorf("%s: an unknown key = %v", label, err)
	}
	if known != 5 { // default, rich, disabled, expired and ownerless are found; the deleted one is not
		t.Errorf("%s: %d known keys, want 5", label, known)
	}

	accountIDs := []string{w.consumer.id, w.sharerA.id, w.sharerB.id, w.e.admin.id, "00000000-0000-4000-8000-000000000000"}
	keyIDs := []string{"", w.keyIDs["default"], w.keyIDs["rich"], w.keyIDs["disabled"], "00000000-0000-4000-8000-0000000000aa"}
	stored := 0
	for _, account := range accountIDs {
		for _, key := range keyIDs {
			for _, model := range modelNames {
				want, wantErr := raw.Routes.Resolve(ctx, account, key, model)
				got, gotErr := cached.Routing.Resolve(ctx, account, key, model)
				if wantErr != nil || gotErr != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("%s: routing for %s/%s/%s differs:\n snapshot %+v (%v)\n database %+v (%v)", label, account, key, model, got, gotErr, want, wantErr)
				}
				if want.Source != "default" {
					stored++
				}
			}
		}
	}
	if stored < 6 {
		t.Fatalf("%s: only %d resolutions found a stored preference: the data set is too thin", label, stored)
	}
}

func TestSnapshotsAnswerLikeTheDatabase(t *testing.T) {
	w := buildSnapshotWorld(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A snapshot built from scratch.
	fresh := w.e.store.Cached(logger)
	if err := fresh.Cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.compare(t, "built from the database", fresh)
	// The one the server kept current through the invalidation calls of every
	// change that built this data set must be the same.
	w.compare(t, "kept current by invalidations", w.e.cached)

	// The conditions that exclude a channel really do.
	candidates, err := fresh.Gateway.Candidates(context.Background(), "gpt-test", channel.FormatOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("channels offering gpt-test in openai_responses = %v, want a and b (not unlisted, suspended, deleted, of a disabled owner)", names)
	}
	if online, _ := fresh.Gateway.CustomModelOnline(context.Background(), "convert-me"); online {
		t.Fatal("a custom model turned official is online while it waits for its multiplier")
	}
	if online, _ := fresh.Gateway.CustomModelOnline(context.Background(), "my-custom"); !online {
		t.Fatal("a custom model is not online")
	}
}

// After warm-up, a request reads nothing the snapshots hold from the database,
// and not the balance either (G8.2): the check before a request is let through
// is answered from memory, so what is left before the upstream is opening the
// call.
func TestGatewayRequestsDoNotReadSnapshotDataFromTheDatabase(t *testing.T) {
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
	for range 3 { // settings, spend and touch caches
		if recorder := chatRequest(e, secret); recorder.Code != http.StatusOK {
			t.Fatalf("warm-up = %d", recorder.Code)
		}
	}

	const requests = 20
	before, _ := counter.Counts()
	for range requests {
		if recorder := chatRequest(e, secret); recorder.Code != http.StatusOK {
			t.Fatalf("request = %d", recorder.Code)
		}
	}
	after, _ := counter.Counts()
	statements := gatewaybench.Since(after, before)
	t.Logf("statements for %d requests: %v (%.1f per request)", requests, statements, float64(gatewaybench.Total(statements))/requests)
	for _, name := range []string{
		"LookupKey", "GetModel", "ListModels", "ListTiers", "ListCandidates", "ListAllCandidates", "ListAllKeys", "ListAllRoutePrefs",
		"ListRoutePrefs", "ListRoutePrefChannels", "CustomModelOnline", "ListOnlineModels", "ResponseChannel",
		"GetPoints", "ListPoints",
	} {
		if statements[name] != 0 {
			t.Errorf("%d requests ran %d x %s: the snapshots should have answered", requests, statements[name], name)
		}
	}
	if statements["InsertCall"] != requests {
		t.Errorf("InsertCall = %d, want %d", statements["InsertCall"], requests)
	}
}
