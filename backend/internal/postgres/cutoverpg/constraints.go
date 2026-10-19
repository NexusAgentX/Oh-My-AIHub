package cutoverpg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
)

func constraints(ctx context.Context, tx *sql.Tx) ([]cutover.Constraint, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.relname,c.conname,c.contype::text,coalesce(r.relname,''),
 pg_get_constraintdef(c.oid,true)||'|validated='||c.convalidated::text
 FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
 LEFT JOIN pg_class r ON r.oid=c.confrelid
 WHERE n.nspname=current_schema() AND c.contype IN ('p','f','u') ORDER BY t.relname,c.conname`)
	if err != nil {
		return nil, cutover.ErrIncomplete
	}
	defer rows.Close()
	out := []cutover.Constraint{}
	for rows.Next() {
		var c cutover.Constraint
		var referenced, definition string
		if rows.Scan(&c.Table, &c.Name, &c.Kind, &referenced, &definition) != nil {
			return nil, cutover.ErrIncomplete
		}
		if c.Table == "goose_db_version" || cutover.LegacyConfiguration(c.Table) || cutover.LegacyConfiguration(referenced) {
			continue
		}
		sum := sha256.Sum256([]byte(definition))
		c.SHA256 = hex.EncodeToString(sum[:])
		out = append(out, c)
	}
	if rows.Err() != nil {
		return nil, cutover.ErrIncomplete
	}
	return out, nil
}
