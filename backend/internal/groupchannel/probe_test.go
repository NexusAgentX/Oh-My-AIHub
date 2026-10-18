package groupchannel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type probePolicy struct {
	channel.Outbound
	client *http.Client
}

func (p probePolicy) FreshClient(context.Context, string) (*http.Client, error) { return p.client, nil }
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
