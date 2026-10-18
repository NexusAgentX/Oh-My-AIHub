package nativeencoding

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

//go:embed *.go
var implementationSource embed.FS

var implementationVersion = func() string {
	h := sha256.New()
	entries, _ := implementationSource.ReadDir(".")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, _ := implementationSource.ReadFile(entry.Name())
		h.Write([]byte(entry.Name()))
		h.Write([]byte{0})
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}()

// ImplementationVersion binds detector evidence to these shared protocol
// dependencies. It is computed at startup, never while forwarding a request.
func ImplementationVersion() string { return implementationVersion }
