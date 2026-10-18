package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
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
	if _, err = pool.Exec(ctx, `INSERT INTO channels(id,owner_id,name,base_url,api_key_ciphertext,api_key_nonce,api_key_key_id,status)
 VALUES($1,$2,'synthetic legacy history','https://synthetic.example',$3,$4,$5,'unlisted')`, cid, members[0].ID, credential.Ciphertext, credential.Nonce, credential.KeyID); err != nil {
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
	if _, err = pool.Exec(ctx, `UPDATE channels SET base_url='https://replacement.example',status='unlisted',deleted_at=now(),updated_at=now() WHERE id=$1`, cid); err != nil {
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
	if _, err = pool.Exec(ctx, `INSERT INTO gateway_background_jobs(call_id,account_id,key_id,channel_id,response_id,metadata,deadline_at)
 SELECT id,account_id,'b0000000-0000-4000-8000-000000000062',$1,'synthetic-pending','{}',now()+interval '1 minute' FROM calls LIMIT 1`, cid); err != nil {
		t.Fatal(err)
	}
	blocked, err = reader.Inspect(ctx, nil)
	if !errors.Is(err, cutover.ErrNotDrained) || blocked.Pending.Background != 1 || len(blocked.Tables) != 0 {
		t.Fatal("pending background produced completed evidence", blocked.Pending, err)
	}
}
