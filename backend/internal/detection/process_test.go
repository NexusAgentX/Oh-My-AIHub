package detection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlledClientDoesNotInheritHostAndRemovesFiles(t *testing.T) {
	if !processSupported() {
		t.Skip("controlled process requires Linux/macOS")
	}
	output, err := RunControlledProcess(context.Background(), ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", `printf '%s\n' "$HOME"; test -z "$UPSTREAM_CREDENTIAL_KEYRING"; test -f config/settings; printf 'synthetic-client'`}, Files: map[string][]byte{"config/settings": []byte("synthetic-only")}, Timeout: 3 * time.Second})
	if err != nil || output.ExitCode != 0 || !strings.Contains(string(output.Stdout), "synthetic-client") {
		t.Fatalf("controlled client: %+v %v", output, err)
	}
	root := strings.Split(string(output.Stdout), "\n")[0]
	if !filepath.IsAbs(root) {
		t.Fatal("no isolated HOME")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("temporary client profile retained")
	}
	if _, err := RunControlledProcess(context.Background(), ProcessSpec{Executable: "/bin/sh", Files: map[string][]byte{"../escape": []byte("forbidden")}, Timeout: time.Second}); err == nil {
		t.Fatal("file escape accepted")
	}
}
func TestControlledClientBoundsOutputAndTerminatesChildren(t *testing.T) {
	if !processSupported() {
		t.Skip("controlled process requires Linux/macOS")
	}
	output, err := RunControlledProcess(context.Background(), ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", "head -c 2097152 /dev/zero"}, Timeout: 3 * time.Second})
	if err == nil || output.Reason != "body_limit" || len(output.Stdout) > 1<<20 {
		t.Fatalf("output bound: %d %s %v", len(output.Stdout), output.Reason, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunControlledProcess(ctx, ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: time.Second}); err == nil {
		t.Fatal("cancelled subprocess continued")
	}
}
