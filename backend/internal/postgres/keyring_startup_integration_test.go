package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/keyringcheck"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// keyringSpec builds "id=base64-key" entries whose key material depends only on
// the ID and the keyring family, so the same ID is the same key everywhere.
func keyringSpec(family string, ids ...string) string {
	entries := make([]string, len(ids))
	for index, id := range ids {
		sum := sha256.Sum256([]byte(family + "/" + id))
		entries[index] = id + "=" + base64.StdEncoding.EncodeToString(sum[:])
	}
	return strings.Join(entries, ",")
}

func upstreamKeyringOf(t *testing.T, active string, ids ...string) *channel.Keyring {
	t.Helper()
	keyring, err := channel.ParseKeyring(keyringSpec("upstream", ids...), active)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func privateKeyringOf(t *testing.T, active string, ids ...string) *c2c.Keyring {
	t.Helper()
	keyring, err := c2c.ParseKeyring(keyringSpec("private", ids...), active)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

// secretFixture writes channels, platform API keys and C2C orders through the
// real services, sealed under whichever keyrings a call passes in.
type secretFixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	store  *storepg.Store
	admin  identity.Account
	seller identity.AdminAccount
	funded int
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	pool, store := isolatedDatabase(t)
	_, admin, members := accounts(t, store, "100")
	return &secretFixture{t: t, pool: pool, store: store, admin: admin, seller: members[0]}
}

const (
	testUpstreamSecret = "sk-upstream-integration-secret"
	testPaymentAccount = "seller@example.com"
)

func (f *secretFixture) channel(keyring *channel.Keyring, name string, status channel.Status) string {
	f.t.Helper()
	service := channel.NewService(channel.Dependencies{
		Store: f.store.Channels, Keyring: keyring, Outbound: testOutbound{client: &http.Client{}},
		KnownModels:  func(context.Context) ([]string, error) { return nil, nil },
		BlockedHosts: func(context.Context) ([]string, error) { return nil, nil },
	})
	created, err := service.Create(context.Background(), f.seller.ID, channel.CreateInput{
		Name: name, BaseURL: "https://relay.example.com", APIKey: testUpstreamSecret + "-" + name, Status: status,
		Models: []channel.Model{{
			ModelID: "foo-1", MultiplierNano: money.Scale, Formats: []channel.Format{channel.FormatOpenAIChat},
			Custom: &channel.CustomPrices{Input: mustAmount(f.t, "1"), Output: mustAmount(f.t, "1")},
		}},
	})
	if err != nil {
		f.t.Fatalf("create channel %s: %v", name, err)
	}
	return created.ID
}

func (f *secretFixture) deleteChannel(id string) {
	f.t.Helper()
	if err := f.store.Channels.SoftDelete(context.Background(), id); err != nil {
		f.t.Fatalf("delete channel: %v", err)
	}
}

func (f *secretFixture) apiKey(keyring *channel.Keyring, name string, status apikey.Status) (id, secret string) {
	f.t.Helper()
	service := apikey.NewService(f.store.Keys, keyring, func(context.Context) ([]string, error) { return nil, nil })
	key, secret, err := service.Create(context.Background(), f.seller.ID, apikey.CreateInput{Name: name, Settings: apikey.Settings{Status: status}})
	if err != nil {
		f.t.Fatalf("create api key %s: %v", name, err)
	}
	return key.ID, secret
}

func (f *secretFixture) deleteAPIKey(id string) {
	f.t.Helper()
	if err := f.store.Keys.Delete(context.Background(), f.seller.ID, id); err != nil {
		f.t.Fatalf("delete api key: %v", err)
	}
}

func (f *secretFixture) order(keyring *c2c.Keyring) string {
	f.t.Helper()
	ctx := context.Background()
	f.funded++
	if _, err := f.store.Ledger.Adjust(ctx, ledger.Adjustment{
		ActorID: f.admin.ID, AccountID: f.seller.ID, Amount: mustAmount(f.t, "1"), Reason: "充值", IdempotencyKey: fmt.Sprintf("fund-%d", f.funded),
	}); err != nil {
		f.t.Fatalf("fund: %v", err)
	}
	minimum := mustAmount(f.t, "1")
	created, err := c2c.NewService(f.store.C2C, keyring).CreateOrder(ctx, c2c.CreateOrderInput{
		SellerID: f.seller.ID, Amount: mustAmount(f.t, "1"), UnitPriceFen: 100, MinPerTrade: &minimum,
		PaymentMethods: []c2c.PaymentMethod{{Channel: "支付宝", Account: testPaymentAccount}},
	})
	if err != nil {
		f.t.Fatalf("create order: %v", err)
	}
	return created.ID
}

func (f *secretFixture) closeOrder(keyring *c2c.Keyring, id string) {
	f.t.Helper()
	if _, err := c2c.NewService(f.store.C2C, keyring).CloseOrder(context.Background(), id, f.seller.ID); err != nil {
		f.t.Fatalf("close order: %v", err)
	}
}

func (f *secretFixture) verify(upstream *channel.Keyring, private *c2c.Keyring) (keyringcheck.Report, error) {
	return keyringcheck.Verify(context.Background(),
		keyringcheck.Sources{Channels: f.store.Channels, APIKeys: f.store.Keys, C2COrders: f.store.C2C},
		keyringcheck.Keyrings{Upstream: upstream, C2C: private})
}

func TestKeyringCheckReadsEverySecretStillInUse(t *testing.T) {
	f := newSecretFixture(t)
	old, current := upstreamKeyringOf(t, "v1", "v1"), upstreamKeyringOf(t, "v2", "v1", "v2")
	oldPrivate, currentPrivate := privateKeyringOf(t, "c1", "c1"), privateKeyringOf(t, "c2", "c1", "c2")

	// Written under the old keys: a listed and an unlisted channel, an enabled and
	// a disabled platform key, an open and a closed order. Channels and keys
	// that were deleted again are never decrypted, so they must not count.
	live := f.channel(old, "live", channel.StatusListed)
	unlisted := f.channel(old, "unlisted", channel.StatusUnlisted)
	deletedChannel := f.channel(old, "deleted", channel.StatusListed)
	f.deleteChannel(deletedChannel)
	liveKey, liveSecret := f.apiKey(old, "live", apikey.StatusEnabled)
	disabledKey, disabledSecret := f.apiKey(old, "disabled", apikey.StatusDisabled)
	deletedKey, _ := f.apiKey(old, "deleted", apikey.StatusEnabled)
	f.deleteAPIKey(deletedKey)
	openOrder := f.order(oldPrivate)
	closedOrder := f.order(oldPrivate)
	f.closeOrder(oldPrivate, closedOrder)
	// Written under the current keys.
	f.channel(current, "current", channel.StatusListed)
	currentKey, _ := f.apiKey(current, "current", apikey.StatusEnabled)
	currentOrder := f.order(currentPrivate)

	// Both generations of keys are configured: everything opens.
	report, err := f.verify(current, currentPrivate)
	if err != nil {
		t.Fatalf("complete keyrings: %v", err)
	}
	if want := (keyringcheck.Report{Channels: 3, APIKeys: 3, C2COrders: 3}); report != want {
		t.Fatalf("report = %+v, want %+v (deleted channel and key must be skipped)", report, want)
	}

	// The historical keys were left out: exactly the records sealed under them
	// fail, each named by kind and ID, and the message holds no secrets.
	_, err = f.verify(upstreamKeyringOf(t, "v2", "v2"), privateKeyringOf(t, "c2", "c2"))
	var failed *keyringcheck.Error
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *keyringcheck.Error", err)
	}
	var got []string
	for _, failure := range failed.Failures {
		got = append(got, failure.Kind+" "+failure.ID)
	}
	want := []string{
		"channel " + live, "channel " + unlisted, "api_key " + liveKey, "api_key " + disabledKey,
		"c2c_order " + openOrder, "c2c_order " + closedOrder,
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || failed.Total != len(want) {
		t.Fatalf("failures = %v (total %d), want %v", got, failed.Total, want)
	}
	for _, leaked := range []string{testUpstreamSecret, liveSecret, disabledSecret, testPaymentAccount, deletedChannel, deletedKey, currentKey, currentOrder} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("message contains %q: %s", leaked, err.Error())
		}
	}
	if !strings.Contains(err.Error(), `"v1"`) || !strings.Contains(err.Error(), `"c1"`) {
		t.Fatalf("message does not name the missing key IDs: %s", err.Error())
	}

	// Only one of the two keyrings lacks its historical key: only its records fail.
	_, err = f.verify(current, privateKeyringOf(t, "c2", "c2"))
	if !errors.As(err, &failed) || failed.Total != 2 || failed.Failures[0].Kind != keyringcheck.KindC2COrder {
		t.Fatalf("err = %v, want the two old orders only", err)
	}
}

func TestKeyringCheckSourcesPageInIDOrder(t *testing.T) {
	f := newSecretFixture(t)
	upstream, private := upstreamKeyringOf(t, "v1", "v1"), privateKeyringOf(t, "c1", "c1")
	for index := 0; index < 5; index++ {
		f.channel(upstream, fmt.Sprintf("channel-%d", index), channel.StatusListed)
		f.apiKey(upstream, fmt.Sprintf("key-%d", index), apikey.StatusEnabled)
		f.order(private)
	}
	deleted := f.channel(upstream, "deleted", channel.StatusListed)
	f.deleteChannel(deleted)

	ctx := context.Background()
	for _, source := range []struct {
		name  string
		query string
		page  func(afterID string, limit int) ([]string, error)
	}{
		{"channels", `SELECT id FROM channels WHERE deleted_at IS NULL ORDER BY id`, func(afterID string, limit int) ([]string, error) {
			rows, err := f.store.Channels.CredentialsAfter(ctx, afterID, limit)
			ids := make([]string, len(rows))
			for index, row := range rows {
				ids[index] = row.RecordID
			}
			return ids, err
		}},
		{"api keys", `SELECT id FROM api_keys WHERE deleted_at IS NULL ORDER BY id`, func(afterID string, limit int) ([]string, error) {
			rows, err := f.store.Keys.CredentialsAfter(ctx, afterID, limit)
			ids := make([]string, len(rows))
			for index, row := range rows {
				ids[index] = row.RecordID
			}
			return ids, err
		}},
		{"orders", `SELECT id FROM c2c_orders ORDER BY id`, func(afterID string, limit int) ([]string, error) {
			rows, err := f.store.C2C.OrderMethodsAfter(ctx, afterID, limit)
			ids := make([]string, len(rows))
			for index, row := range rows {
				ids[index] = row.OrderID
			}
			return ids, err
		}},
	} {
		rows, err := f.pool.Query(ctx, source.query)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want = append(want, id)
		}
		rows.Close()
		if len(want) != 5 {
			t.Fatalf("%s: %d rows stored, want 5", source.name, len(want))
		}

		var got []string
		afterID := ""
		var sizes []int
		for {
			page, err := source.page(afterID, 2)
			if err != nil {
				t.Fatalf("%s: %v", source.name, err)
			}
			sizes = append(sizes, len(page))
			got = append(got, page...)
			if len(page) < 2 {
				break
			}
			afterID = page[len(page)-1]
		}
		if !slices.Equal(got, want) || !slices.Equal(sizes, []int{2, 2, 1}) {
			t.Fatalf("%s: paged %v in pages %v, want %v", source.name, got, sizes, want)
		}
	}
}
