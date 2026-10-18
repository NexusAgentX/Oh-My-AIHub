//go:build uiqa

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit loopback-only synthetic browser fixture; no database, paid Key or
// upstream evidence. API success paths use the actual service and handlers.
func TestTypicalUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_TYPICAL_UI_QA") != "1" {
		t.Skip("explicit synthetic UI QA only")
	}
	service, _ := typicalFixture(t)
	p := newPlatform(t, func(d *Dependencies) { d.Typical = service; d.CookieSecure = false })
	admin := &client{spec: p.spec, handler: p.handler}
	admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); service.Run(ctx) }()
	defer func() { cancel(); <-workerDone }()
	stopped := make(chan struct{})
	var mode atomic.Value
	mode.Store("")
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
		if r.Method == "GET" && (r.URL.Path == "/api/typical-costs" || strings.HasPrefix(r.URL.Path, "/api/admin/typical-costs/settings")) {
			if mode.Load() == "error" {
				writeError(w, 503, "synthetic_unavailable", "合成测试：统计服务暂不可用")
				return
			}
			if r.URL.Path == "/api/typical-costs" && mode.Load() == "empty" {
				writeJSON(w, 200, typicalPage{Items: []typical.Estimate{}})
				return
			}
			if r.URL.Path == "/api/typical-costs" && mode.Load() == "processing" {
				recorder := httptest.NewRecorder()
				p.handler.ServeHTTP(recorder, r)
				if recorder.Code == 200 {
					var page typicalPage
					if json.Unmarshal(recorder.Body.Bytes(), &page) == nil {
						page.Status.Running = true
						writeJSON(w, 200, page)
						return
					}
				}
				for key, values := range recorder.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
				return
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
	fmt.Println("UI_QA_READY typical api=http://127.0.0.1:18490")
	<-stopped
	shutdown, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
}
