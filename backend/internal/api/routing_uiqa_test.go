//go:build uiqa

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

type onlineChannelsFixture struct {
	gateway.Browse
	online []gateway.OnlineChannel
}

func (b onlineChannelsFixture) OnlineChannels(context.Context) ([]gateway.OnlineChannel, error) {
	return b.online, nil
}

// Explicit loopback-only synthetic browser fixture. Uses real model/routing
// handlers and versioned metric loading, with no database or paid upstream.
func TestRoutingUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_ROUTING_UI_QA") != "1" {
		t.Skip("explicit synthetic UI harness only")
	}
	const model = "synthetic-routing-2026"
	const a = "a0000000-0000-4000-8000-000000000001"
	const b = "a0000000-0000-4000-8000-000000000002"
	const c = "a0000000-0000-4000-8000-000000000003"
	owner := "00000000-0000-4000-8000-0000000000f2"
	var engine *gateway.Engine
	p := newPlatform(t, func(d *Dependencies) {
		d.CookieSecure = false
		engine = d.Gateway
		d.Browse = onlineChannelsFixture{online: []gateway.OnlineChannel{
			{ModelID: model, ChannelID: a, ChannelName: "合成标准渠道", OwnerID: owner, OwnerName: "合成贡献者", Formats: []channel.Format{channel.FormatOpenAIResponses, channel.FormatAnthropic}, MultiplierNano: 800_000_000},
			{ModelID: model, ChannelID: b, ChannelName: "暂无统计渠道", OwnerID: owner, OwnerName: "合成贡献者", Formats: []channel.Format{channel.FormatOpenAIResponses}, MultiplierNano: 1_000_000_000},
			{ModelID: model, ChannelID: c, ChannelName: "薄样本渠道", OwnerID: owner, OwnerName: "合成贡献者", Formats: []channel.Format{channel.FormatOpenAIResponses}, MultiplierNano: 900_000_000},
		}}
	})
	admin := &client{spec: p.spec, handler: p.handler}
	admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})
	modelSetup(t, admin, map[string]any{"id": model, "display_name": model, "base_prices": map[string]string{"input": "1.5", "output": "4.5", "cache_read": "0.15", "cache_write": "0"}})
	now := time.Now()
	engine.Store = browseMetricHistory{rows: []gateway.ModelAttemptHistoryRow{
		{ChannelID: a, ModelID: model, Protocol: "openai-responses", Format: channel.FormatOpenAIResponses, Stream: true, Bucket: now.Unix() / 300, Counted: 40, Successes: 38, Excluded: 5, ClientErrors: 1, LatencyBin: 54, SpeedBin: 89},
		{ChannelID: a, ModelID: model, Protocol: "native-messages", Format: channel.FormatAnthropic, Stream: true, Bucket: now.Unix() / 300, Counted: 24, Successes: 24, LatencyBin: 62, SpeedBin: -1},
		{ChannelID: c, ModelID: model, Protocol: "openai-responses", Format: channel.FormatOpenAIResponses, Stream: true, Bucket: now.Unix() / 300, Counted: 3, Successes: 3, LatencyBin: 0, SpeedBin: -1},
	}}
	if err := engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	var mode atomic.Value
	mode.Store("")
	stopped := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:18490", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__uiqa/stop" {
			w.WriteHeader(204)
			select {
			case <-stopped:
			default:
				close(stopped)
			}
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__uiqa/mode" {
			mode.Store(r.URL.Query().Get("value"))
			w.WriteHeader(204)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/models/") {
			if mode.Load() == "error" {
				writeError(w, 503, "synthetic_unavailable", "合成测试：统计服务暂不可用")
				return
			}
			if mode.Load() == "forbidden" {
				writeError(w, 403, "forbidden", "合成测试：没有访问权限")
				return
			}
			if mode.Load() == "processing" {
				timer := time.NewTimer(10 * time.Second)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
			}
			if mode.Load() == "empty" {
				recorder := httptest.NewRecorder()
				p.handler.ServeHTTP(recorder, r)
				var page map[string]any
				if recorder.Code == 200 && json.Unmarshal(recorder.Body.Bytes(), &page) == nil {
					page["channels"] = []any{}
					writeJSON(w, 200, page)
					return
				}
			}
		}
		p.handler.ServeHTTP(w, r)
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			close(stopped)
		}
	}()
	fmt.Println("UI_QA_READY routing http://127.0.0.1:18490 model=" + model)
	<-stopped
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
