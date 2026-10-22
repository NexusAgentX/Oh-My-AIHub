package groupchannel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func fixture() (market.Directory, Data) {
	ua := "group-agent"
	rpm := int32(30)
	g := market.Group{ID: "group", SupplierID: "supplier", Name: "group", Enabled: true, Revision: 1, ConnectionVersion: 1, Defaults: market.Defaults{UserAgent: &ua, RPMLimit: &rpm, HeaderRules: &market.HeaderRules{Set: []market.HeaderSet{{Name: "X-Engine", Value: "one"}}}}}
	qid := "quote"
	m := market.GroupModel{ID: "offering", GroupID: g.ID, ModelID: "vendor/model:1", UpstreamModel: "native/model:1", ModelType: "text", DisplayName: "display", Enabled: true, Revision: 1, QuoteID: &qid}
	m.CapabilityDigest = market.ModelCapabilityDigest(m)
	d := market.Directory{Suppliers: []market.Supplier{{ID: "supplier", Name: "supplier", Enabled: true}}, Groups: []market.Group{g}, Models: []market.GroupModel{m}, Addresses: []market.Address{{ID: "address-one", BaseURL: "https://one.example"}, {ID: "address-two", BaseURL: "https://two.example"}}, GroupAddresses: []market.GroupAddress{{ID: "entrance-one", GroupID: g.ID, AddressID: "address-one"}, {ID: "entrance-two", GroupID: g.ID, AddressID: "address-two", Position: 1}}, Quotes: []market.Quote{{ID: qid, GroupModelID: m.ID, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.Amount(money.Scale), Output: 2 * money.Amount(money.Scale)}}}}
	r := Record{ID: "channel", Owner: channel.Owner{ID: "owner"}, OwnerActive: true, Name: "channel", GroupID: g.ID, Status: channel.StatusListed, AddressIDs: []string{"entrance-two", "entrance-one"}, Revision: 1, CredentialRevision: 1, ProbeEpoch: 1}
	p := Probe{ChannelID: r.ID, GroupModelID: m.ID, Binding: ProbeBinding(r, g, m), Tests: map[channel.Format]channel.FormatTest{channel.FormatOpenAIChat: {OK: true, TestedAt: time.Now()}}, TestedAt: time.Now()}
	return d, Data{Channels: []Record{r}, Probes: []Probe{p}}
}
func TestDynamicInheritanceSelectionAndImmutableQuotes(t *testing.T) {
	d, data := fixture()
	data.Choices = []Choice{{ChannelID: "channel", GroupModelID: "offering", DiscountNano: 0}}
	records, views, err := Build(d, data)
	if err != nil || len(records) != 1 {
		t.Fatal(err, records)
	}
	first := records[0].Candidate
	if first.Entrances[0].ID != "entrance-two" || first.Entrances[1].ID != "entrance-one" || first.MultiplierNano != 0 {
		t.Fatal("selection or free discount lost")
	}
	if first.Quotes.Offers["vendor/model:1"].DiscountNano == nil || *first.Quotes.Offers["vendor/model:1"].DiscountNano != 0 {
		t.Fatal("offer discount not frozen")
	}
	if views["channel"].Sources["rpm_limit"] != "group" {
		t.Fatal("inheritance or invented evidence")
	}
	oldBinding := data.Probes[0].Binding
	rpm := int32(75)
	d.Groups[0].Defaults.RPMLimit = &rpm
	d.Groups[0].Revision++
	d.Groups[0].ConnectionVersion++
	d.Quotes[0].Prices.Input = 3 * money.Amount(money.Scale)
	records, _, err = Build(d, data)
	if err != nil || len(records) != 1 || *records[0].Candidate.Advanced.RPMLimit != 75 {
		t.Fatal("default update did not reach channel", err)
	}
	if first.Quotes.Offers["vendor/model:1"].Quote.Prices.Input != money.Amount(money.Scale) {
		t.Fatal("in-flight quote mutated")
	}
	if oldBinding != ProbeBinding(data.Channels[0], d.Groups[0], d.Models[0]) {
		t.Fatal("unrelated default/price edit invalidated probe")
	}
	override := int32(9)
	data.Channels[0].Overrides.RPMLimit = &override
	data.Channels[0].Overrides.HeaderRules = &market.HeaderRules{}
	records, views, err = Build(d, data)
	// The channel's own UA/header rules are not part of the binding: the next
	// probe uses them, the result already stored stays valid.
	if err != nil || len(records) != 1 || views["channel"].Models[0].ProbeState != "available" || ProbeBinding(data.Channels[0], d.Groups[0], d.Models[0]) != oldBinding {
		t.Fatal("changed headers invalidated key evidence")
	}
	if len(views["channel"].Effective.HeaderRules.Set) != 0 || *views["channel"].Effective.RPMLimit != 9 || views["channel"].Sources["header_rules"] != "channel" {
		t.Fatal("explicit empty block or override lost")
	}
}
func TestExclusionsNewModelsDeletedEntrancesAndLateEvidence(t *testing.T) {
	d, data := fixture()
	r := data.Channels[0]
	g := d.Groups[0]
	m := d.Models[0]
	binding := ProbeBinding(r, g, m)
	for _, change := range []func(*Record){func(v *Record) { v.CredentialRevision++ }, func(v *Record) { v.ProbeEpoch++ }, func(v *Record) { v.ID = "other-channel" }} {
		next := r
		change(&next)
		if ProbeBinding(next, g, m) == binding {
			t.Fatal("stale probe remained usable")
		}
	}
	// Request headers and the model fields a probe does not cover leave the
	// binding alone; a model field it does cover invalidates it.
	for _, change := range []func(*Record){func(v *Record) { s := "changed"; v.Overrides.UserAgent = &s }, func(v *Record) { v.Overrides.HeaderRules = &market.HeaderRules{} }} {
		next := r
		change(&next)
		if ProbeBinding(next, g, m) != binding {
			t.Fatal("request headers invalidated the probe")
		}
	}
	window := int64(1 << 20)
	for _, change := range []func(*market.GroupModel){func(v *market.GroupModel) { v.UpstreamModel = "native/other:1" }, func(v *market.GroupModel) { v.ContextWindow = &window }} {
		next := m
		change(&next)
		next.CapabilityDigest = market.ModelCapabilityDigest(next)
		if ProbeBinding(r, g, next) == binding {
			t.Fatal("model change kept the probe binding")
		}
	}
	data.Choices = []Choice{{ChannelID: r.ID, GroupModelID: m.ID, Excluded: true, DiscountNano: ledger.UnitRate}}
	records, views, err := Build(d, data)
	if err != nil || len(records) != 0 || !views[r.ID].Models[0].Excluded {
		t.Fatal("probe overrode exclusion", err)
	}
	next := m
	next.ID = "new-offering"
	next.ModelID = "new-model"
	next.UpstreamModel = "new-model"
	next.QuoteID = nil
	d.Models = append(d.Models, next)
	state := &State{Directory: d, Data: data}
	pending := pendingModels(state, r)
	if len(pending) != 1 || pending[0].ID != next.ID {
		t.Fatal("new model was not queued or excluded model restored")
	}
	data.Choices = nil
	d.GroupAddresses = nil
	records, views, err = Build(d, data)
	if err != nil || len(records) != 0 || views[r.ID].Available || views[r.ID].Reason != "no_selected_address" {
		t.Fatal("removed selection still routed", err)
	}
}
func TestHeadersAndTimeoutInheritanceKeepsNullDistinct(t *testing.T) {
	d, data := fixture()
	override := market.Defaults{HeaderRules: &market.HeaderRules{}}
	effective, sources := ResolveDefaults(override, d.Groups[0].Defaults)
	if effective.HeaderRules == nil || len(effective.HeaderRules.Set) > 0 || sources["ttft_timeout_ms"] != "model_route_or_platform" {
		t.Fatal("empty block/inheritance lost")
	}
	effective.HeaderRules.Remove = append(effective.HeaderRules.Remove, "X-Test")
	if data.Channels[0].Overrides.HeaderRules != nil || len(d.Groups[0].Defaults.HeaderRules.Remove) > 0 {
		t.Fatal("inheritance mutated source")
	}
	if CompatibleHeaders(override, d.Groups[0].Defaults) {
		t.Fatal("changed headers borrowed group evidence")
	}
	if !CompatibleHeaders(market.Defaults{}, d.Groups[0].Defaults) {
		t.Fatal("inherited headers rejected")
	}
}

// bindingV2 is the payload the probe binding carried before the fingerprint was
// narrowed to the model's own fields. Stored evidence from it must not match:
// the version string is what retires it.
func bindingV2(r Record, g market.Group, m market.GroupModel) string {
	effective, _ := ResolveDefaults(r.Overrides, g.Defaults)
	payload := struct {
		Version                   string
		Channel, Group, Model     string
		CredentialRevision, Epoch int64
		GroupDigest, ModelDigest  string
		UA                        *string
		Headers                   *market.HeaderRules
	}{"group-channel-basic.v2", r.ID, g.ID, m.ID, r.CredentialRevision, r.ProbeEpoch, "", m.CapabilityDigest, effective.UserAgent, effective.HeaderRules}
	b, _ := json.Marshal(payload)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

func TestProbeBindingVersionRetiresStoredEvidence(t *testing.T) {
	d, data := fixture()
	r, g, m := data.Channels[0], d.Groups[0], d.Models[0]
	binding := ProbeBinding(r, g, m)
	if binding != ProbeBinding(r, g, m) {
		t.Fatal("the binding is not stable for the same inputs")
	}
	if binding == bindingV2(r, g, m) {
		t.Fatal("evidence stored under the previous fingerprint still matches")
	}
	// Every channel and model goes back to pending once: this is how the whole
	// fleet is re-probed after the upgrade, without a data migration.
	pending := pendingModels(&State{Directory: d, Data: Data{Channels: data.Channels, Probes: []Probe{{ChannelID: r.ID, GroupModelID: m.ID, Binding: bindingV2(r, g, m)}}, Choices: []Choice{{ChannelID: r.ID, GroupModelID: m.ID}}}}, r)
	if len(pending) != 1 || pending[0].ID != m.ID {
		t.Fatalf("a v2 probe did not return to pending: %+v", pending)
	}
}
