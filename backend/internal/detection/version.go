package detection

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

// The digest follows detector source changes even in container builds without
// .git metadata; UI/docs-only commits do not invalidate upstream evidence.
//
//go:embed *.go
var engineSource embed.FS

var engineVersion = implementationDigest(engineSource, "")
var nativeEngineVersion = implementationDigest(engineSource, "upstream_protocol")

func implementationDigest(source fs.FS, layer string) string {
	h := sha256.New()
	h.Write([]byte("detector-source.v2\x00" + layer + "\x00"))
	entries, _ := fs.ReadDir(source, ".")
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// The raw protocol runners do not use the fixed CLI or its private
		// production-gateway bridge. Keep all unknown/new files by default:
		// classification mistakes fail closed instead of retaining stale proof.
		if layer == "upstream_protocol" && (strings.HasPrefix(name, "codex_cli") || strings.HasPrefix(name, "codex_bridge") || name == "codex_gateway.go" || name == "codex_activity.go" || name == "codex_tls.go") {
			continue
		}
		body, _ := fs.ReadFile(source, name)
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(body)
	}
	h.Write([]byte(gatewaycontract.ImplementationVersion()))
	h.Write([]byte(nativeencoding.ImplementationVersion()))
	return hex.EncodeToString(h.Sum(nil))
}
