package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func groupBook(t *testing.T, currency string, price int64) *quotepricing.Book {
	t.Helper()
	b, err := quotepricing.Freeze(quotepricing.Book{GroupID: "group", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"main": {ModelID: "main", Revision: 1, Quote: market.Quote{ID: "quote-v1", GroupModelID: "group-main", Version: 1, Currency: currency, BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.FromNano(price)}, UnitPrices: []market.UnitPrice{{Unit: "tool_call", Tool: "web_search", Price: money.FromNano(500_000_000)}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestGroupCandidateNeverBorrowsCatalogPriceOrFXDiscount(t *testing.T) {
	model := catalog.Model{ID: "main", InputPrice: 999_000_000_000, PriceCurrency: "CNY"}
	candidate := Candidate{Quotes: groupBook(t, "USD", 2_000_000_000), MultiplierNano: ledger.UnitRate / 2, RateNano: 7 * ledger.UnitRate, FXDiscount: true}
	call := &callState{started: time.Now(), billingFacts: quotepricing.Facts{MainReliable: true}}
	priced, snapshot, err := NewEngine(Dependencies{}).priceUsage(call, model, candidate, ledger.Usage{InputTokens: 1_000_000}, settings.Settings{USDCNYRateNano: 7 * ledger.UnitRate}, 0)
	if err != nil || priced.Cost.String() != "7" || !strings.Contains(string(snapshot), "quote-v1") || strings.Contains(string(snapshot), "999") {
		t.Fatalf("cost=%s err=%v snapshot=%s", priced.Cost.String(), err, snapshot)
	}
	model.ID = "unknown"
	if _, _, err = NewEngine(Dependencies{}).priceUsage(call, model, candidate, ledger.Usage{InputTokens: 1}, settings.Settings{}, 0); err == nil {
		t.Fatal("missing quote used global fallback")
	}
}
func TestNativeImageCountsActualProductsAndRetainsNoContent(t *testing.T) {
	for _, run := range []struct {
		body string
		want int64
	}{{`{"n":9,"data":[{"url":"https://private.invalid/image"},{"b64_json":"private-image"},{"error":{"message":"private-error"}},{}],"revised_prompt":"private-prompt"}`, 2}, {`{"data":[]}`, 0}, {`{"error":{"message":"failed"},"data":[{"url":"https://private.invalid"}]}`, 0}} {
		_, facts := ObserveNativeImage([]byte(run.body))
		count := int64(0)
		for _, unit := range facts.Units {
			if unit.Unit == "successful_image" {
				count = unit.QuantityNano / ledger.UnitRate
			}
		}
		if count != run.want {
			t.Fatalf("products=%d want=%d", count, run.want)
		}
		raw, _ := json.Marshal(facts)
		if strings.Contains(string(raw), "private") {
			t.Fatal("image body retained")
		}
	}
}
func TestOnlyReliableCompletedToolFactsAreBillable(t *testing.T) {
	raw := `{"usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"s1","type":"web_search_call","status":"completed"},{"id":"s1","type":"web_search_call","status":"completed"},{"id":"s2","type":"web_search_call","status":"failed"},{"id":"c1","type":"code_interpreter_call","container_id":"already-existing","memory_limit":"1g"}]}`
	observed := observe(channel.FormatOpenAIResponses, false, "application/json", raw, 7)
	if len(observed.Units) != 1 || observed.Units[0].QuantityNano != ledger.UnitRate || observed.Units[0].Tool != "web_search" {
		t.Fatalf("unreliable units charged: %+v", observed.Units)
	}
	if len(observed.Usage.Detail.Notes) == 0 {
		t.Fatal("unpriced code execution not explained")
	}
}

func TestGroupPriceProtocolFailureTransportInterruptionAndSelfUse(t *testing.T) {
	for _, run := range []struct {
		name, terminal string
		firstErr       error
		self, bill     bool
	}{
		{"protocol failure", `{"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":1000000,"output_tokens":0}}}`, io.EOF, false, false},
		{"transport interruption", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1000000,"output_tokens":0}}}`, io.ErrUnexpectedEOF, false, true},
		{"self use", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1000000,"output_tokens":0}}}`, io.EOF, true, false},
	} {
		t.Run(run.name, func(t *testing.T) {
			store := &speedStore{}
			engine := NewEngine(Dependencies{Store: store})
			candidate := Candidate{ChannelID: "channel", OwnerID: "provider", Quotes: groupBook(t, "CNY", 2_000_000_000), MultiplierNano: ledger.UnitRate}
			if run.self {
				candidate.OwnerID = "consumer"
			}
			call := &callState{id: "group-call", modelID: "main", started: time.Now(), format: channel.FormatOpenAIResponses, stream: true, key: KeyAuth{OwnerID: "consumer"}, attempts: []Attempt{{}}}
			body := "data: " + run.terminal + "\n\n"
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}
			recorder := httptest.NewRecorder()
			engine.streamBack(recorder, call, candidate.PricingModel(catalog.Model{ID: "main"}), candidate, attemptResult{response: response, first: []byte(body), firstErr: run.firstErr, cancel: func(error) {}, upstream: context.Background(), watch: watchClient(context.Background(), func(error) {}, drainWindow), started: call.started}, settings.Settings{})
			finish := store.finish
			if (finish.Bill != nil) != run.bill || recorder.Body.String() != body {
				t.Fatalf("bill=%+v altered response=%v", finish.Bill, recorder.Body.String() != body)
			}
			want := "2"
			if run.name == "protocol failure" {
				want = "0"
			}
			if finish.Cost.String() != want || !strings.Contains(string(finish.PriceSnapshot), "group-quote.v1") {
				t.Fatalf("finish=%+v", finish)
			}
		})
	}
}
