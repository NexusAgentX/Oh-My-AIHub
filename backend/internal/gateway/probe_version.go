package gateway

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
)

//go:embed *.go
var probeSource embed.FS

var probeImplementationVersion = func() string {
	h := sha256.New()
	entries, _ := probeSource.ReadDir(".")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, _ := probeSource.ReadFile(entry.Name())
		h.Write([]byte(entry.Name()))
		h.Write([]byte{0})
		h.Write(body)
	}
	h.Write([]byte(gatewaycontract.ImplementationVersion()))
	h.Write([]byte(nativeencoding.ImplementationVersion()))
	return hex.EncodeToString(h.Sum(nil))
}()

// ProbeImplementationVersion invalidates gateway-layer evidence when the
// actual forwarding/auth/settlement implementation changes. It is read only
// during detector registration, never per request or per SSE frame.
func ProbeImplementationVersion() string { return probeImplementationVersion }
