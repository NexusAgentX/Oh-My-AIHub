package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

func decodeObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("snapshot is not a JSON object: %v (%s)", err, raw)
	}
	return out
}

func TestWithQuoteStatementAppendsOneMember(t *testing.T) {
	raw := []byte(`{"base_prices":{"input":"1"},"detail":null}`)
	out, err := WithQuoteStatement(raw, quotepricing.Statement{Version: "group-quote.v1"})
	if err != nil {
		t.Fatal(err)
	}
	object := decodeObject(t, out)
	if len(object) != 3 {
		t.Fatalf("members = %d, want 3: %s", len(object), out)
	}
	if !strings.Contains(string(object["group_quote"]), "group-quote.v1") {
		t.Fatalf("group_quote = %s", object["group_quote"])
	}
	if strings.Count(string(out), `"group_quote"`) != 1 {
		t.Fatalf("duplicate group_quote member: %s", out)
	}
}

func TestWithQuoteStatementReplacesExistingMember(t *testing.T) {
	raw := []byte(`{"a":1}`)
	first, err := WithQuoteStatement(raw, quotepricing.Statement{Version: "group-quote.v1"})
	if err != nil {
		t.Fatal(err)
	}
	// Settlement attaches the repriced statement to the snapshot that pricing
	// already carried, so the second write must replace, not duplicate.
	second, err := WithQuoteStatement(first, quotepricing.Statement{Version: "group-quote.v2"})
	if err != nil {
		t.Fatal(err)
	}
	object := decodeObject(t, second)
	if len(object) != 2 {
		t.Fatalf("members = %d, want 2: %s", len(object), second)
	}
	if strings.Count(string(second), `"group_quote"`) != 1 {
		t.Fatalf("duplicate group_quote member: %s", second)
	}
	if !strings.Contains(string(object["group_quote"]), "group-quote.v2") {
		t.Fatalf("group_quote = %s", object["group_quote"])
	}
}

func TestWithQuoteStatementHandlesEmptyObject(t *testing.T) {
	out, err := WithQuoteStatement([]byte(`{}`), quotepricing.Statement{Version: "group-quote.v1"})
	if err != nil {
		t.Fatal(err)
	}
	object := decodeObject(t, out)
	if len(object) != 1 {
		t.Fatalf("members = %d, want 1: %s", len(object), out)
	}
}

func TestWithQuoteStatementRejectsNonObject(t *testing.T) {
	if _, err := WithQuoteStatement([]byte(`[]`), quotepricing.Statement{}); err == nil {
		t.Fatal("array snapshot accepted")
	}
	if _, err := WithQuoteStatement([]byte(`{`), quotepricing.Statement{}); err == nil {
		t.Fatal("truncated snapshot accepted")
	}
}

func BenchmarkWithQuoteStatement(b *testing.B) {
	raw := []byte(`{"base_prices":{"input":"1","output":"2","cache_read":"0","cache_write":"0"},"detail":null,"currency":"CNY","fee_rate_nano":0,"multiplier":"1","prices":{"input":"1","output":"2","cache_read":"0","cache_write":"0"},"selected_prices":{"input":"1","output":"2"},"token_prices":{},"base_token_prices":{},"tier":null}`)
	statement := quotepricing.Statement{Version: "group-quote.v1", GroupID: "group", GroupRevision: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := WithQuoteStatement(raw, statement); err != nil {
			b.Fatal(err)
		}
	}
}
