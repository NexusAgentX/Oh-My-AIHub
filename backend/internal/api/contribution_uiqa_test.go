//go:build uiqa

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// Explicit synthetic, local-only browser QA. Uses real contribution services,
// HTTP handlers and UI; never reads credentials, contacts upstreams or writes DB.
func TestContributionUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_CONTRIBUTION_UI_QA") != "1" {
		t.Skip("explicit synthetic UI harness only")
	}
	ctx := context.Background()
	store := newAPIReviewStore()
	runtime := contribution.NewRuntime(store, nil)
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := detection.NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	service := &contribution.Service{Store: store, Live: store, Planner: registry, Publisher: runtime}
	detectorStore := newAPIDetectorStore()
	detector, err := detection.New(ctx, detection.Config{Registry: registry, Store: detectorStore, Settings: detectorStore, Resolver: contribution.DraftResolver{Service: service}, Outbound: &unusedDetectorOutbound{}, Limits: detection.DefaultLimits(), CredentialRoot: make([]byte, 32), Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer detector.Close()
	p := newPlatform(t, func(d *Dependencies) { d.Contributions = service; d.Detection = detector; d.CookieSecure = false })
	admin := &client{spec: p.spec, handler: p.handler}
	admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})
	owner, ownerID := p.member(t, "qa-contributor")
	draft, err := service.New(ctx, contribution.Actor{ID: ownerID}, contribution.NewInput{Kind: "supplier", Starter: true})
	if err != nil {
		t.Fatal(err)
	}
	c := draft.Changes
	c.Suppliers[0].Value.Name = "合成供应商"
	c.Suppliers[0].Value.WebsiteURL = "https://synthetic.example"
	c.Suppliers[0].Value.Summary = "仅供界面验收的合成数据"
	c.Suppliers[0].Value.Description = "## 合成介绍\n\n测试成员贡献与审核。"
	c.Groups[0].Value.Name = "标准分组"
	c.Addresses[0].Value.BaseURL = "https://synthetic.example"
	c.Addresses[0].Value.Label = "主入口"
	c.Models[0].Value.ModelID = "fixture/model:2026"
	draft, err = service.Save(ctx, contribution.Actor{ID: ownerID}, draft.ID, draft.Revision, "合成测试 · 分组报价贡献", c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := service.New(ctx, contribution.Actor{ID: ownerID}, contribution.NewInput{Kind: "supplier"})
	if err != nil {
		t.Fatal(err)
	}
	meta.Changes.Suppliers[0].Value.Name = "待审核的合成供应商"
	meta.Changes.Suppliers[0].Value.WebsiteURL = "https://metadata.example"
	meta, err = service.Save(ctx, contribution.Actor{ID: ownerID}, meta.ID, meta.Revision, "供应商资料贡献", meta.Changes, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta, err = service.Submit(ctx, contribution.Actor{ID: ownerID}, meta.ID, meta.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Value
	mode.Store("normal")
	stopped := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:18490", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/__uiqa/owner" {
			cookie := *owner.cookie
			cookie.Path = "/"
			cookie.HttpOnly = true
			cookie.SameSite = http.SameSiteLaxMode
			http.SetCookie(w, &cookie)
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__uiqa/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			select {
			case <-stopped:
			default:
				close(stopped)
			}
			return
		}
		if r.URL.Path == "/__uiqa/mode" && r.Method == "POST" {
			mode.Store(r.URL.Query().Get("value"))
			w.WriteHeader(204)
			return
		}
		relevant := strings.HasPrefix(r.URL.Path, "/api/contributions") || strings.HasPrefix(r.URL.Path, "/api/admin/contributions")
		if relevant {
			switch mode.Load() {
			case "error":
				if r.Method == "GET" {
					writeError(w, 503, "synthetic_unavailable", "合成测试：贡献服务暂不可用")
					return
				}
			case "empty":
				if r.URL.Path == "/api/contributions" || r.URL.Path == "/api/admin/contributions" {
					writeJSON(w, 200, map[string]any{"items": []any{}, "next_cursor": nil})
					return
				}
			case "forbidden":
				if strings.Contains(r.URL.Path, draft.ID) {
					writeError(w, 403, "forbidden", "无权查看此贡献")
					return
				}
			case "conflict":
				if r.Method == "PUT" {
					writeContributionError(w, contribution.ErrConflict)
					return
				}
			case "processing":
				if r.Method == "PUT" {
					timer := time.NewTimer(3 * time.Second)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
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
	fmt.Printf("UI_QA_READY draft=%s review=%s api=http://127.0.0.1:18490\n", draft.ID, meta.ID)
	<-stopped
	shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
