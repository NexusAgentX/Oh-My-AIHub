package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalysisBudgetErrors(t *testing.T) {
	for _, tc := range []struct {
		err         error
		status      int
		code, retry string
	}{{observe.ErrAnalysisBusy, http.StatusTooManyRequests, "analysis_busy", "15"}, {observe.ErrAnalysisTimeout, http.StatusServiceUnavailable, "analysis_timeout", ""}} {
		w := httptest.NewRecorder()
		writeObserveError(w, tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || w.Header().Get("Retry-After") != tc.retry {
			t.Fatalf("%d %s %v", w.Code, w.Body.String(), w.Header())
		}
	}
}
