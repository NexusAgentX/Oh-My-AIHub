//go:build uiqa

package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// Explicit loopback-only fixture for the shared-account UI, never a deployment.
func TestTestSiteUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_TEST_SITE_UI_QA") != "1" {
		t.Skip("explicit synthetic browser QA only")
	}
	store := newFakeStore()
	service := fixedTestService(t, store)
	if err := service.EnsureFixedAdministrator(context.Background(), fixedTestPassword); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Dependencies{Identity: service, Ledger: ledger.NewService(store), Settings: settings.NewService(store), Audit: audit.NewService(store), Observe: observe.NewService(&fakeObserveStore{})})
	stopped := make(chan struct{}, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__uiqa/stop" {
			w.WriteHeader(http.StatusNoContent)
			select {
			case stopped <- struct{}{}:
			default:
			}
			return
		}
		handler.ServeHTTP(w, r)
	})
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	}()
	t.Logf("test-site UI fixture: http://%s", listener.Addr())
	select {
	case <-stopped:
	case <-time.After(45 * time.Minute):
		t.Fatal("UI fixture timed out")
	}
}
