package main

import (
	"log"
	"net/http"
	"os"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: api.NewHandler(),
	}

	log.Printf("backend listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
