package detection

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var sdkVersions = map[string]string{"github.com/anthropics/anthropic-sdk-go": "v1.82.0", "google.golang.org/genai": "v1.75.0"}

type sdkProbeResult struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider"`
	Version  string `json:"version"`
}

func sdkDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 || info.Size() > 128<<20 {
		return "", ErrInvalid
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func sdkEnvironment() map[string]string {
	return map[string]string{"GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}
}

// RegisterSDK accepts only an operator-selected, source-verified application
// artifact. Missing artifacts leave the two SDK requirements in the full plan
// as pending. Paths/commands are never supplied by API callers.
func (r *Registry) RegisterSDK(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return ErrInvalid
	}
	digest, err := sdkDigest(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	version, err := RunControlledProcess(ctx, ProcessSpec{Executable: path, Args: []string{"--version"}, Env: sdkEnvironment(), Timeout: 10 * time.Second, VirtualMemoryMiB: 2048})
	if err != nil {
		return err
	}
	var actual map[string]string
	if json.Unmarshal(version.Stdout, &actual) != nil || len(actual) != len(sdkVersions) {
		return ErrInvalid
	}
	for module, pinned := range sdkVersions {
		if actual[module] != pinned {
			return ErrInvalid
		}
	}
	for _, suite := range r.manifest.Suites {
		if suite.Feature != "F4" {
			continue
		}
		for _, req := range suite.Requirements {
			if !strings.HasSuffix(req.ID, ".sdk") {
				continue
			}
			protocol, requirement := suite.Protocol, req
			provider, module := "anthropic", "github.com/anthropics/anthropic-sdk-go"
			if protocol == "native-generate-content" {
				provider, module = "gemini", "google.golang.org/genai"
			}
			code := sha256.Sum256([]byte(engineVersion + digest))
			if err = r.Register(Definition{ID: req.ID + "@upstream_protocol", SuiteID: suite.ID, Requirements: []string{req.ID}, Layer: "upstream_protocol", CodeVersion: fmt.Sprintf("%s@%s:%s", provider, sdkVersions[module], hex.EncodeToString(code[:])), Run: func(ctx context.Context, s *Session) Result {
				n := nativeScene{s: s, protocol: protocol, requirement: requirement, checks: map[string]Assertion{}}
				if current, e := sdkDigest(path); e != nil || current != digest {
					return Result{State: "invalid"}
				}
				return n.sdk(ctx, path)
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *nativeScene) sdk(ctx context.Context, path string) Result {
	provider, module := "anthropic", "github.com/anthropics/anthropic-sdk-go"
	if n.protocol == "native-generate-content" {
		provider, module = "gemini", "google.golang.org/genai"
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Result{State: "invalid"}
	}
	token := hex.EncodeToString(random)
	clear(random)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Result{State: "invalid"}
	}
	var used atomic.Bool
	observed := make(chan nativeOutcome, 1)
	endpoint, expected, query := nativeEndpoint(n.protocol, n.s.Target.Model, false)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-AIHub-SDK-Bridge")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.WriteHeader(403)
			return
		}
		if r.Method != "POST" || r.URL.EscapedPath() != expected || r.URL.RawQuery != "" || !used.CompareAndSwap(false, true) {
			w.WriteHeader(400)
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(r.Body, n.s.limits.RequestBytes+1))
		if readErr != nil || int64(len(body)) > n.s.limits.RequestBytes {
			w.WriteHeader(413)
			return
		}
		payload, decodeErr := decodeNative(body)
		if decodeErr != nil || (n.protocol == "native-messages" && str(payload["model"]) != n.s.Target.Model) {
			w.WriteHeader(400)
			return
		}
		headers := http.Header{"Content-Type": {"application/json"}}
		for name, values := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-stainless-") || strings.EqualFold(name, "User-Agent") || strings.EqualFold(name, "anthropic-version") || strings.EqualFold(name, "x-goog-api-client") {
				headers[name] = values
			}
		}
		out := n.call(r.Context(), Request{EndpointID: endpoint, Path: expected, Query: query, Headers: headers, Body: body, Category: "generate"}, false)
		// The fixed basic SDK request needs no large output. Do not let a hostile
		// JSON union allocate unbounded worker memory before its SDK validates it.
		if len(out.Response.Body) > 256<<10 {
			observed <- out
			w.WriteHeader(502)
			return
		}
		status := out.Response.Status
		if status == 0 {
			status = 502
		}
		w.Header().Set("Content-Type", out.Response.Headers.Get("Content-Type"))
		w.WriteHeader(status)
		_, _ = w.Write(out.Response.Body)
		observed <- out
	})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-done }()
	env := sdkEnvironment()
	env["AIHUB_SDK_PROVIDER"], env["AIHUB_SDK_BASE_URL"], env["AIHUB_SDK_BRIDGE_TOKEN"] = provider, "http://"+listener.Addr().String(), token
	env["AIHUB_SDK_MODEL"], env["AIHUB_SDK_PROMPT"], env["AIHUB_SDK_EXPECTED"] = n.s.Target.Model, nativeBasicPrompt, nativeMarker
	output, runErr := RunControlledProcess(ctx, ProcessSpec{Executable: path, Args: []string{"--probe"}, Env: env, Timeout: min(n.s.limits.RequestTimeout, 3*time.Minute), VirtualMemoryMiB: 2048})
	if runErr != nil {
		return Result{State: "invalid"}
	}
	var probe sdkProbeResult
	if json.Unmarshal(output.Stdout, &probe) != nil || probe.Provider != provider || probe.Version != sdkVersions[module] {
		return Result{State: "invalid"}
	}
	var out nativeOutcome
	select {
	case out = <-observed:
	case <-ctx.Done():
		return Result{State: "invalid"}
	case <-time.After(5 * time.Second):
		return Result{State: "invalid"}
	}
	valid := probe.OK && out.Reply.Structure && contentMatches(out, nativeMarker) && nativeEndedNormally(out.Reply)
	n.check("fixed_sdk_real_basic_request_not_mock", out, valid)
	return n.finish()
}
