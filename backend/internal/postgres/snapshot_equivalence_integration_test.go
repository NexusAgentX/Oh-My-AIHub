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
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// snapshotWorld covers independent group quotes, same-ID offerings, explicit
// exclusions/governance, every channel/owner status, native formats, keys and
// account/Key routing overrides. No global model or custom-price takeover exists.
type snapshotWorld struct {
	e                                   *env
	consumer, sharerA, sharerB, sharerC person
	channels                            map[string]string // name -> id
	keys                                map[string]string // name -> platform key (secret)
	keyIDs                              map[string]string // name -> key id
}

func buildSnapshotWorld(t *testing.T) *snapshotWorld {
	t.Helper()
	e := newGroupEnv(t)
	w := &snapshotWorld{e: e, channels: map[string]string{}, keys: map[string]string{}, keyIDs: map[string]string{}}
	w.consumer = e.member("consumer", "1000")
	w.sharerA, w.sharerB, w.sharerC = e.member("sharera", "0"), e.member("sharerb", "0"), e.member("sharerc", "0")
	relay := okUpstream(t)
	create := func(owner person, name, model, discount string, formats []string, overrides map[string]any) {
		w.channels[name] = e.createChannel(owner, channelSpec{name: name, model: model, multiplier: discount, formats: formats, upstream: relay, advanced: overrides})
	}
	all := []string{"openai_chat", "openai_responses", "anthropic", "gemini"}
	create(w.sharerA, "a", "gpt-test", "1", []string{"openai_chat", "openai_responses"}, map[string]any{
		"user_agent": "ua-a", "concurrency_limit": 3, "rpm_limit": 60, "ttft_timeout_ms": 5000, "request_timeout_ms": 20000, "idle_timeout_ms": 3000, "cooldown_failures": 4, "cooldown_seconds": 60,
		"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-A", "value": "1"}}, "remove": []any{"X-B"}},
	})
	minimum := int64(900)
	specific := map[string]money.Amount{"input_audio": 0, "output_audio": mustAmount(t, "40")}
	e.reviseGroupedQuote(w.channels["a"], "gpt-test", func(q *market.Quote) {
		q.Prices.Details = specific
		q.Tiers = []market.Tier{{Name: "long", Conditions: market.Conditions{MinPromptTokens: &minimum}, Prices: market.TokenPrices{Input: mustAmount(t, "40"), Output: mustAmount(t, "20"), Details: specific}}}
	})
	create(w.sharerA, "a-claude", "claude-test", "3", []string{"anthropic"}, nil)
	create(w.sharerA, "a-gemini", "gemini-test", "2", []string{"gemini", "openai_chat"}, nil)
	create(w.sharerA, "a-usd", "usd-model", "1.2", []string{"openai_chat"}, nil)
	create(w.sharerB, "b", "gpt-test", "2", []string{"openai_chat"}, nil)
	create(w.sharerB, "b-usd", "usd-model", "1", []string{"openai_chat"}, nil)
	for _, id := range []string{w.channels["a-usd"], w.channels["b-usd"]} {
		e.reviseGroupedQuote(id, "usd-model", func(q *market.Quote) {
			q.Currency = "USD"
			q.Prices.Input = mustAmount(t, "1")
			q.Prices.Output = mustAmount(t, "2")
		})
	}
	create(w.sharerB, "b-private", "my-custom", "1", []string{"openai_chat", "openai_responses", "anthropic"}, nil)
	create(w.sharerB, "c", "my-custom", "1", []string{"openai_chat"}, nil)
	e.reviseGroupedQuote(w.channels["c"], "my-custom", func(q *market.Quote) { q.Prices.Input = mustAmount(t, "3") })
	create(w.sharerB, "excluded", "excluded-model", "1", []string{"openai_chat"}, nil)
	excluded := e.apiJSON(w.sharerB, 200, "GET", "/api/group-channels/"+w.channels["excluded"], nil)["models"].([]any)[0].(map[string]any)["group_model_id"]
	e.patchGroupedChannel(w.sharerB, w.channels["excluded"], map[string]any{"models": []any{map[string]any{"group_model_id": excluded, "excluded": true, "discount": "1"}}})
	for _, name := range []string{"unlisted", "suspended", "deleted"} {
		create(w.sharerA, name, "gpt-test", "1", all, nil)
	}
	create(w.sharerC, "ownerless", "gpt-test", "1", all, nil)
	e.patchGroupedChannel(w.sharerA, w.channels["unlisted"], map[string]any{"status": "unlisted"})
	if reply := e.api(e.admin, "POST", "/api/admin/group-channels/"+w.channels["suspended"]+"/moderate", map[string]any{"reason": "test"}); reply.Code != 204 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	if reply := e.deleteGroupedChannel(w.sharerA, w.channels["deleted"]); reply.Code != 204 {
		t.Fatal(reply.Code)
	}
	e.publishGroupFixture(market.Changes{Governance: []market.Change[market.ModelRule]{{Value: market.ModelRule{ModelID: "claude-test", Disabled: true, Reason: "synthetic governance"}}}})

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
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/routing/my-custom", map[string]any{"mode": "reliable", "ttft_timeout_ms": 7000, "idle_timeout_ms": 2000, "request_timeout_ms": 12000})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/keys/"+w.keyIDs["rich"]+"/routing/gpt-test", map[string]any{"mode": "fastest", "max_attempts": 2, "ttft_timeout_ms": 5000, "idle_timeout_ms": 3000, "request_timeout_ms": 15000})
	e.apiJSON(w.consumer, http.StatusOK, http.MethodPut, "/api/keys/"+defaultID+"/routing/usd-model", map[string]any{"mode": "manual", "order": []string{w.channels["b"]}})
	e.apiJSON(w.sharerA, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "cheapest", "excluded": []string{w.channels["b"]}})
	return w
}

func (w *snapshotWorld) compare(t *testing.T, label string, cached *storepg.Cached, source snapshot.Loader) {
	t.Helper()
	ctx := context.Background()
	raw := w.e.store
	rows, err := source.LoadCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	models, err := source.LoadModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	modelNames := []string{"gpt-test", "claude-test", "gemini-test", "usd-model", "excluded-model", "my-custom", "nope"}
	formats := []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini, "bogus"}
	nonEmpty := 0
	for _, model := range modelNames {
		for _, format := range formats {
			want := []gateway.Candidate{}
			for _, row := range rows {
				if row.ModelID == model && slices.Contains(row.Formats, format) {
					want = append(want, row.Candidate)
				}
			}
			got, err := cached.Gateway.Candidates(ctx, model, format)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("%s: candidates %s/%s differ: cache=%+v source=%+v err=%v", label, model, format, got, want, err)
			}
			if len(want) > 0 {
				nonEmpty++
			}
		}
		var want catalog.Model
		wantErr := error(catalog.ErrNotFound)
		for _, v := range models {
			if v.ID == model {
				want = v
				wantErr = nil
			}
		}
		got, gotErr := cached.Models.Get(ctx, model)
		if !errors.Is(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: model %s differs: cache=%+v/%v source=%+v/%v", label, model, got, gotErr, want, wantErr)
		}
	}
	if nonEmpty < 8 {
		t.Fatalf("%s: only %d model/format pairs have candidates", label, nonEmpty)
	}
	if got, _ := cached.Models.Get(ctx, "  gpt-test "); got.ID != "gpt-test" {
		t.Errorf("%s: padded internal model lookup", label)
	}
	wantOnline := []gateway.OnlineModel{
		{ID: "gemini-test", Formats: []channel.Format{channel.FormatGemini, channel.FormatOpenAIChat}},
		{ID: "gpt-test", Formats: []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses}},
		{ID: "my-custom", Formats: []channel.Format{channel.FormatAnthropic, channel.FormatOpenAIChat, channel.FormatOpenAIResponses}},
		{ID: "usd-model", Formats: []channel.Format{channel.FormatOpenAIChat}},
	}
	gotOnline, err := cached.Gateway.OnlineModels(ctx)
	for i := range wantOnline {
		slices.Sort(wantOnline[i].Formats)
	}
	for i := range gotOnline {
		slices.Sort(gotOnline[i].Formats)
	}
	if err != nil || !reflect.DeepEqual(gotOnline, wantOnline) {
		t.Errorf("%s: online directory differs: cache=%+v want=%+v err=%v", label, gotOnline, wantOnline, err)
	}
	for _, includeDisabled := range []bool{false, true} {
		want := []catalog.Model{}
		for _, model := range models {
			if includeDisabled || model.Enabled {
				want = append(want, model)
			}
		}
		got, err := cached.Models.List(ctx, includeDisabled)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: aggregate IDs disabled=%v: cache=%+v source=%+v err=%v", label, includeDisabled, got, want, err)
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

func (w *snapshotWorld) fresh(t *testing.T, logger *slog.Logger) (*storepg.Cached, snapshot.Loader) {
	t.Helper()
	runtime := &groupchannel.Runtime{Repository: w.e.groupStore, CredentialScope: w.e.groups.CredentialScope}
	var source snapshot.Loader
	fresh := w.e.store.Cached(logger, func(c *snapshot.Config) {
		source = groupIntegrationEvidence{Loader: runtime.Wrap(c.Loader), Prices: &groupIntegrationPriceSource{}, Proofs: w.e.groupProofs}
		c.Loader = source
	})
	runtime.Rebuild = func(ctx context.Context) error {
		return fresh.Cache.InvalidateChecked(ctx, snapshot.DomainModels|snapshot.DomainFleet)
	}
	if err := runtime.Install(context.Background(), marketSnapshot(t, w.e.store)); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return fresh, source
}

func TestSnapshotsAnswerLikeTheDatabase(t *testing.T) {
	w := buildSnapshotWorld(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fresh, source := w.fresh(t, logger)
	w.compare(t, "built from group persistence", fresh, source)
	w.compare(t, "kept current by invalidations", w.e.cached, source)
	candidates, err := fresh.Gateway.Candidates(context.Background(), "gpt-test", channel.FormatOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
		if candidate.Quotes == nil || !candidate.ValidOffering("gpt-test") {
			t.Fatal("group candidate lost independent quote", candidate.ChannelID)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("gpt-test channels=%v; unavailable owners/statuses must be absent", names)
	}
	if rows, _ := fresh.Gateway.Candidates(context.Background(), "excluded-model", channel.FormatOpenAIChat); len(rows) != 0 {
		t.Fatal("persistent model exclusion was lost")
	}
	if model, err := fresh.Models.Get(context.Background(), "claude-test"); err != nil || model.Enabled {
		t.Fatal("exact-ID governance was lost", model, err)
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
	e := newGroupEnvOn(t, traced, storepg.New(traced), nil)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
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
