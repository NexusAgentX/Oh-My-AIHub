package gateway

// Lookup uses one immutable estimator generation for the entire decision.
// Quote/header/discount/FX updates cannot borrow an old workload price. There
// is no JSON hashing, directory scan or SQL on this request path.
func (e *Engine) applyTypicalPrices(candidates []Candidate, model string, fx int64) {
	var view TypicalPriceLookup
	if e.TypicalPrices != nil {
		view = e.TypicalPrices()
	}
	for i := range candidates {
		candidate := &candidates[i]
		candidate.TypicalCostKnown = false
		if view == nil || candidate.GroupID == "" || candidate.PricingVersion == "" {
			continue
		}
		candidate.TypicalCost, candidate.TypicalUnit, candidate.TypicalQuantity, candidate.TypicalCostKnown = view.LookupComparison(candidate.ChannelID, model, candidate.GroupID, candidate.PricingVersion, candidate.MultiplierNano, fx)
	}
}

func comparableCandidatePrices(pool []Candidate) bool {
	unit := ""
	var quantity int64
	for _, candidate := range pool {
		if !candidate.TypicalCostKnown || candidate.GroupID == "" {
			continue
		}
		if candidate.TypicalUnit == "" || candidate.TypicalQuantity <= 0 {
			return false
		}
		if unit == "" {
			unit, quantity = candidate.TypicalUnit, candidate.TypicalQuantity
			continue
		}
		if candidate.TypicalUnit != unit || candidate.TypicalQuantity != quantity {
			return false
		}
	}
	return true
}
