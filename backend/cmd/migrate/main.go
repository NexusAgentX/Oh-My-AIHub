package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
)

func main() {
	allowCleanup := flag.Bool("allow-legacy-config-cleanup", false, "allow the authorized legacy configuration cleanup in the explicitly selected cutover database")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected migration arguments")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := database.MigrateWithOptions(ctx, databaseURL, database.MigrationOptions{AllowLegacyConfigCleanup: *allowCleanup}); err != nil {
		log.Fatal(err)
	}
	log.Print("database migrations complete")
}
