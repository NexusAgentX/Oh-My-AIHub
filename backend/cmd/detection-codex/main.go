// detection-codex is a fixed adapter for the official CLI, never a shell or an
// arbitrary API-selected executable. Its parent owns the real upstream Key.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	binary := os.Getenv("AIHUB_CODEX_BINARY")
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if codexprobe.CheckVersion(ctx, binary) != nil {
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"protocol": codexprobe.Protocol, "version": codexprobe.Version})
		return
	}
	if len(os.Args) != 2 || os.Args[1] != "--probe" {
		os.Exit(2)
	}
	r, err := codexprobe.Run(ctx, codexprobe.Config{Binary: binary, Origin: os.Getenv("AIHUB_CODEX_BRIDGE_ORIGIN"), Token: os.Getenv("AIHUB_CODEX_BRIDGE_TOKEN"), Model: os.Getenv("AIHUB_CODEX_MODEL"), Profile: os.Getenv("AIHUB_CODEX_PROFILE"), CA: os.Getenv("AIHUB_CODEX_CA"), Tier: os.Getenv("AIHUB_CODEX_TIER")})
	_ = json.NewEncoder(os.Stdout).Encode(r)
	if err != nil {
		// Do not print raw diagnostics or upstream bodies.
		os.Exit(1)
	}
}
