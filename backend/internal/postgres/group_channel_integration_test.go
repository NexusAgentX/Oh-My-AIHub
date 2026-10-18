package postgres_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/groupchannelpg"
)

func TestGroupChannelChoicesLateProbesAndBusinessCascade(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	var accountsBefore, ledgerBefore int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&accountsBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM ledger_entries").Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	change := marketBundle(t, "relay")
	qid := marketID(t)
	change.Quotes = []market.Quote{{ID: qid, GroupModelID: change.Models[0].Value.ID, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(t, "2")}}}
	marketApply(t, s, admin.ID, change)
	d := marketSnapshot(t, s)
	g, m := d.Groups[0], d.Models[0]
	keyring, err := channel.ParseKeyring("fixture="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	id := marketID(t)
	sealed, err := keyring.Encrypt(id, 1, "synthetic-channel-key")
	if err != nil {
		t.Fatal(err)
	}
	repo := groupchannelpg.NewStore(pool)
	r := groupchannel.Record{ID: id, Owner: channel.Owner{ID: admin.ID}, OwnerActive: true, Name: "合成分组渠道", Status: channel.StatusListed, GroupID: g.ID, BaseURL: d.Addresses[0].BaseURL, Revision: 1, CredentialRevision: 1, ProbeEpoch: 1, AddressIDs: []string{d.GroupAddresses[0].ID}, Credential: sealed}
	if err = repo.Create(ctx, r, []groupchannel.Choice{{GroupModelID: m.ID, DiscountNano: 0}}); err != nil {
		t.Fatal(err)
	}
	r, err = repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if opened, e := keyring.Decrypt(id, r.Credential); e != nil || opened != "synthetic-channel-key" {
		t.Fatal("stored credential did not round-trip", e)
	}

	legacy, err := s.Channels.Get(ctx, id)
	if err != nil || legacy.GroupID == nil || *legacy.GroupID != g.ID {
		t.Fatal("legacy management guard cannot identify group channel", err)
	}
	historyID, err := s.Gateway.InsertCall(ctx, gateway.CallInsert{AccountID: admin.ID, RequestedModel: m.ModelID, Format: channel.FormatOpenAIChat, Stream: true, Outcome: gateway.OutcomeInProgress})
	if err != nil {
		t.Fatal(err)
	}
	intermediate, final := false, true
	firstByte, selectionLatency := 10, 150
	snapshot, _ := json.Marshal(map[string]any{"quote_id": qid, "group_id": g.ID})
	_, err = s.Gateway.FinishCall(ctx, gateway.CallFinish{ID: historyID, AccountID: admin.ID, ModelID: m.ModelID, ChannelID: id, Outcome: gateway.OutcomeUnbilled, PriceSnapshot: snapshot, Attempts: []gateway.Attempt{{Channel: &gateway.AttemptChannel{ID: id, Name: r.Name}, EndReason: gateway.EndUpstreamError, SelectionFinal: &intermediate}, {Channel: &gateway.AttemptChannel{ID: id, Name: r.Name}, EndReason: gateway.EndCompleted, SelectionFinal: &final, TTFTMS: &firstByte, SelectionLatencyMS: &selectionLatency}}})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.Observe.ChannelStats(ctx, id, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), time.Now())
	if err != nil || stats.Window.Attempts != 1 || stats.Window.Successes != 1 {
		t.Fatal("addresses multiplied persisted channel health", stats.Window, err)
	}
	before, err := s.Gateway.Points(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := groupchannel.Probe{ChannelID: id, GroupModelID: m.ID, Binding: groupchannel.ProbeBinding(r, g, m), Tests: map[channel.Format]channel.FormatTest{channel.FormatOpenAIChat: {OK: true, TestedAt: time.Now()}}, TestedAt: time.Now()}
	// Synthetic callability metadata tests storage only; no supplier capability
	// report/support is invented by this isolated database fixture.
	saved, err := repo.SaveProbe(ctx, r, g, m, p)
	if err != nil || !saved {
		t.Fatal("probe not saved", err)
	}
	data, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := groupchannel.Build(d, data, nil)
	if err != nil || len(records) != 1 || records[0].Candidate.MultiplierNano != 0 || records[0].Candidate.Quotes == nil {
		t.Fatal("group candidate/zero discount lost", err)
	}
	frozen := records[0].Candidate
	choice := groupchannel.Choice{ChannelID: id, GroupModelID: m.ID, Excluded: true, DiscountNano: 500_000_000}
	if err = repo.Update(ctx, groupchannel.Mutation{Record: r, ExpectedRevision: r.Revision, ReplaceChoices: true, Choices: []groupchannel.Choice{choice}}); err != nil {
		t.Fatal(err)
	}
	if saved, err = repo.SaveProbe(ctx, r, g, m, p); err != nil || !saved {
		t.Fatal("unchanged probe binding should survive exclusion", err)
	}
	data, err = repo.Snapshot(ctx)
	if err != nil || len(data.Choices) != 1 || !data.Choices[0].Excluded {
		t.Fatal("late probe restored excluded model", err)
	}
	current, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	old := current
	current.CredentialRevision++
	current.Credential, err = keyring.Encrypt(id, 1, "rotated-synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Update(ctx, groupchannel.Mutation{Record: current, ExpectedRevision: current.Revision, ReplaceCredential: true}); err != nil {
		t.Fatal(err)
	}
	if saved, err = repo.SaveProbe(ctx, old, g, m, p); err != nil || saved {
		t.Fatal("old credential probe replaced current metadata", err)
	}
	if err = repo.Update(ctx, groupchannel.Mutation{Record: old, ExpectedRevision: old.Revision}); !errors.Is(err, groupchannel.ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	// Stable relationship deletion removes exactly the chosen ID. A future new
	// relationship is not silently selected, even if it has the same URL.
	marketApply(t, s, admin.ID, market.Changes{GroupAddresses: []market.GroupAddressSet{{GroupID: g.ID, Links: []market.GroupAddress{}}}})
	current, err = repo.Get(ctx, id)
	if err != nil || len(current.AddressIDs) != 0 {
		t.Fatal("removed address remained selected", err)
	}
	if len(frozen.Entrances) != 1 || frozen.Quotes.Offers[m.ModelID].Quote.Prices.Input != mustAmount(t, "2") {
		t.Fatal("in-flight execution snapshot changed")
	}
	d = marketSnapshot(t, s)
	g = d.Groups[0]
	now := time.Now().UTC()
	g.DeletedAt = &now
	marketApply(t, s, admin.ID, market.Changes{Groups: []market.Change[market.Group]{{Value: g, BaseRevision: g.Revision}}})
	if _, err = repo.Get(ctx, id); !errors.Is(err, groupchannel.ErrNotFound) {
		t.Fatal("group deletion did not cascade channel", err)
	}

	var historicalModel, historicalChannel, historicalQuote string
	if err = pool.QueryRow(ctx, "SELECT model_id,final_channel_id,price_snapshot->>'quote_id' FROM calls WHERE id=$1", historyID).Scan(&historicalModel, &historicalChannel, &historicalQuote); err != nil {
		t.Fatal(err)
	}
	if historicalModel != m.ModelID || historicalChannel != id || historicalQuote != qid {
		t.Fatal("business cascade rewrote historical call/quote")
	}
	after, err := s.Gateway.Points(ctx, admin.ID)
	if err != nil || before != after {
		t.Fatal("configuration changed account balance", err)
	}
	var accounts, ledgerCount int64
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM ledger_entries").Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if accounts != accountsBefore || ledgerCount != ledgerBefore {
		t.Fatal("configuration touched preserved roots", accounts, ledgerCount)
	}
}
