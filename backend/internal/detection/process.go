package detection

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var processSlots = make(chan struct{}, 2)
var errClientLimit = errors.New("controlled client resource limit")

type ProcessSpec struct {
	Executable string
	Args       []string
	Env        map[string]string
	Files      map[string][]byte
	Timeout    time.Duration
}
type ProcessOutput struct {
	Stdout   []byte `json:"-"`
	Stderr   []byte `json:"-"`
	ExitCode int
	Reason   string
}
type boundedOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buffer.Len()+len(data) > 1<<20 {
		b.overflow = true
		b.cancel()
		return 0, errClientLimit
	}
	return b.buffer.Write(data)
}

// RunControlledProcess is for fixed, source-verified CLI adapters, never an
// HTTP-supplied command. The child has a fresh HOME, no inherited login/proxy
// environment, bounded output/files and process-group cancellation. Raw output
// exists only in memory and must be parsed into fixed assertion metadata.
func RunControlledProcess(ctx context.Context, spec ProcessSpec) (ProcessOutput, error) {
	out := ProcessOutput{ExitCode: -1, Reason: "invalid_request"}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if !processSupported() || !filepath.IsAbs(spec.Executable) || spec.Timeout < time.Second || spec.Timeout > 5*time.Minute || len(spec.Args) > 64 || len(spec.Env) > 32 || len(spec.Files) > 16 {
		return out, ErrInvalid
	}
	argumentBytes := 0
	for _, arg := range spec.Args {
		argumentBytes += len(arg)
	}
	if argumentBytes > 64<<10 {
		return out, ErrInvalid
	}
	for name, value := range spec.Env {
		if name == "" || len(name) > 64 || len(value) > 16384 || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) || strings.ContainsAny(name, "abcdefghijklmnopqrstuvwxyz") {
			return out, ErrInvalid
		}
	}
	select {
	case processSlots <- struct{}{}:
		defer func() { <-processSlots }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	dir, err := os.MkdirTemp("", "aihub-detection-client-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	if err = os.MkdirAll(filepath.Join(dir, "codex"), 0700); err != nil {
		return out, err
	}
	total := 0
	for name, body := range spec.Files {
		clean := filepath.Clean(name)
		if name == "" || filepath.IsAbs(name) || clean != name || strings.HasPrefix(clean, "..") || strings.ContainsRune(name, 0) || len(body) > 2<<20 {
			return out, ErrInvalid
		}
		total += len(body)
		if total > 8<<20 {
			return out, ErrInvalid
		}
		path := filepath.Join(dir, clean)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return out, err
		}
		if err = os.WriteFile(path, body, 0600); err != nil {
			return out, err
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	stdout, stderr := &boundedOutput{cancel: cancel}, &boundedOutput{cancel: cancel}
	// Shell text is constant. Executable and arguments remain separate argv
	// entries, so neither prompts nor filenames are interpreted as shell code.
	args := append([]string{"-c", `ulimit -v 1048576; ulimit -f 16384; ulimit -n 64; exec "$@"`, "aihub-client", spec.Executable}, spec.Args...)
	cmd := exec.Command("/bin/sh", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	env := map[string]string{"PATH": "/usr/bin:/bin", "NO_COLOR": "1", "CI": "1"}
	for k, v := range spec.Env {
		env[k] = v
	}
	if err = os.MkdirAll(filepath.Join(dir, "tmp"), 0700); err != nil {
		return out, err
	}
	env["TMPDIR"] = filepath.Join(dir, "tmp")
	env["TMP"] = env["TMPDIR"]
	env["TEMP"] = env["TMPDIR"]
	env["HOME"] = dir
	env["CODEX_HOME"] = filepath.Join(dir, "codex")
	env["XDG_CONFIG_HOME"] = filepath.Join(dir, "config")
	env["XDG_CACHE_HOME"] = filepath.Join(dir, "cache")
	env["XDG_DATA_HOME"] = filepath.Join(dir, "data")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	configureProcess(cmd)
	if runCtx.Err() != nil {
		return out, runCtx.Err()
	}
	if err = cmd.Start(); err != nil {
		return out, err
	}
	stopped := make(chan struct{})
	watchDone := make(chan struct{})
	var limitMu sync.Mutex
	diskLimit := false
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-runCtx.Done():
				killProcessGroup(cmd)
				return
			case <-ticker.C:
				count, size := 0, int64(0)
				walkErr := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					count++
					if count > 2048 {
						return errClientLimit
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if !entry.IsDir() {
						size += info.Size()
					}
					if size > 16<<20 {
						return errClientLimit
					}
					return nil
				})
				if walkErr != nil {
					limitMu.Lock()
					diskLimit = true
					limitMu.Unlock()
					cancel()
					killProcessGroup(cmd)
					return
				}
			}
		}
	}()
	err = cmd.Wait()
	close(stopped)
	<-watchDone
	out.ExitCode = cmd.ProcessState.ExitCode()
	out.Stdout = bytes.Clone(stdout.buffer.Bytes())
	out.Stderr = bytes.Clone(stderr.buffer.Bytes())
	out.Reason = "ok"
	limitMu.Lock()
	diskExceeded := diskLimit
	limitMu.Unlock()
	if stdout.overflow || stderr.overflow || diskExceeded {
		out.Reason = "body_limit"
		return out, errClientLimit
	}
	if runCtx.Err() != nil {
		out.Reason = "timeout"
		if ctx.Err() != nil {
			out.Reason = "cancelled"
		}
		return out, runCtx.Err()
	}
	if err != nil {
		out.Reason = "client_disabled"
	}
	return out, err
}
