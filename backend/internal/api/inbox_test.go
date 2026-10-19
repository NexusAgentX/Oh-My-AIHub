package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestInboxTradeActionsAndExpiry(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute)
	statement := "private statement"
	for _, tc := range []struct {
		state       c2c.TradeStatus
		role        string
		deadline    time.Time
		statement   *string
		category    string
		hasDeadline bool
	}{
		{c2c.TradeAwaitingPayment, "buyer", future, nil, "action", true},
		{c2c.TradeAwaitingPayment, "buyer", now, nil, "progress", false},
		{c2c.TradeAwaitingPayment, "seller", future, nil, "progress", false},
		{c2c.TradePaid, "seller", future, nil, "action", false},
		{c2c.TradeDisputed, "buyer", future, nil, "action", false},
		{c2c.TradeDisputed, "buyer", future, &statement, "progress", false},
		{c2c.TradeReleased, "buyer", future, nil, "", false},
	} {
		item, ok := inboxTrade(c2c.TradeSignal{ID: "trade", Status: tc.state, PaymentDeadline: tc.deadline, MissingStatement: tc.statement == nil, ViewerRole: tc.role}, now)
		if ok != (tc.category != "") || ok && (item.Category != tc.category || (item.Deadline != nil) != tc.hasDeadline) {
			t.Errorf("%s/%s: %+v, %v", tc.state, tc.role, item, ok)
		}
	}
}
func TestInboxOnlyWarnsAboutListedOrSuspendedChannels(t *testing.T) {
	for _, tc := range []struct {
		status                     channel.Status
		pending, failed, available bool
		category                   string
		blocker                    bool
	}{
		{channel.StatusUnlisted, false, true, false, "", false},
		{channel.StatusSuspended, false, false, false, "anomaly", true},
		{channel.StatusListed, true, false, false, "progress", false},
		{channel.StatusListed, false, true, true, "anomaly", false},
		{channel.StatusListed, false, true, false, "anomaly", true},
	} {
		probe := "available"
		if tc.pending {
			probe = "pending"
		}
		if tc.failed {
			probe = "unavailable"
		}
		item, ok := inboxChannel(groupchannel.View{ID: "channel", Status: tc.status, Available: tc.available, Models: []groupchannel.ModelView{{Enabled: true, ProbeState: probe}}})
		if ok != (tc.category != "") || ok && (item.Category != tc.category || item.Blocker != tc.blocker) {
			t.Errorf("%+v: %+v", tc, item)
		}
	}
}
func TestInboxKeyBudgetThresholdAndPriority(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Second)
	limit := money.Amount(1001)
	monthly := money.Amount(10)
	key := apikey.Key{Status: apikey.StatusEnabled, BudgetDaily: &limit, Spend: apikey.Spend{Today: 800}}
	if _, ok := inboxKey(key, now); ok {
		t.Fatal("800/1001 is below 80%")
	}
	key.Spend.Today = 801
	if item, ok := inboxKey(key, now); !ok || !strings.Contains(item.Message, "80%") {
		t.Fatalf("threshold: %+v", item)
	}
	key.BudgetMonthly = &monthly
	key.Spend.Month = 10
	if item, _ := inboxKey(key, now); !strings.Contains(item.Message, "本月预算已用完") {
		t.Fatalf("exhausted priority: %+v", item)
	}
	key.ExpiresAt = &past
	if item, _ := inboxKey(key, now); !strings.Contains(item.Message, "已过期") {
		t.Fatalf("expiry: %+v", item)
	}
	key.Status = apikey.StatusDisabled
	if _, ok := inboxKey(key, now); ok {
		t.Fatal("disabled old keys are not actionable")
	}
}
func TestInboxContributionsAndDetectionUseActualStages(t *testing.T) {
	for _, stage := range []string{"ready", "invalid", "conflict", "submitted", "publication_pending", "detecting"} {
		item, ok := inboxContribution(contribution.Detail{Stage: stage, Draft: contribution.Draft{ID: "draft", State: "draft"}})
		if !ok || item.Blocker || item.Deadline != nil {
			t.Errorf("stage %s: %+v", stage, item)
		}
	}
	if _, ok := inboxContribution(contribution.Detail{Stage: "approved"}); ok {
		t.Fatal("approved is finished")
	}
	item, ok := inboxDetection(detection.Job{State: "finished", Results: []detection.Result{{Reason: "detector_error"}}})
	if !ok || item.Category != "anomaly" {
		t.Fatalf("detector error: %+v", item)
	}
	if _, ok := inboxDetection(detection.Job{State: "cancelled"}); ok {
		t.Fatal("cancelled tasks need no action")
	}
}
func inboxSourceFromResponse(t *testing.T, response map[string]any, id string) map[string]any {
	t.Helper()
	for _, value := range response["sources"].([]any) {
		source := value.(map[string]any)
		if source["id"] == id {
			return source
		}
	}
	t.Fatalf("source %s missing", id)
	return nil
}
func TestInboxCompletesAllTradePagesAndPreservesPrivacy(t *testing.T) {
	p := newPlatform(t)
	admin := bootstrap(t, p.spec, p.handler)
	buyer, buyerID := p.member(t, "inbox-buyer")
	seller, sellerID := p.member(t, "inbox-seller")
	other, _ := p.member(t, "inbox-other")
	p.c2c.balances[sellerID] = money.Amount(200 * money.Scale)
	order := seller.expect(t, 201, "POST", "/api/c2c/orders", map[string]any{"amount": "150", "unit_price_fen": 100, "payment_methods": []map[string]string{{"channel": "bank", "account": "synthetic-private-payment"}}})["order"].(map[string]any)
	created := buyer.expect(t, 201, "POST", "/api/c2c/orders/"+order["id"].(string)+"/trades", map[string]string{"amount": "1"})["trade"].(map[string]any)
	original := p.c2c.trades[created["id"].(string)]
	for index := 1; index < 101; index++ {
		copy := original
		copy.ID = fmt.Sprintf("80000000-0000-4000-8000-%012d", index)
		copy.CreatedAt = original.CreatedAt.Add(-time.Duration(index) * time.Second)
		p.c2c.trades[copy.ID] = copy
	}
	response := buyer.expect(t, 200, "GET", "/api/inbox", nil)
	trades := inboxSourceFromResponse(t, response, "trades")
	if trades["state"] != "complete" || trades["counts"].(map[string]any)["action"] != float64(101) || len(trades["items"].([]any)) != 101 {
		t.Fatalf("full pages: %+v", trades)
	}
	for _, value := range response["sources"].([]any) {
		if value.(map[string]any)["workspace"] == "admin" {
			t.Fatal("member received governance data")
		}
	}
	if raw := buyer.call(t, "GET", "/api/inbox", nil).Body.String(); strings.Contains(raw, "synthetic-private-payment") || strings.Contains(raw, buyerID) || strings.Contains(raw, "buyer_statement") {
		t.Fatalf("unexpected private fields: %s", raw)
	}
	empty := inboxSourceFromResponse(t, other.expect(t, 200, "GET", "/api/inbox", nil), "trades")
	if empty["counts"].(map[string]any)["action"] != float64(0) {
		t.Fatal("cross-owner leak")
	}
	// Admins also have a personal source; all-site work is isolated in admin sources.
	adminTrades := inboxSourceFromResponse(t, admin.expect(t, 200, "GET", "/api/inbox", nil), "trades")
	if len(adminTrades["items"].([]any)) != 0 {
		t.Fatal("admin personal source broadened")
	}

	for index := 101; index < 1001; index++ {
		copy := original
		copy.ID = fmt.Sprintf("80000000-0000-4000-8000-%012d", index)
		copy.CreatedAt = original.CreatedAt.Add(-time.Duration(index) * time.Second)
		p.c2c.trades[copy.ID] = copy
	}
	limited := inboxSourceFromResponse(t, buyer.expect(t, 200, "GET", "/api/inbox", nil), "trades")
	if limited["state"] != "limited" || limited["counts"] != nil || len(limited["items"].([]any)) != 1000 {
		t.Fatalf("bounded scan pretended complete: state=%v, counts=%v", limited["state"], limited["counts"])
	}
	buyer.expect(t, 401, "GET", "/api/inbox", nil, withoutCookie)
}
func TestInboxUnavailableSourcesHaveUnknownCounts(t *testing.T) {
	p := newPlatform(t)
	bootstrap(t, p.spec, p.handler)
	member, _ := p.member(t, "inbox-fail")
	source := inboxSourceFromResponse(t, member.expect(t, 200, "GET", "/api/inbox", nil), "contributions")
	if source["state"] != "unavailable" || source["counts"] != nil {
		t.Fatalf("failure disguised as zero: %+v", source)
	}
}
func TestInboxRejectsRequestsInsteadOfQueuingWhenBusy(t *testing.T) {
	a := &app{inboxSlots: make(chan struct{}, 2)}
	a.inboxSlots <- struct{}{}
	a.inboxSlots <- struct{}{}
	recorder := httptest.NewRecorder()
	a.getInbox(recorder, httptest.NewRequest(http.MethodGet, "/api/inbox", nil).WithContext(context.Background()))
	if recorder.Code != 429 {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestInboxContainsSourcePanicsWithinTheRequestBoundary(t *testing.T) {
	source := readInboxSource(context.Background(), inboxLoader{source: inboxSource{ID: "contributions", Label: "my contributions", Workspace: "member"}, load: func(context.Context) ([]inboxItem, error) { panic("private fixture detail") }})
	if source.State != "unavailable" || source.Counts != nil || len(source.Items) != 0 || source.ID != "contributions" {
		t.Fatalf("source panic pretended success: %+v", source)
	}
}
