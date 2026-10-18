package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

func receiptFinish(t *testing.T, e *env, consumer, provider person, keyID, receipt string) gateway.CallFinish {
	t.Helper()
	ctx := context.Background()
	id, err := e.store.Gateway.InsertCall(ctx, gateway.CallInsert{AccountID: consumer.id, KeyID: keyID, RequestedModel: "gpt-test", Format: channel.FormatOpenAIResponses, Outcome: gateway.OutcomeInProgress})
	if err != nil {
		t.Fatal(err)
	}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "group", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"gpt-test": {ModelID: "gpt-test", Revision: 1, Quote: market.Quote{ID: "quote", GroupModelID: "gm", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(t, "2")}, UnitPrices: []market.UnitPrice{{Unit: "tool_session", Tool: "code", Price: mustAmount(t, "3")}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	units := []quotepricing.Unit{}
	if receipt != "" {
		units = append(units, quotepricing.Unit{Unit: "tool_session", Tool: "code", QuantityNano: ledger.UnitRate, ReceiptKey: receipt})
	}
	statement, err := quotepricing.Calculate(book, "gpt-test", quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1_000_000}, Units: units}, time.Now(), ledger.UnitRate, ledger.UnitRate, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := gateway.WithQuoteStatement([]byte(`{}`), statement)
	if err != nil {
		t.Fatal(err)
	}
	return gateway.CallFinish{ID: id, AccountID: consumer.id, Outcome: gateway.OutcomeSucceeded, Usage: ledger.Usage{InputTokens: 1_000_000}, PriceSnapshot: snapshot, Cost: statement.Cost, Fee: statement.Fee, QuoteStatement: &statement, Bill: &gateway.Billing{ConsumerID: consumer.id, ProviderID: provider.id, Cost: statement.Cost, Fee: statement.Fee}}
}
func TestGroupExecutionReceiptConcurrentFinalizationAndReplay(t *testing.T) {
	e := newGroupEnv(t)
	consumer, provider := e.member("receipt-consumer", "100"), e.member("receipt-provider", "0")
	keyID, _ := e.defaultKey(consumer)
	first := receiptFinish(t, e, consumer, provider, keyID, strings.Repeat("a", 64))
	second := receiptFinish(t, e, consumer, provider, keyID, strings.Repeat("a", 64))
	var wg sync.WaitGroup
	results := make(chan gateway.FinishResult, 2)
	failures := make(chan error, 2)
	for _, finish := range []gateway.CallFinish{first, second} {
		wg.Go(func() {
			result, err := e.store.Gateway.FinishCall(context.Background(), finish)
			results <- result
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	for result := range results {
		if !result.Finished || result.Applied == nil {
			t.Fatal("missing applied receipt amounts")
		}
		total += result.Applied.Cost.Nano()
	}
	if total != mustAmount(t, "7").Nano() {
		t.Fatalf("expected two main charges and one execution: %d", total)
	}
	replay, err := e.store.Gateway.FinishCall(context.Background(), first)
	if err != nil || replay.Finished {
		t.Fatalf("duplicate finalizer: %+v %v", replay, err)
	}
	var receipts, postings int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM call_execution_receipts),(SELECT count(*) FROM ledger_transactions WHERE type='api_call')`).Scan(&receipts, &postings); err != nil || receipts != 1 || postings != 2 {
		t.Fatalf("receipts=%d postings=%d err=%v", receipts, postings, err)
	}
	var raw []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot FROM calls WHERE id=$1`, first.ID).Scan(&raw); err != nil || !json.Valid(raw) || strings.Contains(string(raw), strings.Repeat("a", 64)) {
		t.Fatal("receipt identity leaked into public price snapshot", err)
	}
	e.assertZeroSum()
}
func TestExecutionReceiptsRollbackWithCallAndLedger(t *testing.T) {
	e := newGroupEnv(t)
	consumer, provider := e.member("rollback-consumer", "100"), e.member("rollback-provider", "0")
	keyID, _ := e.defaultKey(consumer)
	finish := receiptFinish(t, e, consumer, provider, keyID, strings.Repeat("b", 64))
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `CREATE FUNCTION fail_receipt_post() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic receipt rollback'; END $$; CREATE TRIGGER fail_receipt_post BEFORE INSERT ON ledger_entries FOR EACH STATEMENT EXECUTE FUNCTION fail_receipt_post()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.Gateway.FinishCall(ctx, finish); err == nil {
		t.Fatal("injected ledger failure did not abort")
	}
	var receipts int
	var outcome string
	if err = e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM call_execution_receipts),outcome FROM calls WHERE id=$1`, finish.ID).Scan(&receipts, &outcome); err != nil || receipts != 0 || outcome != "in_progress" {
		t.Fatalf("partial transaction committed: %d %s %v", receipts, outcome, err)
	}
	if _, err = e.pool.Exec(ctx, `DROP TRIGGER fail_receipt_post ON ledger_entries; DROP FUNCTION fail_receipt_post()`); err != nil {
		t.Fatal(err)
	}
	if result, err := e.store.Gateway.FinishCall(ctx, finish); err != nil || !result.Finished || result.Applied.Cost != mustAmount(t, "5") {
		t.Fatalf("retry failed: %+v %v", result, err)
	}
	e.assertZeroSum()
}
func TestOrdinaryGroupPricingDoesNotIssueReceiptQueries(t *testing.T) {
	e, counter := tracedEnv(t)
	consumer, provider := e.member("ordinary-consumer", "100"), e.member("ordinary-provider", "0")
	keyID, _ := e.defaultKey(consumer)
	finish := receiptFinish(t, e, consumer, provider, keyID, "")
	before, _ := counter.Counts()
	if _, err := e.store.Gateway.FinishCall(context.Background(), finish); err != nil {
		t.Fatal(err)
	}
	after, _ := counter.Counts()
	for _, name := range []string{"ClaimExecutionReceipt", "LockPendingReceiptCall"} {
		if after[name]-before[name] != 0 {
			t.Fatalf("ordinary call added %s", name)
		}
	}
}
