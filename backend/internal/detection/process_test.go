package detection

import (
	"context"
	"errors"
	"io/fs"
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
	if err == nil || output.Reason != "body_limit" || output.ResourceFailure != "stdout" || len(output.Stdout) > 1<<20 {
		t.Fatalf("output bound: %d %s %v", len(output.Stdout), output.Reason, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunControlledProcess(ctx, ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: time.Second}); err == nil {
		t.Fatal("cancelled subprocess continued")
	}
}

type transientProcessEntry struct {
	fs.DirEntry
	err error
}

func (e transientProcessEntry) Info() (fs.FileInfo, error) { return nil, e.err }

func TestControlledClientTemporaryTreeSeparatesRemovalErrorsAndLimits(t *testing.T) {
	var budget processTreeBudget
	if err := budget.visit("removed", nil, fs.ErrNotExist); err != nil {
		t.Fatal("removed directory treated as failure", err)
	}
	if err := budget.visit("removed", transientProcessEntry{err: fs.ErrNotExist}, nil); err != nil || budget.count != 0 {
		t.Fatal("removed file consumed budget", err, budget)
	}
	if err := budget.visit("denied", transientProcessEntry{err: fs.ErrPermission}, nil); !errors.Is(err, fs.ErrPermission) || budget.exceeded != "" {
		t.Fatal("storage error treated as a resource limit", err, budget)
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := fs.FileInfoToDirEntry(info)
	budget = processTreeBudget{count: 2048}
	if err := budget.visit(path, entry, nil); !errors.Is(err, errClientLimit) || budget.exceeded != "temporary_entries" {
		t.Fatal("entry limit not enforced", err, budget)
	}
	budget = processTreeBudget{size: 16 << 20}
	if err := budget.visit(path, entry, nil); !errors.Is(err, errClientLimit) || budget.exceeded != "temporary_bytes" {
		t.Fatal("byte limit not enforced", err, budget)
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
