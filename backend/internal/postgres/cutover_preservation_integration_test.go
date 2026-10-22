package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/keyringcheck"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/cutoverpg"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestCutoverReadOnlyEvidenceProtectsIndividualBalancesAndLegacyCiphertext(t *testing.T) {
	pool, store := isolatedDatabase(t)
	_, _, members := accountsOn(t, store.Identity, "100", "100")
	ctx := context.Background()
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer db.Close()
	db.SetMaxOpenConns(1)
	reader := cutoverpg.Store{DB: db}
	keyring, err := channel.ParseKeyring("synthetic=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	const cid = "b0000000-0000-4000-8000-000000000061"
	credential, err := keyring.Encrypt(cid, 1, "synthetic-upstream-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channels(id,owner_id,name,api_key_ciphertext,api_key_nonce,api_key_key_id,status,deleted_at)
 VALUES($1,$2,'synthetic legacy history',$3,$4,$5,'unlisted',now())`, cid, members[0].ID, credential.Ciphertext, credential.Nonce, credential.KeyID); err != nil {
		t.Fatal(err)
	}
	before, err := reader.Inspect(ctx, nil)
	if err != nil {
		t.Fatal("initial evidence", err)
	}
	if _, err = before.SHA256(); err != nil {
		t.Fatal("manifest incomplete", err)
	}
	// Old connection configuration may change; the original encrypted bytes,
	// owner/ID/name and every protected table must remain byte-for-byte intact.
	if _, err = pool.Exec(ctx, `UPDATE channels SET summary='retired connection',status='unlisted',deleted_at=now(),updated_at=now() WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	after, err := reader.Inspect(ctx, &before)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := cutover.Compare(before, after)
	if err != nil || len(changes) != 0 {
		t.Fatal("authorized config edit touched protected data", changes, err)
	}
	encrypted := channel.EncryptedCredential{Version: 1}
	if err = pool.QueryRow(ctx, `SELECT api_key_ciphertext,api_key_nonce,api_key_key_id FROM channels WHERE id=$1`, cid).Scan(&encrypted.Ciphertext, &encrypted.Nonce, &encrypted.KeyID); err != nil {
		t.Fatal(err)
	}
	secret, err := keyring.OpenCredential(cid, encrypted)
	if err != nil || secret != "synthetic-upstream-key" {
		t.Fatal("historical AAD/ciphertext lost", err)
	}
	historical, err := store.ChannelHistory.CredentialsAfter(ctx, "", 500)
	if err != nil || len(historical) != 1 || historical[0].RecordID != cid || historical[0].Sealed.Version != 1 {
		t.Fatal("deleted history disappeared from startup verification", err)
	}
	reopened, err := keyring.Decrypt(historical[0].RecordID, historical[0].Sealed)
	if err != nil || reopened != "synthetic-upstream-key" {
		t.Fatal("historical AAD changed", err)
	}
	if rest, err := store.ChannelHistory.CredentialsAfter(ctx, cid, 500); err != nil || len(rest) != 0 {
		t.Fatal("history cursor repeated rows", err)
	}
	sources := keyringcheck.Sources{Channels: store.ChannelHistory, APIKeys: store.Keys, C2COrders: store.C2C}
	private := privateKeyringOf(t, "private", "private")
	verified, err := keyringcheck.Verify(ctx, sources, keyringcheck.Keyrings{Upstream: keyring, C2C: private})
	if err != nil || verified.Channels != 1 {
		t.Fatal("startup did not verify preserved history", verified.Channels, err)
	}
	if _, err = keyringcheck.Verify(ctx, sources, keyringcheck.Keyrings{Upstream: upstreamKeyringOf(t, "new", "new"), C2C: private}); err == nil {
		t.Fatal("startup accepted a keyring missing a retained historical key ID")
	}
	// Deliberately corrupt only an isolated synthetic fixture while keeping
	// total balance zero. A row-count/total-only checker would miss this.
	if _, err = pool.Exec(ctx, `UPDATE ledger_accounts SET balance_nano=CASE WHEN account_id=$1 THEN 1 ELSE -1 END WHERE account_id IN ($1,$2)`, members[0].ID, members[1].ID); err != nil {
		t.Fatal(err)
	}
	_, err = reader.Inspect(ctx, &before)
	if !errors.Is(err, cutover.ErrIntegrity) {
		t.Fatal("zero-sum account mutation was concealed", err)
	}
	// Repair only this deliberately corrupted synthetic fixture before the
	// independent pending-work check. This is never a maintenance operation.
	if _, err = pool.Exec(ctx, `UPDATE ledger_accounts SET balance_nano=0 WHERE account_id IN ($1,$2)`, members[0].ID, members[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Inspect(ctx, &before); !errors.Is(err, cutover.ErrIntegrity) {
		t.Fatal("disabled ledger guard accepted", err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE calls DROP CONSTRAINT calls_ledger_tx_fk`); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Inspect(ctx, &before); !errors.Is(err, cutover.ErrIncomplete) {
		t.Fatal("missing historical ledger FK accepted with unchanged rows", err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE calls ADD CONSTRAINT calls_ledger_tx_fk FOREIGN KEY(ledger_tx_id) REFERENCES ledger_transactions(id)`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO calls(account_id,format,stream,outcome)VALUES($1,'openai_chat',false,'in_progress')`, members[0].ID); err != nil {
		t.Fatal(err)
	}
	blocked, err := reader.Inspect(ctx, nil)
	if !errors.Is(err, cutover.ErrNotDrained) || blocked.Pending.Calls != 1 || len(blocked.Tables) != 0 {
		t.Fatal("unfinished calls produced completed evidence", blocked.Pending, err)
	}
	// This isolated fixture has no live gateway or actual request to interrupt.
	if _, err = pool.Exec(ctx, `UPDATE calls SET outcome='upstream_failed' WHERE outcome='in_progress'`); err != nil {
		t.Fatal(err)
	}
}
