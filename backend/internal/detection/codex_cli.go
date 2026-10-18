package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func codexArtifactDigest(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 || info.Size() > 512<<20 {
		return "", ErrInvalid
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RegisterCodexCLI uses only deployment-selected, version-checked artifacts.
// An absent artifact leaves the full CLI layer pending. API callers never
// choose programs, arguments, files, providers or credential sources.
func (r *Registry) RegisterCodexCLI(ctx context.Context, probePath, binaryPath string) error {
	probeDigest, err := codexArtifactDigest(probePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	binaryDigest, err := codexArtifactDigest(binaryPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	output, err := RunControlledProcess(ctx, ProcessSpec{Executable: probePath, Args: []string{"--version"}, Env: map[string]string{"AIHUB_CODEX_BINARY": binaryPath, "GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}, Timeout: 15 * time.Second})
	var version map[string]string
	if err != nil || json.Unmarshal(output.Stdout, &version) != nil || len(version) != 2 || version["protocol"] != codexprobe.Protocol || version["version"] != codexprobe.Version {
		return ErrInvalid
	}
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F8" {
			continue
		}
		for _, req := range suite.Requirements {
			if !slices.Contains([]string{"codex-special.RL13", "codex-special.RL15"}, req.ID) {
				continue
			}
			if err := r.Register(Definition{ID: req.ID + "@official_cli", SuiteID: suite.ID, Requirements: []string{req.ID}, Layer: "official_cli", CodeVersion: engineVersion + ":" + gateway.ProbeImplementationVersion() + ":" + probeDigest + ":" + binaryDigest, Run: func(ctx context.Context, s *Session) Result {
				if a, e := codexArtifactDigest(probePath); e != nil || a != probeDigest {
					return Result{State: "invalid"}
				}
				if a, e := codexArtifactDigest(binaryPath); e != nil || a != binaryDigest {
					return Result{State: "invalid"}
				}
				return runCodexCLI(ctx, s, req, probePath, binaryPath)
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func runCodexCLI(ctx context.Context, s *Session, req gatewaycontract.Requirement, probePath, binaryPath string) Result {
	s.allowed["codex.models"] = true
	bootstrap := nativeScene{s: s, protocol: "openai-responses", checks: map[string]Assertion{}}
	catalog, _ := bootstrap.codexCatalog(ctx)
	model := s.Target.Model
	if model == "" {
		for _, raw := range list(catalog.Reply.Object["models"]) {
			if slug := str(object(raw)["slug"]); gatewaycontract.ValidModelID(slug) {
				model = slug
				break
			}
		}
	}
	b, err := newCodexBridge(s, model, bootstrap.witnesses)
	if err != nil {
		return Result{State: "invalid"}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.close()
		return Result{State: "invalid"}
	}
	tlsConfig, ca, err := codexprobe.LoopbackTLS()
	if err != nil {
		_ = listener.Close()
		b.close()
		return Result{State: "invalid"}
	}
	server := &http.Server{Handler: b, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = server.ServeTLS(listener, "", "") }()
	defer func() { _ = server.Close(); <-serveDone; b.close() }()
	profile := "notes"
	if req.ID == "codex-special.RL15" {
		profile = "catalog"
	}
	output, runErr := RunControlledProcess(ctx, ProcessSpec{Executable: probePath, Args: []string{"--probe"}, Files: map[string][]byte{"bridge-ca.pem": ca}, Env: map[string]string{"AIHUB_CODEX_BINARY": binaryPath, "AIHUB_CODEX_BRIDGE_ORIGIN": "https://" + listener.Addr().String(), "AIHUB_CODEX_BRIDGE_TOKEN": b.token, "AIHUB_CODEX_MODEL": codexProbeModel, "AIHUB_CODEX_PROFILE": profile, "AIHUB_CODEX_CA": "bridge-ca.pem", "GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}, Timeout: min(s.limits.RequestTimeout, 110*time.Second)})
	var client codexprobe.Result
	parseErr := json.Unmarshal(output.Stdout, &client)
	_ = server.Close()
	// No CLI callbacks may mutate the parent counters after this result is
	// handed back to the task executor.
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.wg.Wait()
	s.usage.Add(b.usage)
	n := nativeScene{s: s, protocol: "openai-responses", layer: "official_cli", requirement: req, checks: map[string]Assertion{}, witnesses: append(slices.Clone(bootstrap.witnesses), b.witnesses...)}
	refs := n.witnesses
	if len(refs) == 0 {
		return Result{State: "invalid"}
	}
	out := nativeOutcome{State: "pass", Reason: "ok", Response: Response{AttemptID: refs[len(refs)-1]}}
	if runErr != nil || parseErr != nil || client.Protocol != codexprobe.Protocol || client.Version != codexprobe.Version || client.Profile != profile {
		stage := "unknown"
		if slices.Contains([]string{"setup", "initialize", "account", "catalog", "thread", "turn_start", "turn", "completed"}, client.Stage) {
			stage = client.Stage
		}
		slog.Warn("controlled Codex probe did not complete", "profile", profile, "stage", stage, "exit_code", output.ExitCode, "reason", output.Reason)
		out.State, out.Reason = "inconclusive", "client_disabled"
		if ctx.Err() != nil {
			out.Reason = "cancelled"
		}
	}
	if req.ID == "codex-special.RL13" {
		auth, activation := out, out
		metadataValid := parseErr == nil && client.Protocol == codexprobe.Protocol && client.Version == codexprobe.Version && client.Profile == profile
		if metadataValid && client.PlatformAuth {
			auth.State, auth.Reason = "pass", "ok"
		}
		if metadataValid && b.observed["codex.notes.thread_hint"] > 0 {
			activation.State, activation.Reason = "pass", "ok"
		}
		n.check("cli_auth_profile", auth, client.PlatformAuth)
		n.check("features_activated", activation, b.observed["codex.notes.thread_hint"] > 0)
	} else {
		n.check("cli_catalog_consumed", out, client.CatalogModel && client.PlatformAuth && b.observed["codex.models"] > 0)
	}
	result := n.finish()
	if b.limited {
		result.State, result.Reason = "inconclusive", "platform_unavailable"
	}
	// Artifacts are never copied into reports, including sanitized adapter
	// diagnostics. These checks also guard accidental future driver changes.
	if strings.Contains(string(output.Stdout), b.token) {
		return Result{State: "invalid"}
	}
	return result
}
