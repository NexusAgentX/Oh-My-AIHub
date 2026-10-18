package typical

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

type offering struct {
	model  market.GroupModel
	book   *quotepricing.Book
	digest string
}

func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func InputVersion(in Input, c Config) string {
	return digest(struct {
		Models     []market.GroupModel
		Quotes     []market.Quote
		Groups     []string
		Governance []market.ModelRule
		Channels   []Channel
		FX, Fee    int64
		Config     Config
	}{in.Directory.Models, in.Directory.Quotes, activeGroups(in.Directory), in.Directory.Governance, in.Channels, in.USDCNYNano, in.FeeRateNano, c})
}
func activeGroups(d market.Directory) []string {
	suppliers := map[string]bool{}
	for _, s := range d.Suppliers {
		if s.Enabled && s.DeletedAt == nil {
			suppliers[s.ID] = true
		}
	}
	groups := []string{}
	for _, g := range d.Groups {
		if g.Enabled && g.DeletedAt == nil && suppliers[g.SupplierID] {
			groups = append(groups, g.ID)
		}
	}
	sort.Strings(groups)
	return groups
}
func offerings(in Input) (map[string]offering, map[string]string, error) {
	quotes := map[string]market.Quote{}
	for _, q := range in.Directory.Quotes {
		quotes[q.ID] = q
	}
	disabled := map[string]bool{}
	for _, r := range in.Directory.Governance {
		disabled[r.ModelID] = r.Disabled
	}
	active := map[string]bool{}
	for _, id := range activeGroups(in.Directory) {
		active[id] = true
	}
	books := map[string]quotepricing.Book{}
	models := map[string]market.GroupModel{}
	refs := map[string]string{}
	for _, m := range in.Directory.Models {
		if !active[m.GroupID] || !m.Enabled || m.DeletedAt != nil || disabled[m.ModelID] || m.QuoteID == nil {
			continue
		}
		q, ok := quotes[*m.QuoteID]
		if !ok || q.GroupModelID != m.ID {
			return nil, nil, ErrInvalid
		}
		b := books[m.GroupID]
		if b.GroupID == "" {
			b = quotepricing.Book{GroupID: m.GroupID, GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{}}
		}
		b.Offers[m.ModelID] = quotepricing.Offer{ModelID: m.ModelID, UpstreamModel: m.UpstreamModel, Revision: m.Revision, Quote: q}
		books[m.GroupID] = b
		models[m.ID] = m
		if m.IsReference {
			if refs[m.ModelID] != "" {
				return nil, nil, ErrInvalid
			}
			refs[m.ModelID] = m.ID
		}
	}
	frozen := map[string]*quotepricing.Book{}
	for id, b := range books {
		p, err := quotepricing.Freeze(b)
		if err != nil {
			return nil, nil, err
		}
		frozen[id] = p
	}
	out := map[string]offering{}
	for id, m := range models {
		b := frozen[m.GroupID]
		out[id] = offering{model: m, book: b, digest: PricingVersion(b)}
	}
	return out, refs, nil
}
func currentSample(s Sample, o offering) bool {
	if s.GroupID != o.model.GroupID || s.ModelID != o.model.ModelID {
		return false
	}
	main := false
	for _, ref := range s.References {
		p, ok := o.book.Offers[ref.ModelID]
		if !ok || p.Quote.ID != ref.QuoteID || p.Quote.Version != ref.QuoteVersion {
			return false
		}
		main = main || ref.ModelID == s.ModelID
	}
	return main
}

type calculationBudget struct{ remaining int }

func (b *calculationBudget) price(ctx context.Context, o offering, s Sample, fx int64) (money.Amount, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	b.remaining--
	if b.remaining < 0 {
		return 0, false, ErrSaturated
	}
	facts := s.Facts
	facts.Units = append([]quotepricing.Unit(nil), facts.Units...)
	for i := range facts.Units {
		u := &facts.Units[i]
		if u.Unit == "tool_session" || u.Unit == "tool_second" {
			u.ReceiptKey = fmt.Sprintf("verified-%d", i)
		}
	}
	statement, err := quotepricing.Calculate(o.book, o.model.ModelID, facts, s.At, ledger.UnitRate, fx, 0)
	if err != nil {
		return 0, false, nil
	}
	if len(statement.Lines) == 0 {
		return 0, false, nil
	}
	for _, line := range statement.Lines {
		if line.State != "priced" {
			return 0, false, nil
		}
	}
	return statement.Cost, true, nil
}
func add(a *int64, b int64) bool {
	if b < 0 || *a > math.MaxInt64-b {
		return false
	}
	*a += b
	return true
}
func workload(samples []Sample) (Workload, error) {
	w := Workload{Tokens: map[string]int64{}, ServiceTiers: map[string]int64{}, Units: map[string]int64{}}
	for _, s := range samples {
		u := s.Facts.Usage
		if !add(&w.Requests, 1) || !add(&w.InputTokens, u.InputTokens) || !add(&w.OutputTokens, u.OutputTokens) || !add(&w.CacheWriteTokens, u.CacheWriteTokens) || !add(&w.CacheReadTokens, u.CacheReadTokens) {
			return w, ErrSaturated
		}
		if u.Detail != nil {
			for key, n := range u.Detail.Tokens {
				v := w.Tokens[key]
				if !add(&v, n) {
					return w, ErrSaturated
				}
				w.Tokens[key] = v
			}
			tier := u.Detail.ServiceTier
			if tier == "" {
				tier = "unspecified"
			}
			w.ServiceTiers[tier]++
		} else {
			w.ServiceTiers["unspecified"]++
		}
		for _, unit := range s.Facts.Units {
			if unit.Unit == "successful_image" && unit.Tool == "" && unit.ModelID == "" {
				if !add(&w.ImagesNano, unit.QuantityNano) {
					return w, ErrSaturated
				}
			}
			key := unit.Tool + "/" + unit.Unit
			v := w.Units[key]
			if !add(&v, unit.QuantityNano) {
				return w, ErrSaturated
			}
			w.Units[key] = v
		}
	}
	return w, nil
}
func denominator(w Workload, typ string, c Config) (string, int64, *big.Int) {
	if typ == "image" {
		return "successful_images", c.TypicalImages, new(big.Int).SetInt64(w.ImagesNano)
	}
	sum := new(big.Int)
	for _, n := range []int64{w.InputTokens, w.OutputTokens, w.CacheWriteTokens, w.CacheReadTokens} {
		sum.Add(sum, big.NewInt(n))
	}
	return "tokens", c.TypicalTokens, sum
}
func decimalRatio(n, d *big.Int) *string {
	if d.Sign() <= 0 {
		return nil
	}
	s := new(big.Rat).SetFrac(n, d).FloatString(9)
	return &s
}
func scaled(n, d *big.Int, target int64, nanoQuantity bool) *string {
	if d.Sign() <= 0 {
		return nil
	}
	numerator := new(big.Int).Mul(n, big.NewInt(target))
	if nanoQuantity {
		numerator.Mul(numerator, big.NewInt(ledger.UnitRate))
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(numerator, d, r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return nil
	}
	s := money.FromNano(q.Int64()).String()
	return &s
}
func derived(value *string, multiplier int64) *string {
	if value == nil {
		return nil
	}
	amount, err := money.Parse(*value)
	if err != nil || multiplier < 0 {
		return nil
	}
	return scaled(big.NewInt(amount.Nano()), big.NewInt(ledger.UnitRate), multiplier, false)
}
func sumPrices(ctx context.Context, b *calculationBudget, o offering, samples []Sample, fx int64) (*big.Int, bool, error) {
	sum := new(big.Int)
	for _, s := range samples {
		price, ok, err := b.price(ctx, o, s, fx)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		sum.Add(sum, big.NewInt(price.Nano()))
	}
	return sum, true, nil
}
func estimate(ctx context.Context, b *calculationBudget, o offering, reference *offering, samples []Sample, own int, in Input, c Config, source string) (Estimate, error) {
	q := o.book.Offers[o.model.ModelID].Quote
	row := Estimate{Level: "group", ModelID: o.model.ModelID, GroupID: o.model.GroupID, GroupModelID: o.model.ID, QuoteID: q.ID, QuoteVersion: q.Version, PricingVersion: o.digest, State: "unknown", Reason: "no_samples", Samples: len(samples), OwnSamples: own, Source: source}
	row.RequestQuantity = c.TypicalRequests
	w, err := workload(samples)
	if err != nil {
		return row, err
	}
	row.Workload = w
	row.Unit, row.Quantity, _ = denominator(w, o.model.ModelType, c)
	if len(samples) == 0 {
		return row, nil
	}
	_, _, denom := denominator(w, o.model.ModelType, c)
	cost, ok, err := sumPrices(ctx, b, o, samples, in.USDCNYNano)
	if err != nil {
		return row, err
	}
	if !ok {
		row.Reason = "unpriced_workload_component"
		return row, nil
	}
	row.RequestCost = scaled(cost, big.NewInt(w.Requests), c.TypicalRequests, false)
	if denom.Sign() == 0 {
		row.Reason = "missing_reliable_conversion"
		return row, nil
	}
	row.Cost = scaled(cost, denom, row.Quantity, o.model.ModelType == "image")
	row.Fee = derived(row.Cost, in.FeeRateNano)
	if row.Cost == nil || row.Fee == nil {
		row.Reason = "estimate_overflow"
		return row, nil
	}
	row.State = "estimated"
	row.Reason = ""
	prompt := new(big.Int).Add(big.NewInt(w.InputTokens), big.NewInt(w.CacheWriteTokens))
	prompt.Add(prompt, big.NewInt(w.CacheReadTokens))
	row.CacheReadShare = decimalRatio(big.NewInt(w.CacheReadTokens), prompt)
	if reference == nil {
		row.Reason = "no_reference"
		return row, nil
	}
	row.ReferenceGroupModelID = reference.model.ID
	refCost, ok, err := sumPrices(ctx, b, *reference, samples, in.USDCNYNano)
	if err != nil {
		return row, err
	}
	if !ok {
		row.Reason = "reference_workload_unpriced"
		return row, nil
	}
	row.ReferenceCost = scaled(refCost, denom, row.Quantity, o.model.ModelType == "image")
	row.Ratio = decimalRatio(cost, refCost)
	if row.Ratio == nil {
		row.Reason = "zero_reference"
	}
	return row, nil
}

// Calculate fixes one input/config/sample waterline. It sums costs and units,
// retaining each call's context/tier/time/image/tool distribution before any
// normalization; it never averages prices, cache percentages or sample ratios.
func Calculate(ctx context.Context, in Input, c Config, samples []Sample, at time.Time) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(in.Directory.Models) > 4096 || len(in.Directory.Quotes) > 4096 || len(in.Channels) > 20000 {
		return nil, ErrSaturated
	}
	if Validate(c) != nil || len(samples) > MaxSamples {
		return nil, ErrInvalid
	}
	offers, refs, err := offerings(in)
	if err != nil {
		return nil, err
	}
	budget := &calculationBudget{remaining: MaxCalculations}
	global := map[string][]Sample{}
	for _, s := range samples {
		if !validSample(s) {
			return nil, ErrInvalid
		}
		global[s.ModelID] = append(global[s.ModelID], s)
	}
	snap := &Snapshot{Version: "typical.v1", USDCNYNano: in.USDCNYNano, FeeRateNano: in.FeeRateNano, Config: c, InputVersion: InputVersion(in, c), UpdatedAt: at, WindowStart: at.AddDate(0, 0, -c.WindowDays), Items: []Estimate{}}
	groupRows := map[string]Estimate{}
	groupNames, supplierNames := map[string]string{}, map[string]string{}
	for _, supplier := range in.Directory.Suppliers {
		supplierNames[supplier.ID] = supplier.Name
	}
	groupSuppliers := map[string]string{}
	for _, group := range in.Directory.Groups {
		groupNames[group.ID] = group.Name
		groupSuppliers[group.ID] = supplierNames[group.SupplierID]
	}
	for id, o := range offers {
		own := []Sample{}
		for _, s := range global[o.model.ModelID] {
			if currentSample(s, o) {
				own = append(own, s)
			}
		}
		profile, source := own, "group_current_quote"
		if len(own) < c.MinSamples {
			profile, source = global[o.model.ModelID], "model_site_fallback"
		}
		var ref *offering
		if r, ok := offers[refs[o.model.ModelID]]; ok {
			ref = &r
		}
		row, err := estimate(ctx, budget, o, ref, profile, len(own), in, c, source)
		if err != nil {
			return nil, err
		}
		row.GroupName = groupNames[row.GroupID]
		row.SupplierName = groupSuppliers[row.GroupID]
		groupRows[id] = row
		snap.Items = append(snap.Items, row)
	}
	for _, ch := range in.Channels {
		for _, o := range offers {
			if o.model.GroupID != ch.GroupID || o.model.ModelID != ch.ModelID {
				continue
			}
			row := groupRows[o.model.ID]
			row.Level = "channel"
			row.ChannelID = ch.ID
			row.ChannelName = ch.Name
			row.Cost = derived(row.Cost, ch.DiscountNano)
			row.RequestCost = derived(row.RequestCost, ch.DiscountNano)
			row.Fee = derived(row.Cost, in.FeeRateNano)
			if row.Ratio != nil {
				n, ok := new(big.Rat).SetString(*row.Ratio)
				if ok {
					n.Mul(n, new(big.Rat).SetFrac(big.NewInt(ch.DiscountNano), big.NewInt(ledger.UnitRate)))
					value := n.FloatString(9)
					row.Ratio = &value
				}
			}
			row.PricingVersion = digest(struct {
				Group    string
				Discount int64
			}{row.PricingVersion, ch.DiscountNano})
			snap.Items = append(snap.Items, row)
		}
	}
	for model, profile := range global {
		var example *offering
		sum := new(big.Int)
		count := 0
		for _, s := range profile {
			var matched *offering
			for _, o := range offers {
				if o.model.GroupID == s.GroupID && o.model.ModelID == model {
					copy := o
					matched = &copy
					break
				}
			}
			if matched == nil {
				continue
			}
			value, ok, err := budget.price(ctx, *matched, s, in.USDCNYNano)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			sum.Add(sum, big.NewInt(value.Nano()))
			count++
			if example == nil {
				example = matched
			}
		}
		if example == nil {
			continue
		}
		active := []Sample{}
		for _, s := range profile {
			for _, o := range offers {
				if o.model.GroupID == s.GroupID && o.model.ModelID == model {
					active = append(active, s)
					break
				}
			}
		}
		var ref *offering
		if r, ok := offers[refs[model]]; ok {
			ref = &r
		}
		row, err := estimate(ctx, budget, *example, ref, active, len(active), in, c, "site_active_groups_weighted")
		if err != nil {
			return nil, err
		}
		row.Level = "model"
		row.GroupName = ""
		row.SupplierName = ""
		row.GroupID = ""
		row.GroupModelID = ""
		row.QuoteID = ""
		row.QuoteVersion = 0
		row.PricingVersion = snap.InputVersion
		// The numerator follows each sample's current group quote; the reference
		// uses these exact same samples, never a different cache workload.
		_, _, denom := denominator(row.Workload, example.model.ModelType, c)
		if count != len(active) {
			row.State = "unknown"
			row.Reason = "unpriced_workload_component"
			row.Cost = nil
			row.Fee = nil
			row.Ratio = nil
		} else {
			row.Cost = scaled(sum, denom, row.Quantity, example.model.ModelType == "image")
			row.RequestCost = scaled(sum, big.NewInt(row.Workload.Requests), c.TypicalRequests, false)
			row.Fee = derived(row.Cost, in.FeeRateNano)
			if row.Cost != nil && row.Fee != nil {
				row.State = "estimated"
				row.Reason = ""
			} else {
				row.State = "unknown"
				row.Reason = "missing_reliable_conversion"
			}
			if ref != nil {
				refSum, ok, err := sumPrices(ctx, budget, *ref, active, in.USDCNYNano)
				if err != nil {
					return nil, err
				}
				if ok {
					row.Ratio = decimalRatio(sum, refSum)
					row.ReferenceCost = scaled(refSum, denom, row.Quantity, example.model.ModelType == "image")
					if row.Ratio == nil {
						row.Reason = "zero_reference"
					}
				}
			} else {
				row.Reason = "no_reference"
			}
		}
		snap.Items = append(snap.Items, row)
	}
	// Models without a single reliable sample still have an explicit unknown row.
	seen := map[string]bool{}
	for _, row := range snap.Items {
		if row.Level == "model" {
			seen[row.ModelID] = true
		}
	}
	for _, o := range offers {
		if !seen[o.model.ModelID] {
			row := groupRows[o.model.ID]
			row.Level = "model"
			row.GroupName = ""
			row.SupplierName = ""
			row.GroupID = ""
			row.GroupModelID = ""
			row.QuoteID = ""
			row.QuoteVersion = 0
			row.Source = "site_no_samples"
			snap.Items = append(snap.Items, row)
			seen[o.model.ModelID] = true
		}
	}
	sort.Slice(snap.Items, func(i, j int) bool {
		a, b := snap.Items[i], snap.Items[j]
		return a.ModelID+"\x00"+a.Level+"\x00"+a.GroupID+"\x00"+a.ChannelID < b.ModelID+"\x00"+b.Level+"\x00"+b.GroupID+"\x00"+b.ChannelID
	})
	return snap, nil
}

// PricingVersion matches an immutable candidate book without channel discounts,
// display revisions, address order or connection metadata changing the price.
func PricingVersion(book *quotepricing.Book) string {
	if book == nil {
		return ""
	}
	refs := map[string]quotepricing.Reference{}
	for id, o := range book.Offers {
		refs[id] = quotepricing.Reference{ModelID: id, GroupModelID: o.Quote.GroupModelID, QuoteID: o.Quote.ID, QuoteVersion: o.Quote.Version}
	}
	return digest(struct {
		GroupID    string
		References map[string]quotepricing.Reference
	}{book.GroupID, refs})
}

// PricingVersions uses the same active quote references as the estimator.
// Billing books may also retain excluded/disabled companion quotes, so callers
// must not hash those broader books into a comparison estimate's version.
func PricingVersions(directory market.Directory) (map[string]string, error) {
	rows, _, err := offerings(Input{Directory: directory})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, row := range rows {
		out[row.model.GroupID] = row.digest
	}
	return out, nil
}
