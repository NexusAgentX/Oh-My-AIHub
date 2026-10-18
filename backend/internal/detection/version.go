package detection

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

// The digest follows detector source changes even in container builds without
// .git metadata; UI/docs-only commits do not invalidate upstream evidence.
//
//go:embed *.go
var engineSource embed.FS

var engineVersion = func() string {
	h := sha256.New()
	entries, _ := engineSource.ReadDir(".")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, _ := engineSource.ReadFile(entry.Name())
		h.Write([]byte(entry.Name()))
		h.Write([]byte{0})
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}()
