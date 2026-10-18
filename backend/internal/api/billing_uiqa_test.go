//go:build uiqa

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

func TestBillingUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_BILLING_UI_QA") != "1" {
		t.Skip("explicit synthetic browser fixture only")
	}
	p := newPlatform(t, func(d *Dependencies) { d.CookieSecure = false })
	client := &client{spec: p.spec, handler: p.handler}
	account := client.expect(t, 201, http.MethodPost, "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})["account"].(map[string]any)
	id := "40000000-0000-4000-8000-000000000001"
	model := "demo-text"
	mode, source := "cheapest", "account"
	ended := time.Now()
	started := ended.Add(-time.Second)
	duration := 1000
	p.observe.calls = []observe.CallRecord{{CallRow: observe.CallRow{ID: id, CreatedAt: started, CompletedAt: &ended, Account: observe.AccountRef{ID: account["id"].(string), Username: "qaadmin", DisplayName: "合成验收管理员"}, ModelID: &model, RequestedModel: model, Format: "openai_responses", Outcome: "succeeded", Usage: observe.Usage{InputTokens: 1_000_000}, Cost: money.FromNano(8_250_000_000), Fee: money.FromNano(82_500_000), DurationMS: &duration}, PriceSnapshot: groupBillingFixture(t), RoutingMode: &mode, RoutingSource: &source, Attempts: []observe.Attempt{}}}
	stopped := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:18490", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__uiqa/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			close(stopped)
			return
		}
		p.handler.ServeHTTP(w, r)
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			close(stopped)
		}
	}()
	fmt.Println("BILLING_UI_QA_READY call=" + id)
	<-stopped
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
