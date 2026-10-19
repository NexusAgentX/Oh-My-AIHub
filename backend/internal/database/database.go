package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return openPool(ctx, databaseURL, 20)
}

// OpenLimited gives bounded background work its own connection slots.
func OpenLimited(ctx context.Context, databaseURL string, maxConnections int32) (*pgxpool.Pool, error) {
	if maxConnections < 1 || maxConnections > 20 {
		return nil, fmt.Errorf("invalid background connection limit")
	}
	return openPool(ctx, databaseURL, maxConnections)
}
func openPool(ctx context.Context, databaseURL string, maxConnections int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	config.MaxConns = maxConnections
	config.MinConns = min(2, maxConnections)
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 15 * time.Minute
	config.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, databaseURL string) error {
	return MigrateWithOptions(ctx, databaseURL, MigrationOptions{})
}

func MigrateWithOptions(ctx context.Context, databaseURL string, options MigrationOptions) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer db.Close()
	// One session retains the explicit authorization setting for Goose. A
	// replacement connection loses it and the SQL guard fails closed.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}
	if err := migrationPreflight(ctx, db, options); err != nil {
		return fmt.Errorf("migration preflight: %w", err)
	}
	if err := configureCleanup(ctx, db, options); err != nil {
		return err
	}
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		return fmt.Errorf("configure migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}
