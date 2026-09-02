package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		log.Fatal(err)
	}
	log.Print("database migrations complete")
}
