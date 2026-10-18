package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

func codexCLITLSBridge(t *testing.T, handler http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AIHub synthetic CLI root"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}}}
	server.StartTLS()
	return server, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func runProductionCodexProbe(t *testing.T, handler http.Handler, profile string) codexprobe.Result {
	return runProductionCodexProbeOutcome(t, handler, profile, true)
}

func runProductionCodexProbeOutcome(t *testing.T, handler http.Handler, profile string, success bool) codexprobe.Result {
	t.Helper()
	binary, adapter := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || adapter == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	bridge, ca := codexCLITLSBridge(t, handler)
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := detection.RunControlledProcess(ctx, detection.ProcessSpec{Executable: adapter, Args: []string{"--probe"}, Files: map[string][]byte{"bridge-ca.pem": ca}, Env: map[string]string{"AIHUB_CODEX_BINARY": binary, "AIHUB_CODEX_BRIDGE_ORIGIN": bridge.URL, "AIHUB_CODEX_BRIDGE_TOKEN": "sk-aih-synthetic", "AIHUB_CODEX_MODEL": "gpt-6.1-sol", "AIHUB_CODEX_PROFILE": profile, "AIHUB_CODEX_CA": "bridge-ca.pem", "GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}, VirtualMemoryMiB: 2048, Timeout: 110 * time.Second})
	var result codexprobe.Result
	parseErr := json.Unmarshal(output.Stdout, &result)
	if success && (err != nil || parseErr != nil || !result.Completed || !result.MarkerMatched || !result.PlatformAuth) {
		t.Fatalf("official CLI did not complete production-handler profile %s: reason=%s resource=%s exit=%d metadata=%+v", profile, output.Reason, output.ResourceFailure, output.ExitCode, result)
	}
	if !success && (err == nil || parseErr != nil || result.Protocol != codexprobe.Protocol || result.Version != codexprobe.Version || result.Completed || result.PlatformAuth) {
		t.Fatalf("revoked platform Key enabled the official client: reason=%s exit=%d metadata=%+v", output.Reason, output.ExitCode, result)
	}
	if strings.Contains(string(output.Stdout), "sk-aih-synthetic") || strings.Contains(string(output.Stdout), codexprobe.Marker) {
		t.Fatal("probe emitted a credential or body")
	}
	return result
}

// Real fixed official CLI -> real platform HTTP handlers -> synthetic upstream.
// This proves the implemented bridge, never supplier compatibility.
func TestOfficialCodexCLIUsesProductionHandlers(t *testing.T) {
	binary, adapter := os.Getenv("AIHUB_TEST_CODEX_BINARY"), os.Getenv("AIHUB_TEST_CODEX_PROBE")
	if binary == "" || adapter == "" {
		t.Skip("explicit pinned official CLI and probe artifacts required")
	}
	for _, profile := range []string{"standard", "notes", "image"} {
		t.Run(profile, func(t *testing.T) {
			var mu sync.Mutex
			paths := map[string]int{}
			namespaceValid, nativeModels, fedBack := true, true, false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				paths[r.URL.Path]++
				if r.Header.Get("Authorization") != "Bearer shared-synthetic-upstream" {
					t.Error("CLI platform token leaked to upstream or wrong candidate used")
				}
				var body map[string]any
				if r.Method == "POST" && json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid native request")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/backend-api/codex/models":
					_, _ = io.WriteString(w, `{"models":[{"slug":"native/model","display_name":"Synthetic model","description":"Isolated fixture","model_messages":{"instructions_template":"Use only the declared synthetic tools."},"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],"input_modalities":["text"],"supports_experimental_context":true,"context_window":32768}]}`)
				case "/backend-api/codex/alpha/notes/v2/thread_hint":
					ctx, _ := body["context"].(map[string]any)
					session, _ := ctx["session_id"].(string)
					namespaceValid = namespaceValid && len(session) == 36 && session[14] == '8' && (r.Header.Get("Session-Id") == "" || session == r.Header.Get("Session-Id"))
					_, _ = io.WriteString(w, `{"text":""}`)
				case "/backend-api/codex/images/generations":
					nativeModels = nativeModels && body["model"] == "native/image"
					_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==","generation_id":"synthetic-image"}]}`)
				case "/backend-api/codex/responses":
					nativeModels = nativeModels && body["model"] == "native/model"
					input, _ := body["input"].([]any)
					for _, raw := range input {
						item, _ := raw.(map[string]any)
						fedBack = fedBack || item["type"] == "function_call_output"
					}
					toolTurn := profile == "image" && paths[r.URL.Path] == 1
					item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": codexprobe.Marker, "annotations": []any{}}}}
					if toolTurn {
						item = map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "namespace": "image_gen", "name": "imagegen", "arguments": `{"prompt":"Synthetic blue square"}`}
					}
					response := map[string]any{"id": "resp_fixture", "object": "response", "created_at": 1, "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11}}
					w.Header().Set("Content-Type", "text/event-stream")
					events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "object": "response", "status": "in_progress", "output": []any{}}}}
					if !toolTurn {
						events = append(events, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}, map[string]any{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": codexprobe.Marker})
					}
					events = append(events, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": response})
					for _, event := range events {
						encoded, _ := json.Marshal(event)
						_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
					}
				default:
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"outside synthetic fixture"}}`)
				}
			}))
			defer upstream.Close()
			handler, store, _ := newCodexContextFixture(t, upstream)
			store.rows = store.rows[:1]
			store.key.Aliases = map[string]string{"gpt-6.1-sol": "platform/model:2026", "gpt-image-2": "image/model"}
			image := store.rows[0]
			image.ModelID, image.Candidate.UpstreamModel, image.Candidate.GroupModelID = "image/model", "native/image", "image-offering"
			book, err := quotepricing.Freeze(quotepricing.Book{GroupID: image.Candidate.GroupID, GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"image/model": {ModelID: "image/model", Revision: 1, Quote: market.Quote{ID: "image-quote", GroupModelID: image.Candidate.GroupModelID, Version: 1, Currency: "CNY", BillingUnit: "per_successful_image", UnitPrices: []market.UnitPrice{{Unit: "successful_image", Price: 1_000_000_000}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			image.Candidate.Quotes = book
			store.rows = append(store.rows, image)
			bridge, ca := codexCLITLSBridge(t, handler)
			defer bridge.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			output, err := detection.RunControlledProcess(ctx, detection.ProcessSpec{Executable: adapter, Args: []string{"--probe"}, Files: map[string][]byte{"bridge-ca.pem": ca}, Env: map[string]string{"AIHUB_CODEX_BINARY": binary, "AIHUB_CODEX_BRIDGE_ORIGIN": bridge.URL, "AIHUB_CODEX_BRIDGE_TOKEN": "sk-aih-synthetic", "AIHUB_CODEX_MODEL": "gpt-6.1-sol", "AIHUB_CODEX_PROFILE": profile, "AIHUB_CODEX_CA": "bridge-ca.pem", "GOMEMLIMIT": "128MiB", "GOMAXPROCS": "1"}, VirtualMemoryMiB: 2048, Timeout: 110 * time.Second})
			var result codexprobe.Result
			parseErr := json.Unmarshal(output.Stdout, &result)
			if err != nil || parseErr != nil || !result.Completed || !result.MarkerMatched || !result.PlatformAuth {
				// Adapter errors deliberately contain no raw CLI diagnostics.
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("official CLI failed: reason=%s resource=%s exit=%d metadata=%+v paths=%v", output.Reason, output.ResourceFailure, output.ExitCode, result, paths)
			}
			mu.Lock()
			defer mu.Unlock()
			if !nativeModels || !namespaceValid || paths["/backend-api/codex/responses"] == 0 || (profile != "standard" && paths["/backend-api/codex/alpha/notes/v2/thread_hint"] == 0) {
				t.Fatal("fixed CLI did not use actual native model/context handlers", paths)
			}
			if profile == "image" && (paths["/backend-api/codex/images/generations"] != 1 || !fedBack) {
				t.Fatal("actual image tool/independent model/result continuation missing", paths)
			}
			if profile == "image" {
				store.mu.Lock()
				defer store.mu.Unlock()
				imageCharges := 0
				for _, f := range store.finished {
					if f.ModelID == "image/model" {
						imageCharges++
						if f.Cost.Nano() != 1_000_000_000 || f.QuoteStatement == nil {
							t.Fatal("official image tool did not settle its independent quote", f.Cost)
						}
					}
				}
				if imageCharges != 1 {
					t.Fatal("official image tool was not settled exactly once", imageCharges)
				}
			}
			if strings.Contains(string(output.Stdout), "sk-aih-synthetic") || strings.Contains(string(output.Stdout), codexprobe.Marker) {
				t.Fatal("probe emitted a credential or body")
			}
		})
	}
}
