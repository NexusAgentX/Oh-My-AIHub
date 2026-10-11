package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

type fakeSource struct{ balances observe.Balances }

func (s fakeSource) Checks(context.Context) (observe.Checks, observe.Balances, error) {
	return observe.Checks{ZeroSumOK: true}, s.balances, nil
}
func (fakeSource) LedgerTotals(context.Context) ([]observe.LedgerTotals, error) { return nil, nil }
func (fakeSource) Channels(context.Context) ([]observe.ChannelState, error)     { return nil, nil }

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// The platform revenue gauge is the cumulative fees; the administrator
// adjustments get their own gauge instead of being netted into the revenue.
func TestPointsGaugesKeepRevenueAndAdjustmentsApart(t *testing.T) {
	m := New(nil)
	if err := m.Refresh(context.Background(), fakeSource{balances: observe.Balances{
		PlatformRevenue:  money.FromNano(30_000_000),      // 0.03 of fees
		AdminAdjustments: money.FromNano(100_000_000_000), // 100 issued to users
		PlatformAccount:  money.FromNano(-99_970_000_000), // the account balance: fees minus adjustments
	}}); err != nil {
		t.Fatal(err)
	}
	body := scrape(t, m)
	for _, want := range []string{
		"aihub_points_platform_revenue 0.03\n",
		"aihub_points_admin_adjustments 100\n",
		"# HELP aihub_points_platform_revenue Platform revenue: cumulative fees booked by api_call transactions. Excludes administrator adjustments",
		"# HELP aihub_points_admin_adjustments Net points issued to users by administrator adjustments",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "-99.97") {
		t.Errorf("the platform account balance must not be exposed as revenue:\n%s", body)
	}
}

// An administrator taking points back shows as a negative adjustment net, not as extra revenue.
func TestAdminAdjustmentsGaugeCanBeNegative(t *testing.T) {
	m := New(nil)
	if err := m.Refresh(context.Background(), fakeSource{balances: observe.Balances{
		AdminAdjustments: money.FromNano(-5_000_000_000),
	}}); err != nil {
		t.Fatal(err)
	}
	body := scrape(t, m)
	if !strings.Contains(body, "aihub_points_admin_adjustments -5\n") || !strings.Contains(body, "aihub_points_platform_revenue 0\n") {
		t.Fatalf("metrics:\n%s", body)
	}
}
