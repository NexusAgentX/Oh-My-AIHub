// Package cutover defines read-only preservation evidence for the one-time
// market cutover. It never grants database cleanup or deployment authorization.
package cutover

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"slices"
	"strings"
)

var ErrInvalid = errors.New("invalid preservation evidence")
var ErrIncomplete = errors.New("preservation evidence incomplete")
var ErrNotDrained = errors.New("finish existing calls and background responses before cutover")
var ErrIntegrity = errors.New("ledger integrity checks failed")

const Format = "market-preservation.v1"

var requiredTables = []string{"accounts", "sessions", "audit_log", "settings", "api_keys", "ledger_accounts", "ledger_transactions", "ledger_entries", "calls", "c2c_orders", "c2c_trades", "channels"}

type Table struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	PrimaryKey []string `json:"primary_key"`
}
type Rule struct {
	Table      string   `json:"table"`
	Scope      string   `json:"scope"`
	Columns    []string `json:"columns"`
	PrimaryKey []string `json:"primary_key"`
}
type Digest struct {
	Rule   Rule   `json:"rule"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type Pending struct {
	Calls      int64 `json:"calls"`
	Background int64 `json:"background"`
}

func (p Pending) Check() error {
	if p.Calls < 0 || p.Background < 0 {
		return ErrInvalid
	}
	if p.Calls != 0 || p.Background != 0 {
		return ErrNotDrained
	}
	return nil
}

type Manifest struct {
	Format           string       `json:"format"`
	MigrationVersion int64        `json:"migration_version"`
	Pending          Pending      `json:"pending"`
	Tables           []Digest     `json:"tables"`
	Guards           []Guard      `json:"guards"`
	Checks           []Check      `json:"checks"`
	Constraints      []Constraint `json:"constraints"`
}

type Constraint struct {
	Table  string `json:"table"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// These are retained history/ledger links, not the intentionally retired
// calls-to-global-model link. Every other existing protected PK/FK/unique
// constraint is captured too; this minimum prevents an incomplete baseline.
var requiredConstraints = []string{"accounts/accounts_pkey", "api_keys/api_keys_pkey", "channels/channels_pkey", "calls/calls_pkey", "ledger_accounts/ledger_accounts_pkey", "ledger_transactions/ledger_transactions_pkey", "ledger_entries/ledger_entries_pkey", "c2c_orders/c2c_orders_pkey", "c2c_trades/c2c_trades_pkey", "calls/calls_ledger_tx_fk", "calls/calls_final_channel_id_fkey", "ledger_entries/ledger_entries_transaction_id_fkey", "ledger_entries/ledger_entries_ledger_account_id_fkey", "c2c_trades/c2c_trades_ledger_tx_id_fkey"}

func validateConstraints(m Manifest) error {
	seen := map[string]bool{}
	for _, c := range m.Constraints {
		key := c.Table + "/" + c.Name
		if !validIdentifier(c.Table) || !validIdentifier(c.Name) || seen[key] || LegacyConfiguration(c.Table) || c.Table == "goose_db_version" || !slices.Contains([]string{"p", "f", "u"}, c.Kind) || len(c.SHA256) != 64 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(c.SHA256); err != nil {
			return ErrInvalid
		}
		seen[key] = true
	}
	if m.MigrationVersion != 0 || len(m.Tables) != 0 {
		for _, key := range requiredConstraints {
			if !seen[key] {
				return ErrIncomplete
			}
		}
	}
	return nil
}

type Guard struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
	SHA256  string `json:"sha256"`
}
type Check struct {
	Name       string `json:"name"`
	Violations int64  `json:"violations"`
}

var GuardNames = []string{"ledger_entries_immutable", "ledger_transaction_balance", "ledger_transactions_immutable", "reject_ledger_mutation", "verify_ledger_transaction_balance"}
var CheckNames = []string{"ledger_total", "account_balances", "transaction_balance", "entry_balances", "escrow_orders", "order_trades", "unbilled_calls", "unbooked_trades"}

func validateIntegrity(m Manifest) error {
	guards := map[string]bool{}
	for _, g := range m.Guards {
		if !slices.Contains(GuardNames, g.Name) || guards[g.Name] || len(g.SHA256) != 64 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(g.SHA256); err != nil {
			return ErrInvalid
		}
		function := g.Name == "reject_ledger_mutation" || g.Name == "verify_ledger_transaction_balance"
		if function && g.Enabled != "" || !function && g.Enabled != "O" && g.Enabled != "A" {
			return ErrIntegrity
		}
		guards[g.Name] = true
	}
	checks := map[string]bool{}
	for _, c := range m.Checks {
		if !slices.Contains(CheckNames, c.Name) || checks[c.Name] || c.Violations < 0 {
			return ErrInvalid
		}
		if c.Violations != 0 {
			return ErrIntegrity
		}
		checks[c.Name] = true
	}
	if m.MigrationVersion != 0 || len(m.Tables) != 0 {
		if len(guards) != len(GuardNames) || len(checks) != len(CheckNames) {
			return ErrIncomplete
		}
	}
	return nil
}

// Only these old business configuration tables may disappear. In particular,
// channel_events, historical jobs, keys, routes by ID, and new contributed
// market objects are not in this list.
func LegacyConfiguration(table string) bool {
	return slices.Contains([]string{"models", "model_price_tiers", "channel_models", "suppliers", "supplier_endpoints"}, table)
}
func validIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, c := range s {
		if c != '_' && (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}
func validColumns(columns []string) bool {
	seen := map[string]bool{}
	for _, c := range columns {
		if !validIdentifier(c) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return len(columns) > 0
}
func requiredColumns(t Table, columns []string) ([]string, error) {
	for _, c := range columns {
		if !slices.Contains(t.Columns, c) {
			return nil, ErrIncomplete
		}
	}
	return slices.Clone(columns), nil
}

// Plan protects every existing ordinary table unless explicitly authorized as
// old configuration. Channel identity and ciphertext are protected for ALL
// rows, including legacy read-only tombstones; current group configuration has
// a separate stricter rule. Removed legacy channel selections are the sole
// permitted route-row deletion. No user/ledger allowlist can omit a new table.
func Plan(tables []Table) ([]Rule, error) {
	if len(tables) == 0 {
		return []Rule{}, nil
	} // genuinely empty schema only
	seen := map[string]bool{}
	out := []Rule{}
	for _, t := range tables {
		if !validIdentifier(t.Name) || seen[t.Name] || !validColumns(t.Columns) {
			return nil, ErrInvalid
		}
		seen[t.Name] = true
		for _, c := range t.PrimaryKey {
			if !slices.Contains(t.Columns, c) {
				return nil, ErrInvalid
			}
		}
		if t.Name == "goose_db_version" || LegacyConfiguration(t.Name) {
			continue
		}
		r := Rule{Table: t.Name, Scope: "all", Columns: slices.Clone(t.Columns), PrimaryKey: slices.Clone(t.PrimaryKey)}
		switch t.Name {
		case "channels":
			var err error
			r.Columns, err = requiredColumns(t, []string{"id", "owner_id", "name", "api_key_ciphertext", "api_key_nonce", "api_key_key_id", "created_at"})
			if err != nil {
				return nil, err
			}
			r.Scope = "channel_identity"
			out = append(out, r)
			if slices.Contains(t.Columns, "market_group_id") {
				columns, err := requiredColumns(t, []string{"id", "owner_id", "name", "market_group_id", "status", "suspended_reason", "summary", "description", "deleted_at", "created_at", "updated_at"})
				if err != nil {
					return nil, err
				}
				out = append(out, Rule{Table: t.Name, Scope: "group_channel_configuration", Columns: columns, PrimaryKey: slices.Clone(t.PrimaryKey)})
			}
		case "route_pref_channels":
			// Before the group column exists there can be no contributed
			// channel choices; their old selections are cleanup-authorized.
			r.Scope = "group_channel_selections"
			out = append(out, r)
		default:
			out = append(out, r)
		}
	}
	if len(seen) == 1 && seen["goose_db_version"] {
		return []Rule{}, nil
	}
	for _, name := range requiredTables {
		if !seen[name] {
			return nil, ErrIncomplete
		}
	}
	slices.SortFunc(out, func(a, b Rule) int { return strings.Compare(a.Table+"/"+a.Scope, b.Table+"/"+b.Scope) })
	return out, nil
}

type RowHasher struct {
	h      hash.Hash
	rows   int64
	failed bool
}

func NewRowHasher() *RowHasher { return &RowHasher{h: sha256.New()} }

// Add hashes canonical row bytes in primary-key order, with explicit lengths
// so adjacent rows cannot collide by concatenation. Raw rows never leave the
// stream or appear in a preservation manifest.
func (h *RowHasher) Add(row []byte) error {
	if h.failed || len(row) == 0 {
		h.failed = true
		return ErrInvalid
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(row)))
	_, _ = h.h.Write(length[:])
	_, _ = h.h.Write(row)
	h.rows++
	return nil
}
func (h *RowHasher) Finish(rule Rule, streamErr error) (Digest, error) {
	if h.failed || streamErr != nil {
		return Digest{}, ErrIncomplete
	}
	return Digest{Rule: rule, Rows: h.rows, SHA256: hex.EncodeToString(h.h.Sum(nil))}, nil
}
func (m Manifest) Validate() error {
	if m.Format != Format || m.MigrationVersion < 0 || m.Pending.Check() != nil {
		return ErrInvalid
	}
	if err := validateIntegrity(m); err != nil {
		return err
	}
	if err := validateConstraints(m); err != nil {
		return err
	}
	seen := map[string]bool{}
	tables := map[string]bool{}
	for _, d := range m.Tables {
		key := d.Rule.Table + "/" + d.Rule.Scope
		if !validIdentifier(d.Rule.Table) || !validColumns(d.Rule.Columns) || seen[key] || d.Rows < 0 || len(d.SHA256) != 64 {
			return ErrInvalid
		}
		seen[key] = true
		tables[d.Rule.Table] = true
		for _, c := range d.Rule.PrimaryKey {
			if !slices.Contains(d.Rule.Columns, c) {
				return ErrInvalid
			}
		}
		if _, err := hex.DecodeString(d.SHA256); err != nil {
			return ErrInvalid
		}
		if !slices.Contains([]string{"all", "channel_identity", "group_channel_configuration", "group_channel_selections"}, d.Rule.Scope) || LegacyConfiguration(d.Rule.Table) || d.Rule.Table == "goose_db_version" {
			return ErrInvalid
		}
		switch d.Rule.Table {
		case "channels":
			if d.Rule.Scope != "channel_identity" && d.Rule.Scope != "group_channel_configuration" {
				return ErrInvalid
			}
		case "route_pref_channels":
			if d.Rule.Scope != "group_channel_selections" {
				return ErrInvalid
			}
		default:
			if d.Rule.Scope != "all" {
				return ErrInvalid
			}
		}
	}
	if m.MigrationVersion != 0 || len(m.Tables) > 0 {
		for _, name := range requiredTables {
			if !tables[name] {
				return ErrIncomplete
			}
		}
		if !seen["channels/channel_identity"] {
			return ErrIncomplete
		}
	}
	return nil
}
func (m Manifest) SHA256() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type Difference struct {
	Table  string `json:"table"`
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

// Compare checks each account/content row through its ordered digest. A zero
// total balance or unchanged row count cannot hide moved or edited balances.
func Compare(before, after Manifest) ([]Difference, error) {
	if before.Validate() != nil || after.Validate() != nil || after.MigrationVersion < before.MigrationVersion {
		return nil, ErrInvalid
	}
	current := map[string]Digest{}
	for _, d := range after.Tables {
		current[d.Rule.Table+"/"+d.Rule.Scope] = d
	}
	changes := []Difference{}
	constraints := map[string]Constraint{}
	for _, c := range after.Constraints {
		constraints[c.Table+"/"+c.Name] = c
	}
	for _, c := range before.Constraints {
		if constraints[c.Table+"/"+c.Name] != c {
			changes = append(changes, Difference{c.Table, c.Name, "constraint_changed"})
		}
	}
	guards := map[string]Guard{}
	for _, g := range after.Guards {
		guards[g.Name] = g
	}
	for _, g := range before.Guards {
		if guards[g.Name] != g {
			changes = append(changes, Difference{"ledger_guards", g.Name, "definition_changed"})
		}
	}
	for _, a := range before.Tables {
		key := a.Rule.Table + "/" + a.Rule.Scope
		b, ok := current[key]
		if !ok {
			changes = append(changes, Difference{a.Rule.Table, a.Rule.Scope, "missing"})
			continue
		}
		if !slices.Equal(a.Rule.Columns, b.Rule.Columns) || !slices.Equal(a.Rule.PrimaryKey, b.Rule.PrimaryKey) {
			changes = append(changes, Difference{a.Rule.Table, a.Rule.Scope, "projection_changed"})
			continue
		}
		if a.Rows != b.Rows || a.SHA256 != b.SHA256 {
			changes = append(changes, Difference{a.Rule.Table, a.Rule.Scope, "rows_changed"})
		}
	}
	return changes, nil
}
