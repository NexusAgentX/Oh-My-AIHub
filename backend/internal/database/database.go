package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
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
	return migrate(ctx, databaseURL, 0)
}

func MigrateTo(ctx context.Context, databaseURL string, version int64) error {
	if version < 1 {
		return fmt.Errorf("migration version must be positive")
	}
	return migrate(ctx, databaseURL, version)
}

func migrate(ctx context.Context, databaseURL string, version int64) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure migration dialect: %w", err)
	}
	if version > 0 {
		if err := goose.UpToContext(ctx, db, "migrations", version); err != nil {
			return fmt.Errorf("apply database migrations through version %d: %w", version, err)
		}
		return nil
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}
