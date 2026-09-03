package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
)

type fakeOpsStore struct {
	metrics   ops.Metrics
	providers ops.ProviderIncomeSnapshot
	records   []ops.InspectionRecord
	summary   ops.TrialSummary
	anomalies ops.Anomalies
}

func (fake *fakeOpsStore) OpsMetrics(context.Context, ops.Window) (ops.Metrics, error) {
	return fake.metrics, nil
}
func (fake *fakeOpsStore) OpsProviderIncome(_ context.Context, window ops.Window) (ops.ProviderIncomeSnapshot, error) {
	snapshot := fake.providers
	snapshot.Window = window
	if snapshot.Providers == nil {
		snapshot.Providers = []ops.ProviderIncomeRow{}
	}
	return snapshot, nil
}
func (fake *fakeOpsStore) OpsAnomalies(context.Context) (ops.Anomalies, error) {
	return fake.anomalies, nil
}
func (fake *fakeOpsStore) OpsRunInspection(_ context.Context, triggeredBy string) (ops.InspectionRecord, error) {
	return ops.InspectionRecord{ID: "inspection-" + triggeredBy, TriggeredBy: triggeredBy, CheckedAt: time.Now().UTC()}, nil
}
func (fake *fakeOpsStore) OpsListInspections(context.Context, int64) ([]ops.InspectionRecord, error) {
	return fake.records, nil
}
func (fake *fakeOpsStore) OpsTrialSummary(context.Context) (ops.TrialSummary, error) {
	return fake.summary, nil
}

func callOpsMetrics(query string) *httptest.ResponseRecorder {
	application := &app{ops: &fakeOpsStore{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/ops/metrics"+query, nil)
	application.opsMetrics(recorder, request)
	return recorder
}

func TestOpsMetricsWindowValidation(t *testing.T) {
	if recorder := callOpsMetrics(""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing from/to = %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder := callOpsMetrics("?from=not-a-time&to=also-not"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("garbage from/to = %d", recorder.Code)
	}
	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if recorder := callOpsMetrics("?from=" + from + "&to=" + to); recorder.Code != http.StatusBadRequest {
		t.Fatalf("reversed window = %d", recorder.Code)
	}
	validTo := time.Now().UTC().Format(time.RFC3339)
	recorder := callOpsMetrics("?from=" + from + "&to=" + validTo)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid window = %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Metrics ops.Metrics `json:"metrics"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
}

func callOpsProviders(query string) *httptest.ResponseRecorder {
	application := &app{ops: &fakeOpsStore{providers: ops.ProviderIncomeSnapshot{
		TotalIncome: "12",
		Providers: []ops.ProviderIncomeRow{{
			AccountID: "provider-id", DisplayName: "顾言", TotalIncome: "12",
			OtherConsumerIncome: "10", OwnUsageIncome: "2",
		}},
	}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/ops/providers"+query, nil)
	application.opsProviderIncome(recorder, request)
	return recorder
}

func TestOpsProviderIncomeWindowValidation(t *testing.T) {
	if recorder := callOpsProviders(""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing from/to = %d: %s", recorder.Code, recorder.Body.String())
	}
	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	recorder := callOpsProviders("?from=" + from + "&to=" + to)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid window = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"display_name":"顾言"`) || !strings.Contains(body, `"success_rate":null`) {
		t.Fatalf("provider income missing explicit empty success rate: %s", body)
	}
	for _, forbidden := range []string{"base_url", "credential", "raw_error", "upstream_model_id"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("provider income leaked %q: %s", forbidden, body)
		}
	}
}

func TestOpsResponsesUseExplicitContracts(t *testing.T) {
	application := &app{ops: &fakeOpsStore{
		summary:   ops.TrialSummary{NonAdminAccounts: 3},
		anomalies: ops.Anomalies{Hard: []ops.Anomaly{{Kind: "k", Count: 1, Drilldown: "/admin/ops?drilldown=x"}}},
		records:   []ops.InspectionRecord{{ID: "r1"}},
	}}

	recorder := httptest.NewRecorder()
	application.opsTrialSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/ops/trial-summary", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"non_admin_accounts":3`) {
		t.Fatalf("trial summary = %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"raw_error", "credential", "base_url"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("trial summary leaked %q: %s", forbidden, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	application.opsRunInspection(recorder, httptest.NewRequest(http.MethodPost, "/api/admin/ops/inspections", nil))
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), "inspection-manual") {
		t.Fatalf("manual inspection = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	application.opsAnomalies(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/ops/anomalies", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"hard_anomalies"`) {
		t.Fatalf("anomalies = %d: %s", recorder.Code, recorder.Body.String())
	}
}
