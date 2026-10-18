package typical

import "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"

type costKey struct{ channel, model string }
type costValue struct {
	group, version string
	unit           string
	quantity       int64
	discount       int64
	cost           money.Amount
}
type costIndex struct {
	fx     int64
	values map[costKey]costValue
}

// PriceSnapshot is immutable and obtained once for a routing decision. All
// candidates then use the same workload denominator and config generation.
type PriceSnapshot = costIndex

func (s *Service) Prices() *PriceSnapshot { return s.prices.Load() }

func (s *Service) install(snap *Snapshot, in Input) {
	groups := map[string]string{}
	for _, row := range snap.Items {
		if row.Level == "group" {
			groups[row.GroupID+"\x00"+row.ModelID] = row.PricingVersion
		}
	}
	channels := map[costKey]Channel{}
	for _, ch := range in.Channels {
		channels[costKey{ch.ID, ch.ModelID}] = ch
	}
	index := &costIndex{fx: in.USDCNYNano, values: map[costKey]costValue{}}
	for _, row := range snap.Items {
		if row.Level != "channel" || row.State != "estimated" || row.Cost == nil {
			continue
		}
		key := costKey{row.ChannelID, row.ModelID}
		ch, ok := channels[key]
		if !ok {
			continue
		}
		cost, err := money.Parse(*row.Cost)
		if err != nil {
			continue
		}
		index.values[key] = costValue{group: ch.GroupID, version: groups[ch.GroupID+"\x00"+ch.ModelID], discount: ch.DiscountNano, cost: cost, unit: row.Unit, quantity: row.Quantity}
	}
	s.current.Store(snap)
	s.prices.Store(index)
}

// Lookup performs one immutable map read and no SQL, JSON work or allocation.
// F10 supplies the frozen candidate's price version, discount and current FX;
// stale quotes/discounts/rates remain unknown until background recomputation.
func (s *Service) Lookup(channel, model, group, pricingVersion string, discount, fx int64) (money.Amount, bool) {
	value, _, _, ok := s.Prices().LookupComparison(channel, model, group, pricingVersion, discount, fx)
	return value, ok
}

func (index *costIndex) LookupComparison(channel, model, group, pricingVersion string, discount, fx int64) (money.Amount, string, int64, bool) {
	if index == nil || index.fx != fx {
		return 0, "", 0, false
	}
	value, ok := index.values[costKey{channel, model}]
	return value.cost, value.unit, value.quantity, ok && value.group == group && value.version == pricingVersion && value.discount == discount && value.unit != "" && value.quantity > 0
}
