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
			if !slices.Contains([]string{"codex-special.RL01", "codex-special.RL02", "codex-special.RL03", "codex-special.RL08", "codex-special.RL13", "codex-special.RL14", "codex-special.RL15", "codex-special.RL18"}, req.ID) {
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

type codexCLIProof struct {
	Client           codexprobe.Result
	Wire             codexWireFacts
	State, Reason    string
	Witnesses        []string
	Observed         map[string]int
	ContextContinued map[string]bool
	ContextPhases    map[string]map[string]int
}

func runCodexCLI(ctx context.Context, s *Session, req gatewaycontract.Requirement, probePath, binaryPath string) Result {
	n := nativeScene{s: s, protocol: "openai-responses", layer: "official_cli", requirement: req, checks: map[string]Assertion{}}
	profiles := []string{"lite_tools"}
	switch req.ID {
	case "codex-special.RL03":
		profiles = []string{"tools", "lite_tools"}
	case "codex-special.RL08":
		profiles = []string{"lite_tools", "plain"}
	case "codex-special.RL13":
		profiles = []string{"notes"}
	case "codex-special.RL14":
		profiles = []string{"compressed", "plain"}
	case "codex-special.RL15":
		profiles = []string{"catalog"}
	case "codex-special.RL18":
		profiles = []string{"notes_lifecycle"}
	}
	for _, profile := range profiles {
		proof := runCodexCLIProfile(ctx, s, profile, probePath, binaryPath)
		n.witnesses = append(n.witnesses, proof.Witnesses...)
		out := nativeOutcome{State: proof.State, Reason: proof.Reason}
		if len(proof.Witnesses) > 0 {
			out.Response.AttemptID = proof.Witnesses[len(proof.Witnesses)-1]
		}
		client, wire := proof.Client, proof.Wire
		closed := client.Completed && client.MarkerMatched && client.ToolCalls == 1 && wire.ToolCalls == 1 && wire.ToolResults == 1 && wire.ToolDeclarations > 0 && wire.SSE >= 2
		switch req.ID {
		case "codex-special.RL01":
			for _, label := range req.Assertions {
				n.check(label, out, closed && wire.LiteRequests >= 2 && wire.StandardRequests == 0)
			}
		case "codex-special.RL02":
			n.check("additional_tools_declaration", out, wire.LiteRequests > 0 && wire.ToolDeclarations > 0)
			n.check("tool_call_association", out, client.ToolCalls == 1 && wire.ToolCalls == 1 && wire.ToolResults == 1)
			n.check("tool_result_continuation", out, closed)
		case "codex-special.RL03":
			label := "standard_tools_roundtrip"
			shape := wire.StandardRequests >= 2 && wire.LiteRequests == 0
			if profile == "lite_tools" {
				label, shape = "lite_tools_roundtrip", wire.LiteRequests >= 2 && wire.StandardRequests == 0
			}
			n.check(label, out, closed && shape)
		case "codex-special.RL13":
			auth, activation := out, out
			if client.PlatformAuth && client.Protocol == codexprobe.Protocol {
				auth.State, auth.Reason = "pass", "ok"
			}
			if proof.Observed["codex.notes.thread_hint"] > 0 && client.Protocol == codexprobe.Protocol {
				activation.State, activation.Reason = "pass", "ok"
			}
			n.check("cli_auth_profile", auth, client.PlatformAuth)
			n.check("features_activated", activation, proof.Observed["codex.notes.thread_hint"] > 0)
		case "codex-special.RL08":
			requests := wire.LiteRequests + wire.StandardRequests
			n.check("sse_lifecycle", out, client.Completed && requests > 0 && wire.SSE == requests)
			n.check("terminal_usage", out, client.Completed && requests > 0 && wire.ReliableUsage == requests)
			if profile == "plain" {
				n.check("text_utf8_deltas", out, client.MarkerMatched && wire.TextDeltas > 0)
			} else {
				n.check("tool_argument_deltas", out, closed && wire.ToolDeltas > 0)
			}
		case "codex-special.RL14":
			valid := client.Completed && client.MarkerMatched && wire.SSE > 0
			if profile == "compressed" {
				valid = valid && wire.ZstdRequests > 0 && wire.PlainRequests == 0
				n.check("content_encoding_zstd", out, valid)
				n.check("native_zstd_request", out, valid)
			} else {
				n.check("uncompressed_request", out, valid && wire.PlainRequests > 0 && wire.ZstdRequests == 0)
			}
		case "codex-special.RL15":
			n.check("cli_catalog_consumed", out, client.CatalogModel && client.PlatformAuth && proof.Observed["codex.models"] > 0)
		case "codex-special.RL18":
			for _, operation := range []string{"write_file", "read_file", "list_files_by_prefix", "search_contents", "append_to_file"} {
				endpoint := "codex.notes." + operation
				n.check(operation, out, client.ContextVerified && proof.ContextPhases["notes_primary"][endpoint] > 0 && proof.ContextContinued[endpoint])
			}
			n.check("reopen_session", out, client.ContextVerified && client.ReopenVerified && proof.ContextPhases["notes_reopen"]["codex.notes.read_file"] > 0 && proof.ContextContinued["codex.notes.read_file"])
			n.check("context_isolation", out, client.ContextVerified && client.IsolationVerified && proof.ContextPhases["notes_isolation"]["codex.notes.read_file"] > 0 && proof.ContextContinued["codex.notes.read_file"])
		}
	}
	return n.finish()
}

func runCodexCLIProfile(ctx context.Context, s *Session, profile, probePath, binaryPath string) (proof codexCLIProof) {
	key := "codex-cli-profile-v1:" + profile
	if cached := s.Recall(key); len(cached) > 0 && json.Unmarshal(cached, &proof) == nil {
		return proof
	}
	proof.State, proof.Reason = "inconclusive", "client_disabled"
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
		return proof
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.close()
		return proof
	}
	tlsConfig, ca, err := codexprobe.LoopbackTLS()
	if err != nil {
		_ = listener.Close()
		b.close()
		return proof
	}
	server := &http.Server{Handler: b, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = server.ServeTLS(listener, "", "") }()
	defer func() { _ = server.Close(); <-serveDone; b.close() }()
	executionStarted := time.Now().UnixNano()
	output, runErr := RunControlledProcess(ctx, ProcessSpec{Executable: probePath, Args: []string{"--probe"}, Files: map[string][]byte{"bridge-ca.pem": ca}, Env: map[string]string{"AIHUB_CODEX_BINARY": binaryPath, "AIHUB_CODEX_BRIDGE_ORIGIN": "https://" + listener.Addr().String(), "AIHUB_CODEX_BRIDGE_TOKEN": b.token, "AIHUB_CODEX_MODEL": codexProbeModel, "AIHUB_CODEX_PROFILE": profile, "AIHUB_CODEX_CA": "bridge-ca.pem", "GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}, Timeout: min(s.limits.RequestTimeout, 110*time.Second)})
	executionFinished := time.Now().UnixNano()
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
	refs := append(slices.Clone(bootstrap.witnesses), b.witnesses...)
	if len(refs) == 0 {
		return proof
	}
	proof = codexCLIProof{Client: client, Wire: b.wire, State: "pass", Reason: "ok", Witnesses: refs, Observed: b.observed, ContextContinued: b.contextContinued}
	if runErr != nil || parseErr != nil || client.Protocol != codexprobe.Protocol || client.Version != codexprobe.Version || client.Profile != profile {
		stage := "unknown"
		if slices.Contains([]string{"setup", "initialize", "account", "catalog", "thread", "turn_start", "turn", "completed"}, client.Stage) {
			stage = client.Stage
		}
		proof.State, proof.Reason = "inconclusive", "client_disabled"
		if parseErr == nil && client.Protocol == codexprobe.Protocol && client.Version == codexprobe.Version && client.Profile == profile && b.failure != nil {
			// A fixed CLI which actually reached an upstream that rejected its
			// request supplies a real negative result. Missing activation, local
			// admission faults and broken adapters remain execution-incomplete.
			proof.State, proof.Reason = b.failure.State, b.failure.Reason
		}
		if ctx.Err() != nil {
			proof.Reason = "cancelled"
		}
		slog.Warn("controlled Codex probe did not complete", "profile", profile, "stage", stage, "exit_code", output.ExitCode, "state", proof.State, "reason", proof.Reason)
	}
	if profile == "notes_lifecycle" && proof.State == "pass" {
		counts, valid := codexObservedPhases(client.Phases, b.contextExecutions, executionStarted, executionFinished)
		proof.ContextPhases = counts
		if !valid {
			proof.State, proof.Reason = "inconclusive", "detector_error"
		}
	}
	if b.limited {
		proof.State, proof.Reason = "inconclusive", "platform_unavailable"
	}
	// Artifacts are never copied into reports, including sanitized adapter
	// diagnostics. These checks also guard accidental future driver changes.
	if strings.Contains(string(output.Stdout), b.token) {
		return codexCLIProof{State: "inconclusive", Reason: "detector_error"}
	}
	if proof.State == "pass" || proof.State == "unsupported" || proof.State == "fail" {
		data, _ := json.Marshal(proof)
		_ = s.Remember(key, data)
	}
	return proof
}

func codexObservedPhases(phases []codexprobe.Phase, executions []codexContextExecution, start, end int64) (map[string]map[string]int, bool) {
	counts := map[string]map[string]int{}
	kinds := []string{"notes_primary", "notes_isolation", "notes_reopen"}
	if len(phases) != len(kinds) || start <= 0 || end < start {
		return counts, false
	}
	previous := start
	for i, phase := range phases {
		if phase.Kind != kinds[i] || phase.Start < previous || phase.End < phase.Start || phase.End > end {
			return counts, false
		}
		previous = phase.End
		counts[phase.Kind] = map[string]int{}
		for _, execution := range executions {
			if execution.Start >= phase.Start && execution.End >= execution.Start && execution.End <= phase.End {
				counts[phase.Kind][execution.Endpoint]++
			}
		}
	}
	return counts, true
}
