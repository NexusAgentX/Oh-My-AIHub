package detection

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestControlledClientAddressProfilesRemainHardBounded(t *testing.T) {
	if !processSupported() {
		t.Skip("controlled process requires Linux/macOS")
	}
	for _, profile := range []int{0, 1024, 2048} {
		output, err := RunControlledProcess(context.Background(), ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", "ulimit -v"}, Timeout: time.Second, VirtualMemoryMiB: profile})
		want := "1048576"
		if profile == 2048 {
			want = "2097152"
		}
		if err != nil || runtime.GOOS == "linux" && strings.TrimSpace(string(output.Stdout)) != want {
			t.Fatal("address limit was omitted or altered", profile, err, string(output.Stdout))
		}
	}
	for _, invalid := range []int{-1, 256, 8192} {
		if _, err := RunControlledProcess(context.Background(), ProcessSpec{Executable: "/bin/sh", Timeout: time.Second, VirtualMemoryMiB: invalid}); err == nil {
			t.Fatal("arbitrary/unbounded address profile accepted", invalid)
		}
	}
}
