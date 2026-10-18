// cutover-check produces or verifies read-only preservation evidence. It has no
// migration, cleanup, deployment or authorization option.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/cutoverpg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type inspector interface {
	Inspect(context.Context, *cutover.Manifest) (cutover.Manifest, error)
}
type opener func(context.Context, string) (inspector, func(), error)

func open(ctx context.Context, dsn string) (inspector, func(), error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, errors.New("invalid cutover database configuration")
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, nil, errors.New("cannot connect to cutover database")
	}
	return cutoverpg.Store{DB: db}, func() { _ = db.Close() }, nil
}

func run(ctx context.Context, args []string, dsn string, out io.Writer, connect opener) error {
	flags := flag.NewFlagSet("cutover-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	baseline := flags.String("baseline", "", "baseline manifest to verify")
	expected := flags.String("sha256", "", "independently retained baseline digest")
	destination := flags.String("out", "", "new baseline file, created exclusively with mode 0600")
	if flags.Parse(args) != nil || flags.NArg() != 0 || dsn == "" {
		return errors.New("set AIHUB_CUTOVER_DATABASE_URL and use --out PATH, or --baseline PATH --sha256 DIGEST")
	}
	inspect := *baseline == "" && *expected == "" && *destination != ""
	verify := *baseline != "" && *expected != "" && *destination == ""
	if !inspect && !verify {
		return errors.New("choose exactly one evidence operation")
	}
	var before *cutover.Manifest
	if verify {
		f, err := os.Open(*baseline)
		if err != nil {
			return errors.New("cannot open baseline evidence")
		}
		m, err := cutover.ReadEvidence(f, *expected)
		_ = f.Close()
		if err != nil {
			return err
		}
		before = &m
	}
	reader, closeDB, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer closeDB()
	after, err := reader.Inspect(ctx, before)
	if err != nil {
		return err
	}
	digest, err := after.SHA256()
	if err != nil {
		return err
	}
	if verify {
		differences, err := cutover.Compare(*before, after)
		if err != nil {
			return err
		}
		result := struct {
			Matches          bool                 `json:"matches"`
			MigrationVersion int64                `json:"migration_version"`
			Differences      []cutover.Difference `json:"differences"`
		}{len(differences) == 0, after.MigrationVersion, differences}
		if err = json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
		if !result.Matches {
			return errors.New("protected data or ledger guards changed; keep ingress stopped and investigate")
		}
		return nil
	}
	// O_EXCL refuses to overwrite pre-cutover evidence, even on a retry.
	file, err := os.OpenFile(*destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create a new baseline evidence file")
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(*destination)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(after); err != nil {
		return errors.New("cannot write baseline evidence")
	}
	if err = file.Sync(); err != nil {
		return errors.New("cannot persist baseline evidence")
	}
	if err = file.Close(); err != nil {
		return errors.New("cannot close baseline evidence")
	}
	success = true
	_, err = fmt.Fprintln(out, digest)
	return err
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv("AIHUB_CUTOVER_DATABASE_URL"), os.Stdout, open); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
