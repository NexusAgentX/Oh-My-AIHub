package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type fakeFeeRateStore struct {
	versions []feerate.Version
}

func (s *fakeFeeRateStore) ListFeeRates(_ context.Context, limit int) ([]feerate.Version, error) {
	if limit < len(s.versions) {
		return s.versions[:limit], nil
	}
	return s.versions, nil
}

func (s *fakeFeeRateStore) AppendFeeRate(_ context.Context, actorID string, expectedVersion int64, rate money.Amount, reason string) (feerate.Version, error) {
	current := s.versions[0]
	if current.Version != expectedVersion {
		return feerate.Version{}, feerate.ErrConflict
	}
	if current.Rate == rate {
		return feerate.Version{}, feerate.ErrUnchanged
	}
	created := feerate.Version{Version: current.Version + 1, Rate: rate, CreatedByID: actorID, CreatedByUsername: "admin", Reason: reason, CreatedAt: time.Now()}
	s.versions = append([]feerate.Version{created}, s.versions...)
	return created, nil
}

func feeRateTestApp() (*app, *fakeFeeRateStore) {
	store := &fakeFeeRateStore{versions: []feerate.Version{{Version: 1, Rate: 1_000_000, CreatedAt: time.Now()}}}
	return &app{feeRates: feerate.NewService(store)}, store
}

func feeRateRequestAs(admin bool, method, body string) *http.Request {
	request := httptest.NewRequest(method, "/api/admin/fee-rate", strings.NewReader(body))
	actor := identity.Account{ID: "0d7d60a3-1e6f-4b8a-9d99-2f4a28c1b001", Username: "admin", IsAdmin: admin, Status: identity.StatusActive}
	return request.WithContext(context.WithValue(request.Context(), accountContextKey, actor))
}

func TestAdminFeeRatesReturnsDecimalStrings(t *testing.T) {
	application, _ := feeRateTestApp()
	recorder := httptest.NewRecorder()
	application.adminFeeRates(recorder, feeRateRequestAs(true, http.MethodGet, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Current struct {
			Version   int64   `json:"version"`
			FeeRate   string  `json:"fee_rate"`
			CreatedBy *string `json:"created_by"`
		} `json:"current"`
		History []json.RawMessage `json:"history"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Current.Version != 1 || payload.Current.FeeRate != "0.001" || payload.Current.CreatedBy != nil || len(payload.History) != 1 {
		t.Fatalf("payload = %s", recorder.Body.String())
	}
}

func TestAdminSetFeeRateValidatesAndAppends(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"negative", `{"expected_version":1,"fee_rate":"-0.1","reason":"r"}`, http.StatusUnprocessableEntity, "invalid_input"},
		{"above hundred percent", `{"expected_version":1,"fee_rate":"1.000000001","reason":"r"}`, http.StatusUnprocessableEntity, "invalid_input"},
		{"too precise", `{"expected_version":1,"fee_rate":"0.0000000001","reason":"r"}`, http.StatusUnprocessableEntity, "invalid_input"},
		{"exponent", `{"expected_version":1,"fee_rate":"1e-3","reason":"r"}`, http.StatusUnprocessableEntity, "invalid_input"},
		{"missing reason", `{"expected_version":1,"fee_rate":"0.002","reason":"  "}`, http.StatusUnprocessableEntity, "invalid_input"},
		{"stale version", `{"expected_version":7,"fee_rate":"0.002","reason":"r"}`, http.StatusConflict, "fee_rate_conflict"},
		{"unchanged", `{"expected_version":1,"fee_rate":"0.001","reason":"r"}`, http.StatusUnprocessableEntity, "fee_rate_unchanged"},
		{"unknown field", `{"expected_version":1,"fee_rate":"0.002","reason":"r","fee_rate_nano":1}`, http.StatusBadRequest, "invalid_json"},
	}
	for _, testCase := range cases {
		application, store := feeRateTestApp()
		recorder := httptest.NewRecorder()
		application.adminSetFeeRate(recorder, feeRateRequestAs(true, http.MethodPut, testCase.body))
		if recorder.Code != testCase.status || !strings.Contains(recorder.Body.String(), `"code":"`+testCase.code+`"`) {
			t.Errorf("%s: status = %d body = %s", testCase.name, recorder.Code, recorder.Body.String())
		}
		if len(store.versions) != 1 {
			t.Errorf("%s: appended a version", testCase.name)
		}
	}

	for _, rate := range []string{"0", "1", "0.000000001", "1.0"} {
		application, store := feeRateTestApp()
		recorder := httptest.NewRecorder()
		application.adminSetFeeRate(recorder, feeRateRequestAs(true, http.MethodPut, `{"expected_version":1,"fee_rate":"`+rate+`","reason":"调整"}`))
		if recorder.Code != http.StatusOK || len(store.versions) != 2 {
			t.Fatalf("rate %s: status = %d body = %s", rate, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"version":2`) || !strings.Contains(recorder.Body.String(), `"reason":"调整"`) {
			t.Fatalf("rate %s: body = %s", rate, recorder.Body.String())
		}
	}
}

func TestAdminFeeRateRejectsNonAdministrator(t *testing.T) {
	application, store := feeRateTestApp()
	recorder := httptest.NewRecorder()
	application.adminFeeRates(recorder, feeRateRequestAs(false, http.MethodGet, ""))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("list status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	application.adminSetFeeRate(recorder, feeRateRequestAs(false, http.MethodPut, `{"expected_version":1,"fee_rate":"0.002","reason":"r"}`))
	if recorder.Code != http.StatusForbidden || len(store.versions) != 1 {
		t.Fatalf("set status = %d versions = %d", recorder.Code, len(store.versions))
	}
}

func TestFeeRateRoutesRequireSessionAndSameOrigin(t *testing.T) {
	handler := NewHandler(Dependencies{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/admin/fee-rate", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "cross_origin_request") {
		t.Fatalf("cross-origin put = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/fee-rate", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous get = %d %s", recorder.Code, recorder.Body.String())
	}
}
