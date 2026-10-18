package cutoverpg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
)

// Only catalog definitions and aggregate violation counts leave these queries.
// No account IDs, balances, request content, ciphertext or credentials are emitted.
func integrity(ctx context.Context, tx *sql.Tx) ([]cutover.Guard, []cutover.Check, error) {
	guards := []cutover.Guard{}
	rows, err := tx.QueryContext(ctx, `SELECT t.tgname,t.tgenabled::text,pg_get_triggerdef(t.oid,true)
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND NOT t.tgisinternal
 AND (c.relname='ledger_entries' AND t.tgname IN ('ledger_entries_immutable','ledger_transaction_balance')
 OR c.relname='ledger_transactions' AND t.tgname='ledger_transactions_immutable')
 UNION ALL
 SELECT p.proname,''::text,pg_get_functiondef(p.oid)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() AND p.proname IN ('reject_ledger_mutation','verify_ledger_transaction_balance') AND p.pronargs=0
 ORDER BY 1`)
	if err != nil {
		return nil, nil, cutover.ErrIncomplete
	}
	for rows.Next() {
		var g cutover.Guard
		var definition string
		if rows.Scan(&g.Name, &g.Enabled, &definition) != nil {
			_ = rows.Close()
			return nil, nil, cutover.ErrIncomplete
		}
		sum := sha256.Sum256([]byte(definition))
		g.SHA256 = hex.EncodeToString(sum[:])
		guards = append(guards, g)
	}
	streamErr := rows.Err()
	_ = rows.Close()
	if streamErr != nil {
		return nil, nil, cutover.ErrIncomplete
	}
	queries := []string{
		`SELECT CASE WHEN coalesce(sum(balance_nano::numeric),0)=0 THEN 0 ELSE 1 END FROM ledger_accounts`,
		`SELECT count(*) FROM (SELECT a.id FROM ledger_accounts a LEFT JOIN ledger_entries e ON e.ledger_account_id=a.id GROUP BY a.id,a.balance_nano HAVING a.balance_nano::numeric<>coalesce(sum(e.amount_nano::numeric),0)) x`,
		`SELECT count(*) FROM (SELECT t.id FROM ledger_transactions t LEFT JOIN ledger_entries e ON e.transaction_id=t.id GROUP BY t.id HAVING count(e.id)<2 OR coalesce(sum(e.amount_nano::numeric),0)<>0) x`,
		`SELECT count(*) FROM (SELECT balance_after_nano,sum(amount_nano::numeric) OVER(PARTITION BY ledger_account_id ORDER BY id ROWS UNBOUNDED PRECEDING) expected FROM ledger_entries) e WHERE balance_after_nano::numeric<>expected`,
		`SELECT CASE WHEN (SELECT balance_nano::numeric FROM ledger_accounts WHERE system_code='c2c_escrow')=(SELECT coalesce(sum(available_nano::numeric+in_trade_nano::numeric),0) FROM c2c_orders) THEN 0 ELSE 1 END`,
		`SELECT count(*) FROM (SELECT o.id,o.in_trade_nano FROM c2c_orders o LEFT JOIN c2c_trades t ON t.order_id=o.id AND t.status IN ('awaiting_payment','paid','disputed') GROUP BY o.id,o.in_trade_nano HAVING o.in_trade_nano::numeric<>coalesce(sum(t.amount_nano::numeric),0)) x`,
		`SELECT count(*) FROM calls c JOIN channels ch ON ch.id=c.final_channel_id WHERE c.ledger_tx_id IS NULL AND c.outcome IN ('succeeded','interrupted','client_disconnected') AND c.cost_nano::numeric+c.fee_nano::numeric>0 AND ch.owner_id<>c.account_id`,
		`SELECT count(*) FROM c2c_trades WHERE status IN ('released','resolved_to_buyer') AND ledger_tx_id IS NULL`,
	}
	checks := make([]cutover.Check, 0, len(queries))
	for i, q := range queries {
		c := cutover.Check{Name: cutover.CheckNames[i]}
		if tx.QueryRowContext(ctx, q).Scan(&c.Violations) != nil {
			return nil, nil, cutover.ErrIncomplete
		}
		checks = append(checks, c)
	}
	return guards, checks, nil
}
