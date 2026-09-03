package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
)

func (a *app) opsMetrics(w http.ResponseWriter, r *http.Request) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "from 必须是 UTC RFC3339 时间")
		return
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "to 必须是 UTC RFC3339 时间")
		return
	}
	window := ops.Window{From: from.UTC(), To: to.UTC()}
	if !window.Validate() {
		writeError(w, http.StatusBadRequest, "invalid_input", "时间窗口无效：需要 from < to")
		return
	}
	snapshot, err := a.ops.OpsMetrics(r.Context(), window)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": snapshot})
}

func (a *app) opsProviderIncome(w http.ResponseWriter, r *http.Request) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "from 必须是 UTC RFC3339 时间")
		return
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "to 必须是 UTC RFC3339 时间")
		return
	}
	window := ops.Window{From: from.UTC(), To: to.UTC()}
	if !window.Validate() {
		writeError(w, http.StatusBadRequest, "invalid_input", "时间窗口无效：需要 from < to")
		return
	}
	snapshot, err := a.ops.OpsProviderIncome(r.Context(), window)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider_income": snapshot})
}

func (a *app) opsAnomalies(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.ops.OpsAnomalies(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": snapshot})
}

func (a *app) opsListInspections(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit 必须在 1 到 500 之间")
			return
		}
		limit = parsed
	}
	records, err := a.ops.OpsListInspections(r.Context(), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inspections": records})
}

func (a *app) opsRunInspection(w http.ResponseWriter, r *http.Request) {
	record, err := a.ops.OpsRunInspection(r.Context(), "manual")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"inspection": record})
}

func (a *app) opsTrialSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.ops.OpsTrialSummary(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trial_summary": summary})
}
