package quotepricing

import "encoding/json"

// Domain money remains integral nano units. Public snapshots use exact decimal
// strings like historical bills, never JSON numbers or floating point.
type snapshotTokenPrices struct {
	Input      string            `json:"input"`
	Output     string            `json:"output"`
	CacheWrite string            `json:"cache_write"`
	CacheRead  string            `json:"cache_read"`
	Details    map[string]string `json:"details"`
}

func (line Line) MarshalJSON() ([]byte, error) {
	type fields Line
	var prices *snapshotTokenPrices
	if p := line.Prices; p != nil {
		prices = &snapshotTokenPrices{Input: p.Input.String(), Output: p.Output.String(), CacheWrite: p.CacheWrite.String(), CacheRead: p.CacheRead.String(), Details: map[string]string{}}
		for key, value := range p.Details {
			prices.Details[key] = value.String()
		}
	}
	var unitPrice *string
	if line.UnitPrice != nil {
		value := line.UnitPrice.String()
		unitPrice = &value
	}
	return json.Marshal(struct {
		fields
		Cost      string               `json:"cost"`
		Prices    *snapshotTokenPrices `json:"selected_prices,omitempty"`
		UnitPrice *string              `json:"unit_price,omitempty"`
	}{fields: fields(line), Cost: line.Cost.String(), Prices: prices, UnitPrice: unitPrice})
}
func (statement Statement) MarshalJSON() ([]byte, error) {
	type fields Statement
	return json.Marshal(struct {
		fields
		Cost string `json:"cost"`
		Fee  string `json:"fee"`
	}{fields: fields(statement), Cost: statement.Cost.String(), Fee: statement.Fee.String()})
}
