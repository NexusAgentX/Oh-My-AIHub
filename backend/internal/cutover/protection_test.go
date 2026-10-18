package cutover

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func tables() []Table {
	out := []Table{}
	for _, name := range []string{"accounts", "sessions", "audit_log", "settings", "api_keys", "ledger_accounts", "ledger_transactions", "ledger_entries", "calls", "c2c_orders", "c2c_trades", "route_pref_channels", "market_quotes", "models", "model_price_tiers", "channel_models", "suppliers", "supplier_endpoints", "goose_db_version"} {
		out = append(out, Table{Name: name, Columns: []string{"id", "value"}, PrimaryKey: []string{"id"}})
	}
	out = append(out, Table{Name: "channels", Columns: []string{"id", "owner_id", "name", "api_key_ciphertext", "api_key_nonce", "api_key_key_id", "created_at", "market_group_id", "status", "suspended_reason", "summary", "description", "deleted_at", "updated_at", "base_url"}, PrimaryKey: []string{"id"}})
	return out
}
func digest(t *testing.T, rows ...string) Digest {
	t.Helper()
	h := NewRowHasher()
	for _, row := range rows {
		if err := h.Add([]byte(row)); err != nil {
			t.Fatal(err)
		}
	}
	d, err := h.Finish(Rule{Table: "ledger_accounts", Scope: "all", Columns: []string{"id", "balance"}, PrimaryKey: []string{"id"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func manifest(t *testing.T, version int64, ledger Digest) Manifest {
	t.Helper()
	rules, err := Plan(tables())
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Format: Format, MigrationVersion: version, Tables: []Digest{}}
	for _, r := range rules {
		if r.Table == "ledger_accounts" {
			m.Tables = append(m.Tables, ledger)
			continue
		}
		d, err := NewRowHasher().Finish(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		m.Tables = append(m.Tables, d)
	}
	for _, name := range GuardNames {
		enabled := "O"
		if name == "reject_ledger_mutation" || name == "verify_ledger_transaction_balance" {
			enabled = ""
		}
		m.Guards = append(m.Guards, Guard{Name: name, Enabled: enabled, SHA256: strings.Repeat("a", 64)})
	}
	for _, name := range CheckNames {
		m.Checks = append(m.Checks, Check{Name: name})
	}
	for _, key := range requiredConstraints {
		table, name, _ := strings.Cut(key, "/")
		kind := "f"
		if strings.HasSuffix(name, "_pkey") {
			kind = "p"
		}
		m.Constraints = append(m.Constraints, Constraint{Table: table, Name: name, Kind: kind, SHA256: strings.Repeat("c", 64)})
	}
	return m
}
func TestProtectionPlanKeepsAllFinancialAndHistoricalTablesAndCiphertext(t *testing.T) {
	rules, err := Plan(tables())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accounts", "api_keys", "calls", "ledger_accounts", "ledger_transactions", "ledger_entries", "c2c_orders", "c2c_trades", "market_quotes"} {
		if !slices.ContainsFunc(rules, func(r Rule) bool { return r.Table == name && r.Scope == "all" }) {
			t.Fatal("protected table omitted", name)
		}
	}
	for _, name := range []string{"models", "model_price_tiers", "channel_models", "suppliers", "supplier_endpoints", "goose_db_version"} {
		if slices.ContainsFunc(rules, func(r Rule) bool { return r.Table == name }) {
			t.Fatal("authorized config confused with protected history", name)
		}
	}
	for _, r := range rules {
		if r.Table == "channels" && r.Scope == "channel_identity" && (!slices.Contains(r.Columns, "api_key_ciphertext") || slices.Contains(r.Columns, "base_url")) {
			t.Fatal("ciphertext lost or old address treated as identity", r)
		}
		if r.Table == "route_pref_channels" && r.Scope != "group_channel_selections" {
			t.Fatal("new channel choices may be removed", r)
		}
	}
	if _, err := Plan(slices.DeleteFunc(tables(), func(v Table) bool { return v.Name == "ledger_entries" })); !errors.Is(err, ErrIncomplete) {
		t.Fatal("partial schema accepted", err)
	}
}
func TestPerAccountDigestDetectsZeroSumBalanceMovesAndContentChanges(t *testing.T) {
	a := digest(t, `{"id":"a","balance":10}`, `{"id":"b","balance":-10}`)
	b := digest(t, `{"id":"a","balance":20}`, `{"id":"b","balance":-20}`)
	before := manifest(t, 14, a)
	after := manifest(t, 26, b)
	changed, err := Compare(before, after)
	if err != nil || len(changed) != 1 || changed[0].Reason != "rows_changed" {
		t.Fatal("zero-sum move concealed", changed, err)
	}
	after = manifest(t, 26, a)
	changed, err = Compare(before, after)
	if err != nil || len(changed) != 0 {
		t.Fatal("unchanged rows rejected", changed, err)
	}
	x, y := digest(t, "ab", "c"), digest(t, "a", "bc")
	if x.SHA256 == y.SHA256 {
		t.Fatal("ambiguous row framing")
	}
	if strings.Contains(a.SHA256, "balance") {
		t.Fatal("raw row content in manifest")
	}
}
func TestIncompleteStreamsAndPendingWorkCannotProduceCompleteEvidence(t *testing.T) {
	for _, p := range []Pending{{Calls: 1}, {Background: 1}, {Calls: -1}} {
		if p.Check() == nil {
			t.Fatal("pending or invalid work allowed", p)
		}
	}
	h := NewRowHasher()
	if err := h.Add([]byte(`{"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	if d, err := h.Finish(Rule{}, errors.New("private transport detail")); err == nil || d.SHA256 != "" {
		t.Fatal("partial stream became a fingerprint", d, err)
	}
	if _, err := Plan([]Table{{Name: "accounts; DROP TABLE calls", Columns: []string{"id"}}}); err == nil {
		t.Fatal("SQL-like metadata accepted")
	}
	if (Manifest{Format: Format, MigrationVersion: 14}).Validate() == nil {
		t.Fatal("empty manifest hid initialized data")
	}
}
