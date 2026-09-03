package postgres_test

import (
	"context"
	"strings"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ops"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

func TestOpsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(basePool.Close)

	schema := "test_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatalf("migrate isolated test schema: %v", err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open isolated test schema: %v", err)
	}
	t.Cleanup(pool.Close)
	store := storepg.New(pool)
	identityService, err := identity.NewService(store, 24*time.Hour)
	if err != nil {
		t.Fatalf("create identity service: %v", err)
	}
	admin := createExactlyOneBootstrapAdmin(t, ctx, store)
	if changed, err := identityService.ChangePassword(ctx, admin.ID, "Bootstrap-password-2026", "Ops-admin-password-2026!"); err != nil {
		t.Fatalf("ready administrator: %v", err)
	} else {
		admin = changed.Account
	}
	createReady := func(username string) identity.Account {
		t.Helper()
		created, err := identityService.CreateInvitedAccount(ctx, admin, username, username, mustAmount(t, "300"), false, identity.StatusActive)
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		changed, err := identityService.ChangePassword(ctx, created.Account.ID, created.InitialPassword, "Ops-member-password-2026-"+username)
		if err != nil {
			t.Fatalf("ready %s: %v", username, err)
		}
		return changed.Account
	}
	funder := createReady("ops.funder")
	seller := createReady("ops.seller")
	ledgerService := ledger.NewService(store)

	window := ops.Window{From: time.Now().UTC().Add(-24 * time.Hour), To: time.Now().UTC().Add(time.Hour)}

	t.Run("empty metrics never fabricate samples", func(t *testing.T) {
		snapshot, err := store.OpsMetrics(ctx, window)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.API.PrecheckRejected != 0 || snapshot.API.ReachedUpstream != 0 || snapshot.API.TerminalReached != 0 {
			t.Fatalf("empty api funnel = %+v", snapshot.API)
		}
		if snapshot.API.SuccessRate != nil {
			t.Fatalf("empty success rate fabricated = %s", *snapshot.API.SuccessRate)
		}
		if snapshot.API.AverageTTFTMillis != nil || snapshot.API.AverageTPS != nil {
			t.Fatalf("empty ttft/tps fabricated = %+v", snapshot.API)
		}
		if snapshot.Concentration.Top1Share != nil || snapshot.Concentration.Top5Share != nil || snapshot.Concentration.HHI != nil {
			t.Fatalf("empty concentration fabricated = %+v", snapshot.Concentration)
		}
		if snapshot.C2C.Quote.LastTradedPriceFen != nil || snapshot.C2C.Quote.BestBidPriceFen != nil || snapshot.C2C.Quote.BestAskPriceFen != nil || snapshot.C2C.Quote.SpreadFen != nil {
			t.Fatalf("empty quote fabricated = %+v", snapshot.C2C.Quote)
		}
		if snapshot.Ledger.TotalPostedBalance != "0" {
			t.Fatalf("empty ledger total = %s", snapshot.Ledger.TotalPostedBalance)
		}
		if len(snapshot.NegativeBalances) != 0 {
			t.Fatalf("empty negatives = %+v", snapshot.NegativeBalances)
		}
	})

	t.Run("negative balance risk reflects funding streak", func(t *testing.T) {
		if _, err := ledgerService.Transfer(ctx, "ops-fund", funder.ID, seller.ID, mustAmount(t, "40"), "fund seller", "test_funding", "ops-fund"); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.OpsMetrics(ctx, window)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Ledger.TotalPostedBalance != "0" {
			t.Fatalf("zero-sum broken: %s", snapshot.Ledger.TotalPostedBalance)
		}
		var risk *ops.NegativeBalanceRisk
		for index := range snapshot.NegativeBalances {
			if snapshot.NegativeBalances[index].Username == "ops.funder" {
				risk = &snapshot.NegativeBalances[index]
			}
		}
		if risk == nil {
			t.Fatalf("funder negative risk missing: %+v", snapshot.NegativeBalances)
		}
		if risk.PostedBalance != "-40" || risk.NegativeSince == "" || risk.LastFinancialActivity == "" || risk.OverLimit {
			t.Fatalf("funder risk row = %+v", risk)
		}
		if risk.CreditLimit != "300" {
			t.Fatalf("funder credit limit = %+v", risk)
		}
		if risk.InactiveDays < 0 {
			t.Fatalf("inactive days negative: %+v", risk)
		}
		if snapshot.Concentration.Top1Share == nil || snapshot.Concentration.Top5Share == nil || snapshot.Concentration.HHI == nil {
			t.Fatalf("concentration missing on real balances: %+v", snapshot.Concentration)
		}
		for label, value := range map[string]*string{"top1": snapshot.Concentration.Top1Share, "top5": snapshot.Concentration.Top5Share, "hhi": snapshot.Concentration.HHI} {
			fraction := strings.Split(*value, ".")
			if len(fraction) > 2 || len(fraction[1]) > 6 {
				t.Fatalf("concentration %s not rounded to 6 decimals: %s", label, *value)
			}
		}
		if *snapshot.Concentration.Top1Share != "1.000000" {
			t.Fatalf("single positive account top1 share = %s, want 1.000000", *snapshot.Concentration.Top1Share)
		}
		if snapshot.Concentration.TotalPositive != "40" {
			t.Fatalf("total positive = %s", snapshot.Concentration.TotalPositive)
		}
	})

	t.Run("c2c orders appear and consumption stays empty without calls", func(t *testing.T) {
		keyring, err := c2c.ParseKeyring("test="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "test")
		if err != nil {
			t.Fatalf("keyring: %v", err)
		}
		c2cService, err := c2c.NewService(store, keyring)
		if err != nil {
			t.Fatalf("c2c service: %v", err)
		}
		method := []c2c.PaymentMethodInput{{Type: c2c.PaymentWeChat, Contact: "wx-ops"}}
		if _, err := c2cService.CreateOrder(ctx, seller, "ops-sell", c2c.SideSell, 100, mustAmount(t, "10"), mustAmount(t, "1"), mustAmount(t, "5"), method); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.OpsMetrics(ctx, window)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range snapshot.C2C.Orders {
			if row.Side == "sell" && row.Status == "open" && row.Count == 1 {
				found = true
			}
		}
		if !found {
			t.Fatalf("sell order missing from metrics: %+v", snapshot.C2C.Orders)
		}
		if snapshot.C2C.Quote.BestAskPriceFen == nil || *snapshot.C2C.Quote.BestAskPriceFen != 100 {
			t.Fatalf("best ask missing: %+v", snapshot.C2C.Quote)
		}
		if snapshot.C2C.Quote.BestBidPriceFen != nil || snapshot.C2C.Quote.SpreadFen != nil {
			t.Fatalf("one-sided book fabricated bid/spread: %+v", snapshot.C2C.Quote)
		}
		if snapshot.Consumption.ConsumerSpend != "0" || snapshot.Consumption.PlatformFee != "0" {
			t.Fatalf("consumption fabricated: %+v", snapshot.Consumption)
		}
	})

	t.Run("anomalies empty on consistent state", func(t *testing.T) {
		snapshot, err := store.OpsAnomalies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.HardCount != 0 || len(snapshot.Hard) != 0 {
			t.Fatalf("hard anomalies on clean state: %+v", snapshot.Hard)
		}
		if len(snapshot.Attention) == 0 {
			t.Fatalf("attention items missing: %+v", snapshot)
		}
	})

	t.Run("inspection persists and lists", func(t *testing.T) {
		startup, err := store.OpsRunInspection(ctx, "startup")
		if err != nil {
			t.Fatal(err)
		}
		manual, err := store.OpsRunInspection(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		if !startup.ZeroSumOK || !startup.ProjectionOK || !startup.CallSettlementOK || !startup.C2CConsistencyOK {
			t.Fatalf("clean inspection failed: %+v", startup)
		}
		if startup.ZeroSumDifference != "0" || startup.SuccessfulCallsWithoutSettlement != 0 {
			t.Fatalf("clean inspection differences: %+v", startup)
		}
		records, err := store.OpsListInspections(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 2 || records[0].ID != manual.ID || records[1].ID != startup.ID {
			t.Fatalf("inspection history order = %+v", records)
		}
		if _, err := store.OpsRunInspection(ctx, "bogus"); err == nil {
			t.Fatal("bogus trigger accepted")
		}
	})

	t.Run("trial summary aggregates without sensitive fields", func(t *testing.T) {
		summary, err := store.OpsTrialSummary(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if summary.NonAdminAccounts != 2 || summary.C2COpenOrders != 1 {
			t.Fatalf("trial summary counts = %+v", summary)
		}
		if !summary.LedgerZeroSumOK {
			t.Fatalf("trial summary zero-sum flag false: %+v", summary)
		}
		if summary.LastInspectionOK == nil || !*summary.LastInspectionOK {
			t.Fatalf("trial summary inspection flag = %+v", summary)
		}
		if summary.InspectionPassCount != 2 || summary.InspectionTotalCount != 2 {
			t.Fatalf("trial summary inspection counts = %+v", summary)
		}
	})

	t.Run("window validation", func(t *testing.T) {
		instant := time.Now().UTC()
		if (ops.Window{From: instant, To: instant}).Validate() {
			t.Fatal("equal window accepted")
		}
		if (ops.Window{From: time.Now().Add(time.Hour), To: time.Now()}).Validate() {
			t.Fatal("reversed window accepted")
		}
	})
}
