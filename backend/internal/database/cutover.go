package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
)

const legacyCleanupVersion int64 = 22

var ErrCleanupAuthorization = errors.New("legacy configuration cleanup requires an explicitly selected cutover environment")

type MigrationOptions struct {
	// Set only for the environment the operator has chosen for final cutover.
	// It never permits modifying users, balances, ledger or historical calls.
	AllowLegacyConfigCleanup bool
}

// Check before Goose initializes its version table or applies any intermediate
// migration. The final SQL migration independently locks and rechecks the data.
func migrationPreflight(ctx context.Context, db *sql.DB, options MigrationOptions) error {
	var versions bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&versions); err != nil {
		return err
	}
	var version int64
	if versions {
		if err := db.QueryRowContext(ctx, "SELECT coalesce(max(version_id),0) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
			return err
		}
	}
	if version >= legacyCleanupVersion {
		return nil
	}
	for _, table := range []string{"calls"} {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		var pending bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE outcome='in_progress')").Scan(&pending); err != nil {
			return err
		}
		if pending {
			return cutover.ErrNotDrained
		}
	}
	if options.AllowLegacyConfigCleanup {
		return nil
	}
	for _, table := range []string{"models", "model_price_tiers", "channel_models", "suppliers", "supplier_endpoints", "channels"} {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		condition := ""
		if table == "channels" {
			var grouped bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='channels' AND column_name='market_group_id')").Scan(&grouped); err != nil {
				return err
			}
			if grouped {
				condition = " WHERE market_group_id IS NULL"
			}
		}
		var present bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+condition+")").Scan(&present); err != nil {
			return err
		}
		if present {
			return ErrCleanupAuthorization
		}
	}
	return nil
}

func configureCleanup(ctx context.Context, db *sql.DB, options MigrationOptions) error {
	value := "off"
	if options.AllowLegacyConfigCleanup {
		value = "on"
	}
	if _, err := db.ExecContext(ctx, "SELECT set_config('aihub.allow_legacy_config_cleanup',$1,false)", value); err != nil {
		return fmt.Errorf("configure cutover authorization: %w", err)
	}
	return nil
}
