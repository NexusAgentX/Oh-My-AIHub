package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func marketID(t *testing.T) string {
	t.Helper()
	id, err := ids.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func marketBundle(t *testing.T, kind string) market.Changes {
	t.Helper()
	supplier := market.Supplier{ID: marketID(t), Name: "合成供应商", Kind: kind, WebsiteURL: "https://synthetic.example", Enabled: true}
	group := market.Group{ID: marketID(t), SupplierID: supplier.ID, Name: "合成分组", Enabled: true}
	address := market.Address{ID: marketID(t), SupplierID: supplier.ID, BaseURL: "https://SYNTHETIC.example:443/v1/", Label: "入口"}
	model := market.GroupModel{ID: marketID(t), GroupID: group.ID, ModelID: "publishers/test/models/model:2026", ModelType: "text", Enabled: true}
	return market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: supplier}}, Groups: []market.Change[market.Group]{{Value: group}}, Addresses: []market.Change[market.Address]{{Value: address}}, GroupAddresses: []market.GroupAddressSet{{GroupID: group.ID, Links: []market.GroupAddress{{ID: marketID(t), GroupID: group.ID, AddressID: address.ID, SupplierID: supplier.ID}}}}, Models: []market.Change[market.GroupModel]{{Value: model}}}
}
func marketSnapshot(t *testing.T, s *storepg.Store) market.Directory {
	t.Helper()
	d, err := s.Market.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func marketApply(t *testing.T, s *storepg.Store, actor string, c market.Changes) {
	t.Helper()
	if err := s.Market.Apply(context.Background(), actor, c); err != nil {
		t.Fatal(err)
	}
}

func TestMarketTypedPublicationAndImmutableQuotes(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	c := marketBundle(t, "official_api")
	c.Groups[0].Value.Defaults.HeaderRules = &market.HeaderRules{}
	window, output := int64(131072), int64(8192)
	c.Models[0].Value.ContextWindow = &window
	c.Models[0].Value.MaxOutputTokens = &output
	c.Models[0].Value.IsReference = true
	p := market.TokenPrices{Input: 0, Output: mustAmount(t, "2.5"), CacheWrite: mustAmount(t, "1.1"), CacheRead: mustAmount(t, "0.25"), Details: map[string]money.Amount{}}
	for _, k := range []string{"input_text", "input_image", "input_audio", "input_video", "output_text", "output_image", "output_audio", "output_video", "cache_read_text", "cache_read_image", "cache_read_audio", "cache_read_video", "cache_write_5m", "cache_write_1h"} {
		p.Details[k] = mustAmount(t, "0.123456789")
	}
	min, max := int64(1000), int64(5000)
	start, end := int16(1380), int16(60)
	q := market.Quote{ID: marketID(t), GroupModelID: c.Models[0].Value.ID, Currency: "USD", BillingUnit: "per_million_tokens", Prices: p, Tiers: []market.Tier{{Name: "夜间", Conditions: market.Conditions{ServiceTier: "openai:flex", MinPromptTokens: &min, MaxPromptTokens: &max, Timezone: "Asia/Shanghai", Weekdays: []int{1, 3, 5}, StartMinute: &start, EndMinute: &end}, Prices: p}}, UnitPrices: []market.UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: mustAmount(t, "0.01")}, {Unit: "tool_second", Tool: "code_interpreter", Price: mustAmount(t, "0.02")}}}
	c.Quotes = []market.Quote{q}
	marketApply(t, s, admin.ID, c)
	d := marketSnapshot(t, s)
	if len(d.Models) != 1 || d.Models[0].ModelID != c.Models[0].Value.ModelID || d.Models[0].UpstreamModel != c.Models[0].Value.ModelID || d.Models[0].DisplayName != c.Models[0].Value.ModelID {
		t.Fatalf("native model lost: %+v", d.Models)
	}
	if d.Addresses[0].BaseURL != "https://synthetic.example" || d.Addresses[0].Revision != 1 {
		t.Fatalf("canonical/version: %+v", d.Addresses)
	}
	if d.Groups[0].Defaults.HeaderRules == nil || d.Groups[0].Defaults.UserAgent != nil {
		t.Fatal("explicit empty and inherit coalesced")
	}
	got := d.Quotes[0]
	if got.Currency != "USD" || got.Version != 1 || !reflect.DeepEqual(got.Prices, p) || !reflect.DeepEqual(got.Tiers, q.Tiers) || !reflect.DeepEqual(got.UnitPrices, q.UnitPrices) {
		t.Fatalf("typed quote roundtrip mismatch: %+v", got)
	}
	// A quote version is append-only, including all child prices.
	for _, sql := range []string{`UPDATE market_quotes SET currency='CNY' WHERE id=$1`, `DELETE FROM market_quote_token_prices WHERE quote_id=$1`, `UPDATE market_quote_tiers SET name='changed' WHERE quote_id=$1`, `DELETE FROM market_quote_tier_token_prices WHERE quote_id=$1`, `DELETE FROM market_quote_unit_prices WHERE quote_id=$1`} {
		if _, err := pool.Exec(ctx, sql, q.ID); err == nil {
			t.Fatalf("mutable quote: %s", sql)
		}
	}
	digest := d.Models[0].CapabilityDigest
	q.ID = marketID(t)
	q.Currency = "CNY"
	q.Prices.Input = mustAmount(t, "3")
	marketApply(t, s, admin.ID, market.Changes{Quotes: []market.Quote{q}})
	d = marketSnapshot(t, s)
	if d.Quotes[0].Version != 2 || d.Models[0].CapabilityDigest != digest {
		t.Fatal("price revision invalidates engine evidence")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_quotes WHERE group_model_id=$1`, q.GroupModelID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("quote history %d: %v", count, err)
	}
	// Header inheritance changes capability dependencies; labels/address order do not.
	g := d.Groups[0]
	g.Name = "新名字"
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}})
	d = marketSnapshot(t, s)
	if d.Models[0].CapabilityDigest != digest {
		t.Fatal("name changed capabilities")
	}
	g = d.Groups[0]
	ua := "Synthetic/2"
	g.Defaults.UserAgent = &ua
	g.Defaults.HeaderRules = nil
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}})
	d = marketSnapshot(t, s)
	if d.Models[0].CapabilityDigest == digest || d.Groups[0].Defaults.HeaderRules != nil {
		t.Fatal("inherited capability change lost")
	}
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}}); !errors.Is(err, market.ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
}

func TestMarketAddressOwnershipAndAtomicRollback(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	c := marketBundle(t, "relay")
	marketApply(t, s, admin.ID, c)
	d := marketSnapshot(t, s)
	second := market.Group{ID: marketID(t), SupplierID: d.Suppliers[0].ID, Name: "第二组", Enabled: true}
	link := market.GroupAddress{ID: marketID(t), GroupID: second.ID, AddressID: d.Addresses[0].ID, SupplierID: second.SupplierID}
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: second}}, GroupAddresses: []market.GroupAddressSet{{GroupID: second.ID, Links: []market.GroupAddress{link}}}})
	original := marketSnapshot(t, s)
	if len(original.GroupAddresses) != 2 {
		t.Fatal("same supplier cannot share address")
	}
	other := marketBundle(t, "relay")
	if err := s.Market.Apply(ctx, admin.ID, other); !errors.Is(err, market.ErrConflict) {
		t.Fatalf("cross supplier canonical address conflict: %v", err)
	}
	after := marketSnapshot(t, s)
	if !reflect.DeepEqual(after, original) {
		t.Fatal("failed bundle partially committed")
	}
	// Cross-owner relationship is rejected even with distinct canonical address.
	other.Addresses[0].Value.BaseURL = "https://other.example"
	marketApply(t, s, admin.ID, other)
	wrong := market.GroupAddress{ID: marketID(t), GroupID: second.ID, AddressID: other.Addresses[0].Value.ID, SupplierID: second.SupplierID}
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{GroupAddresses: []market.GroupAddressSet{{GroupID: second.ID, Links: []market.GroupAddress{wrong}}}}); err == nil {
		t.Fatal("foreign address attached")
	}
	// Removing an entrance retires both links while preserving stable history.
	address := original.Addresses[0]
	now := time.Now().UTC()
	address.DeletedAt = &now
	marketApply(t, s, admin.ID, market.Changes{Addresses: []market.Change[market.Address]{{Value: address, BaseRevision: address.Revision}}})
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_group_addresses WHERE address_id=$1 AND deleted_at IS NULL`, address.ID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("retire links: %d %v", active, err)
	}
}

func TestMarketReferenceRaceAndTypeConflict(t *testing.T) {
	_, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	c := marketBundle(t, "official_api")
	marketApply(t, s, admin.ID, c)
	d := marketSnapshot(t, s)
	groups := make([]market.Group, 2)
	for i := range groups {
		groups[i] = market.Group{ID: marketID(t), SupplierID: d.Suppliers[0].ID, Name: fmt.Sprintf("引用组%d", i), Enabled: true}
		marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: groups[i]}}})
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, g := range groups {
		m := market.GroupModel{ID: marketID(t), GroupID: g.ID, ModelID: d.Models[0].ModelID, ModelType: "text", Enabled: true, IsReference: true}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.Market.Apply(ctx, admin.ID, market.Changes{Models: []market.Change[market.GroupModel]{{Value: m}}})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, market.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("reference race successes=%d conflicts=%d", success, conflict)
	}
	model := market.GroupModel{ID: marketID(t), GroupID: groups[0].ID, ModelID: d.Models[0].ModelID, ModelType: "image", Enabled: true}
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{Models: []market.Change[market.GroupModel]{{Value: model}}}); err == nil {
		t.Fatal("same ID conflicting types accepted")
	}
	supplier := d.Suppliers[0]
	supplier.Kind = "relay"
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: supplier, BaseRevision: supplier.Revision}}}); err == nil {
		t.Fatal("nonofficial reference source accepted")
	}
}

func marketProtectedRows(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var value string
	// Includes non-empty synthetic accounts, balances and platform Keys; JSONB
	// canonical form compares every column, rather than only aggregate balances.
	err := pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
 'accounts',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM accounts x),
 'balances',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM ledger_accounts x),
 'transactions',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM ledger_transactions x),
 'entries',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM ledger_entries x),
 'calls',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM calls x),
 'keys',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM api_keys x))::text`).Scan(&value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMarketBusinessDeletionPreservesProtectedRows(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s, "100")
	ctx := context.Background()
	c := marketBundle(t, "relay")
	marketApply(t, s, admin.ID, c)
	d := marketSnapshot(t, s)
	// An actually suspended channel exercises the old status/reason constraint.
	id := marketID(t)
	sealed, err := upstreamKeyringOf(t, "v1", "v1").Encrypt(id, 1, "synthetic-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channels(id,owner_id,name,api_key_ciphertext,api_key_nonce,api_key_key_id,market_group_id,status,suspended_reason) VALUES($1,$2,'合成渠道',$3,$4,$5,$6,'suspended','合成暂停')`, id, admin.ID, sealed.Ciphertext, sealed.Nonce, sealed.KeyID, d.Groups[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ledger.Adjust(ctx, ledger.Adjustment{ActorID: admin.ID, AccountID: admin.ID, Amount: mustAmount(t, "25"), Reason: "合成保全样本", IdempotencyKey: "market-preserve"}); err != nil {
		t.Fatal(err)
	}
	keyService := apikey.NewService(s.Keys, upstreamKeyringOf(t, "v1", "v1"), func(context.Context) ([]string, error) { return nil, nil })
	if _, _, err := keyService.Create(ctx, admin.ID, apikey.CreateInput{Name: "合成平台Key", Settings: apikey.Settings{Status: apikey.StatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	before := marketProtectedRows(t, pool)
	now := time.Now().UTC()
	supplier := d.Suppliers[0]
	supplier.DeletedAt = &now
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: supplier, BaseRevision: supplier.Revision}}}); !errors.Is(err, market.ErrHasGroups) {
		t.Fatalf("parent with group deleted: %v", err)
	}
	g := d.Groups[0]
	g.Enabled = false
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}})
	if err := s.Market.Apply(ctx, admin.ID, market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: supplier, BaseRevision: supplier.Revision}}}); !errors.Is(err, market.ErrHasGroups) {
		t.Fatalf("disabled group bypassed deletion guard: %v", err)
	}
	d = marketSnapshot(t, s)
	g = d.Groups[0]
	g.DeletedAt = &now
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}})
	marketApply(t, s, admin.ID, market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: supplier, BaseRevision: supplier.Revision}}})
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND status='unlisted' AND suspended_reason IS NULL FROM channels WHERE id=$1`, id).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("channel cascade: %v %v", deleted, err)
	}
	after := marketProtectedRows(t, pool)
	if after != before {
		t.Fatal("market cleanup changed protected account/financial/key rows")
	}
	d = marketSnapshot(t, s)
	if len(d.Suppliers) != 0 || len(d.Groups) != 0 || len(d.Models) != 0 || len(d.GroupAddresses) != 0 {
		t.Fatalf("deleted entries still active: %+v", d)
	}
	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_object_versions WHERE target_id=$1`, c.Models[0].Value.ID).Scan(&versions); err != nil || versions < 2 {
		t.Fatalf("model history lost: %d %v", versions, err)
	}
	// No physical delete path may destroy configuration provenance.
	if _, err := pool.Exec(ctx, `DELETE FROM market_suppliers WHERE id=$1`, supplier.ID); err == nil {
		t.Fatal("hard delete accepted")
	}
}
