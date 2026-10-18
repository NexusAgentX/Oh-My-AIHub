// Package cutoverpg reads preservation evidence in one read-only consistent
// transaction. It has no mutation or migration API and emits no row contents.
package cutoverpg

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
	"github.com/jackc/pgx/v5"
)

type Store struct{ DB *sql.DB }

func metadata(ctx context.Context, tx *sql.Tx) ([]cutover.Table, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.relname,a.attname,coalesce(a.attnum=ANY(i.indkey),false)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid
 LEFT JOIN pg_index i ON i.indrelid=c.oid AND i.indisprimary
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND a.attnum>0 AND NOT a.attisdropped
 ORDER BY c.relname,a.attnum`)
	if err != nil {
		return nil, cutover.ErrIncomplete
	}
	defer rows.Close()
	tables := []cutover.Table{}
	for rows.Next() {
		var table, column string
		var key bool
		if rows.Scan(&table, &column, &key) != nil {
			return nil, cutover.ErrIncomplete
		}
		if len(tables) == 0 || tables[len(tables)-1].Name != table {
			tables = append(tables, cutover.Table{Name: table, Columns: []string{}, PrimaryKey: []string{}})
		}
		last := &tables[len(tables)-1]
		last.Columns = append(last.Columns, column)
		if key {
			last.PrimaryKey = append(last.PrimaryKey, column)
		}
	}
	if rows.Err() != nil {
		return nil, cutover.ErrIncomplete
	}
	return tables, nil
}
func existing(tables []cutover.Table, name string) (cutover.Table, bool) {
	for _, v := range tables {
		if v.Name == name {
			return v, true
		}
	}
	return cutover.Table{}, false
}
func quoted(v string) string { return pgx.Identifier{v}.Sanitize() }

// Inspect with a baseline re-reads exactly its protected columns/scopes, so
// additive columns cannot conceal old content or create false differences.
// New tables remain visible in a separate fresh inspection after verification.
func (s Store) Inspect(ctx context.Context, before *cutover.Manifest) (cutover.Manifest, error) {
	out := cutover.Manifest{Format: cutover.Format, Tables: []cutover.Digest{}}
	if s.DB == nil {
		return out, cutover.ErrInvalid
	}
	if before != nil && before.Validate() != nil {
		return out, cutover.ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, cutover.ErrIncomplete
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'UTC'`); err != nil {
		return out, cutover.ErrIncomplete
	}
	tables, err := metadata(ctx, tx)
	if err != nil {
		return out, err
	}
	if _, ok := existing(tables, "goose_db_version"); ok {
		if tx.QueryRowContext(ctx, `SELECT coalesce(max(version_id) FILTER (WHERE is_applied),0) FROM goose_db_version`).Scan(&out.MigrationVersion) != nil {
			return out, cutover.ErrIncomplete
		}
	}
	if _, ok := existing(tables, "calls"); ok {
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM calls WHERE outcome='in_progress'`).Scan(&out.Pending.Calls) != nil {
			return out, cutover.ErrIncomplete
		}
	}
	if _, ok := existing(tables, "gateway_background_jobs"); ok {
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM gateway_background_jobs WHERE settled_at IS NULL`).Scan(&out.Pending.Background) != nil {
			return out, cutover.ErrIncomplete
		}
	}
	if err = out.Pending.Check(); err != nil {
		return out, err
	}
	if _, initialized := existing(tables, "ledger_entries"); initialized {
		out.Guards, out.Checks, err = integrity(ctx, tx)
		if err != nil {
			return out, err
		}
	}
	var rules []cutover.Rule
	if before == nil {
		rules, err = cutover.Plan(tables)
		if err != nil {
			return out, err
		}
	} else {
		for _, d := range before.Tables {
			rules = append(rules, d.Rule)
		}
	}
	channelTable, hasChannel := existing(tables, "channels")
	hasGroupColumn := hasChannel && slices.Contains(channelTable.Columns, "market_group_id")
	for _, rule := range rules {
		table, ok := existing(tables, rule.Table)
		if !ok {
			return out, cutover.ErrIncomplete
		}
		for _, c := range rule.Columns {
			if !slices.Contains(table.Columns, c) {
				return out, cutover.ErrIncomplete
			}
		}
		columns := make([]string, 0, len(rule.Columns))
		for _, c := range rule.Columns {
			columns = append(columns, "t."+quoted(c))
		}
		where := ""
		switch rule.Scope {
		case "all", "channel_identity":
		case "group_channel_configuration":
			if !hasGroupColumn {
				return out, cutover.ErrIncomplete
			}
			where = " WHERE t.market_group_id IS NOT NULL"
		case "group_channel_selections":
			where = " WHERE false"
			if hasGroupColumn {
				where = " WHERE EXISTS(SELECT 1 FROM channels c WHERE c.id=t.channel_id AND c.market_group_id IS NOT NULL)"
			}
		default:
			return out, cutover.ErrInvalid
		}
		order := []string{}
		for _, c := range rule.PrimaryKey {
			if !slices.Contains(rule.Columns, c) {
				return out, cutover.ErrInvalid
			}
			order = append(order, "protected."+quoted(c))
		}
		if len(order) == 0 {
			order = append(order, `to_jsonb(protected)::text COLLATE "C"`)
		}
		query := "SELECT to_jsonb(protected)::text FROM (SELECT " + strings.Join(columns, ",") + " FROM " + quoted(rule.Table) + " t" + where + ") protected ORDER BY " + strings.Join(order, ",")
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return out, cutover.ErrIncomplete
		}
		h := cutover.NewRowHasher()
		for rows.Next() {
			var row []byte
			if rows.Scan(&row) != nil {
				_ = rows.Close()
				return out, cutover.ErrIncomplete
			}
			if h.Add(row) != nil {
				_ = rows.Close()
				return out, cutover.ErrIncomplete
			}
		}
		streamErr := rows.Err()
		_ = rows.Close()
		digest, err := h.Finish(rule, streamErr)
		if err != nil {
			return out, err
		}
		out.Tables = append(out.Tables, digest)
	}
	if tx.Commit() != nil {
		return cutover.Manifest{}, cutover.ErrIncomplete
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}
