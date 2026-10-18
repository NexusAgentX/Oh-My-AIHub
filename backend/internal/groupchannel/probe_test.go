package groupchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type probePolicy struct {
	channel.Outbound
	client *http.Client
}

func TestBasicProbeChoosesNativeCodexOnlyFromCurrentUpstreamEvidence(t *testing.T) {
	model := market.GroupModel{ModelType: "text"}
	proof := detection.Projection{Valid: true, Capabilities: []market.Capability{{ID: "codex-special.RL14/uncompressed_request", Layer: "upstream_protocol", State: "pass"}}}
	if basicProbeEndpoint(model, channel.FormatOpenAIResponses, proof) != "codex.responses" {
		t.Fatal("native-only model cannot pass Key probe")
	}
	for _, change := range []func(*detection.Projection){func(p *detection.Projection) { p.Valid = false }, func(p *detection.Projection) { p.Capabilities[0].Layer = "official_cli" }, func(p *detection.Projection) { p.Capabilities[0].State = "inconclusive" }} {
		p := proof
		p.Capabilities = append([]market.Capability(nil), proof.Capabilities...)
		change(&p)
		if basicProbeEndpoint(model, channel.FormatOpenAIResponses, p) != "" {
			t.Fatal("unknown/stale/other-layer evidence selected native endpoint")
		}
	}
	proof.Capabilities = append(proof.Capabilities, market.Capability{ID: "openai-responses.base/accepted_native_message_shapes", Layer: "upstream_protocol", State: "pass"})
	if basicProbeEndpoint(model, channel.FormatOpenAIResponses, proof) != "" {
		t.Fatal("extra paid native probe selected despite usable public entrance")
	}
	proof.Capabilities = []market.Capability{{ID: "codex-special.RL25/native_generation", Layer: "upstream_protocol", State: "pass"}}
	if basicProbeEndpoint(market.GroupModel{ModelType: "image"}, channel.FormatOpenAIImages, proof) != "codex.images" {
		t.Fatal("native image entry ignored")
	}
}

func nativeBasicStream() string {
	part := map[string]any{"type": "output_text", "text": "OK", "annotations": []any{}}
	item := map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	response := map[string]any{"id": "resp_probe", "object": "response", "created_at": 1, "model": "native", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp_probe", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "sequence_number": 2, "output_index": 0, "content_index": 0, "item_id": "msg_probe", "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
		{"type": "response.output_text.delta", "sequence_number": 3, "output_index": 0, "content_index": 0, "item_id": "msg_probe", "delta": "OK"},
		{"type": "response.output_text.done", "sequence_number": 4, "output_index": 0, "content_index": 0, "item_id": "msg_probe", "text": "OK"},
		{"type": "response.content_part.done", "sequence_number": 5, "output_index": 0, "content_index": 0, "item_id": "msg_probe", "part": part},
		{"type": "response.output_item.done", "sequence_number": 6, "output_index": 0, "item": item},
		{"type": "response.completed", "sequence_number": 7, "response": response},
	}
	var body strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	return body.String()
}

func TestNativeOnlyKeyProbeUsesFixedEntrancesAndTerminalSSE(t *testing.T) {
	for _, variant := range []string{"valid", "missing_terminal", "image"} {
		t.Run(variant, func(t *testing.T) {
			seen := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("native Key probe lost authentication")
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != "native" {
					t.Error("probe used another model")
				}
				if strings.HasPrefix(r.URL.Path, "/one/") {
					w.WriteHeader(503)
					return
				}
				if variant == "image" {
					_, _ = io.WriteString(w, `{"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="}]}`)
					return
				}
				if body["stream"] != true || body["store"] != false || body["instructions"] == nil || body["max_output_tokens"] != nil {
					t.Error("probe added a public/legacy parameter to native CLI shape")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				stream := nativeBasicStream()
				if variant == "missing_terminal" {
					stream = stream[:strings.LastIndex(stream, "event: response.completed")]
				}
				_, _ = io.WriteString(w, stream)
			}))
			defer up.Close()
			format, endpoint, kind := channel.FormatOpenAIResponses, "codex.responses", "text"
			path := "/backend-api/codex/responses"
			if variant == "image" {
				format, endpoint, kind, path = channel.FormatOpenAIImages, "codex.images", "image", "/backend-api/codex/images/generations"
			}
			out := basicProbeAt(context.Background(), probePolicy{client: up.Client()}, []gateway.Entrance{{BaseURL: up.URL + "/one"}, {BaseURL: up.URL + "/two"}}, market.Defaults{}, market.GroupModel{UpstreamModel: "native", ModelType: kind}, format, "synthetic-key", endpoint)
			if out.OK != (variant != "missing_terminal") || out.EndpointID != endpoint || !slices.Equal(seen, []string{"/one" + path, "/two" + path}) {
				t.Fatal("native minimal probe used public endpoint/invalid completion", out, seen)
			}
		})
	}
}

func (p probePolicy) FreshClient(context.Context, string) (*http.Client, error) { return p.client, nil }

func TestProbeDoesNotSpendForDisabledOrDeletedSupplier(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		d, data := fixture()
		data.Probes = nil
		if deleted {
			d.Suppliers[0].DeletedAt = &data.Channels[0].UpdatedAt
		} else {
			d.Suppliers[0].Enabled = false
		}
		if got := pendingModels(&State{Directory: d, Data: data}, data.Channels[0]); len(got) != 0 {
			t.Fatal("disabled supplier scheduled paid probe", got)
		}
	}
}

func TestKeyProbeUsesNativeBodyInheritedHeadersAndOrderedEntrances(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "invalid native reply"}[invalid], func(t *testing.T) {
			seen := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer synthetic-key" || r.Header.Get("User-Agent") != "group-agent" || r.Header.Get("X-Engine") != "one" {
					t.Error("effective headers or authentication lost")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"max_completion_tokens":64`) || strings.Contains(string(body), `"max_tokens"`) {
					t.Error("old parameter or wrong native probe")
				}
				w.Header().Set("Content-Type", "application/json")
				if invalid {
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/one/") {
					w.WriteHeader(503)
					return
				}
				_, _ = io.WriteString(w, `{"id":"fixture","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
			}))
			defer up.Close()
			d, _ := fixture()
			out := basicProbe(context.Background(), probePolicy{client: up.Client()}, []gateway.Entrance{{ID: "one", BaseURL: up.URL + "/one"}, {ID: "two", BaseURL: up.URL + "/two"}}, d.Groups[0].Defaults, market.GroupModel{UpstreamModel: "native"}, channel.FormatOpenAIChat, "synthetic-key")
			want := []string{"/one/v1/chat/completions", "/two/v1/chat/completions"}
			if invalid {
				want = want[:1]
			}
			if !slices.Equal(seen, want) || out.OK == invalid || out.DurationMS == nil {
				t.Fatal("probe result/order", out, seen)
			}
			if out.Error != nil && strings.Contains(*out.Error, "synthetic-key") {
				t.Fatal("secret in probe metadata")
			}
		})
	}
}
