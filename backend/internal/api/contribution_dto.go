package api

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
)

type contributionPrices struct {
	Input      string            `json:"input"`
	Output     string            `json:"output"`
	CacheWrite string            `json:"cache_write"`
	CacheRead  string            `json:"cache_read"`
	Details    map[string]string `json:"details"`
}
type contributionTier struct {
	Name       string             `json:"name"`
	Conditions market.Conditions  `json:"conditions"`
	Prices     contributionPrices `json:"prices"`
}
type contributionUnit struct {
	Unit            string `json:"unit"`
	Tool            string `json:"tool"`
	ImageSize       string `json:"image_size"`
	ImageQuality    string `json:"image_quality"`
	ImageBackground string `json:"image_background"`
	ServiceTier     string `json:"service_tier"`
	Price           string `json:"price"`
}
type contributionQuote struct {
	ID           string             `json:"id"`
	GroupModelID string             `json:"group_model_id"`
	Version      int64              `json:"version"`
	Currency     string             `json:"currency"`
	BillingUnit  string             `json:"billing_unit"`
	Prices       contributionPrices `json:"prices"`
	Tiers        []contributionTier `json:"tiers"`
	UnitPrices   []contributionUnit `json:"unit_prices"`
	CreatedAt    time.Time          `json:"created_at"`
}
type contributionChanges struct {
	Suppliers      []market.Change[market.Supplier]   `json:"suppliers"`
	Groups         []market.Change[market.Group]      `json:"groups"`
	Addresses      []market.Change[market.Address]    `json:"addresses"`
	GroupAddresses []market.GroupAddressSet           `json:"group_addresses"`
	Models         []market.Change[market.GroupModel] `json:"models"`
	Quotes         []contributionQuote                `json:"quotes"`
	Governance     []market.Change[market.ModelRule]  `json:"governance"`
}
type contributionDraft struct {
	ID            string              `json:"id"`
	OwnerID       string              `json:"owner_id"`
	Title         string              `json:"title"`
	Kind          string              `json:"kind"`
	TargetID      string              `json:"target_id"`
	State         string              `json:"state"`
	Revision      int64               `json:"revision"`
	Changes       contributionChanges `json:"changes"`
	Base          contributionChanges `json:"base"`
	ReportIDs     []string            `json:"report_ids"`
	TaskIDs       []string            `json:"task_ids"`
	PendingPrices []string            `json:"pending_prices"`
	ReviewReason  string              `json:"review_reason"`
	ReviewedBy    *string             `json:"reviewed_by"`
	SubmittedAt   *time.Time          `json:"submitted_at"`
	PublishedAt   *time.Time          `json:"published_at"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

func pricesJSON(p market.TokenPrices, blank bool) contributionPrices {
	out := contributionPrices{Input: p.Input.String(), Output: p.Output.String(), CacheWrite: p.CacheWrite.String(), CacheRead: p.CacheRead.String(), Details: map[string]string{}}
	if blank {
		out.Input = ""
		out.Output = ""
		out.CacheWrite = ""
		out.CacheRead = ""
	}
	for k, v := range p.Details {
		out.Details[k] = v.String()
	}
	return out
}
func changesJSON(c market.Changes, pending []string, blanks ...map[string][]string) contributionChanges {
	out := contributionChanges{Suppliers: append([]market.Change[market.Supplier]{}, c.Suppliers...), Groups: append([]market.Change[market.Group]{}, c.Groups...), Addresses: append([]market.Change[market.Address]{}, c.Addresses...), Models: append([]market.Change[market.GroupModel]{}, c.Models...), GroupAddresses: append([]market.GroupAddressSet{}, c.GroupAddresses...), Governance: append([]market.Change[market.ModelRule]{}, c.Governance...), Quotes: []contributionQuote{}}
	for _, q := range c.Quotes {
		blank := len(blanks) == 0 && slices.Contains(pending, q.GroupModelID)
		v := contributionQuote{ID: q.ID, GroupModelID: q.GroupModelID, Version: q.Version, Currency: q.Currency, BillingUnit: q.BillingUnit, Prices: pricesJSON(q.Prices, blank), Tiers: []contributionTier{}, UnitPrices: []contributionUnit{}, CreatedAt: q.CreatedAt}
		for _, t := range q.Tiers {
			v.Tiers = append(v.Tiers, contributionTier{Name: t.Name, Conditions: t.Conditions, Prices: pricesJSON(t.Prices, false)})
		}
		for _, u := range q.UnitPrices {
			v.UnitPrices = append(v.UnitPrices, contributionUnit{Unit: u.Unit, Tool: u.Tool, ImageSize: u.ImageSize, ImageQuality: u.ImageQuality, ImageBackground: u.ImageBackground, ServiceTier: u.ServiceTier, Price: u.Price.String()})
		}
		if len(blanks) > 0 {
			maskQuoteBlanks(&v, blanks[0][q.GroupModelID])
		}
		out.Quotes = append(out.Quotes, v)
	}
	return out
}
func draftJSON(d contribution.Draft) contributionDraft {
	return contributionDraft{ID: d.ID, OwnerID: d.OwnerID, Title: d.Title, Kind: d.Kind, TargetID: d.TargetID, State: d.State, Revision: d.Revision, Changes: changesJSON(d.Changes, d.PendingPrices, d.PriceBlanks), Base: changesJSON(d.Base, nil), ReportIDs: append([]string{}, d.ReportIDs...), TaskIDs: append([]string{}, d.TaskIDs...), PendingPrices: append([]string{}, d.PendingPrices...), ReviewReason: d.ReviewReason, ReviewedBy: d.ReviewedBy, SubmittedAt: d.SubmittedAt, PublishedAt: d.PublishedAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}
func (c contributionChanges) domain() (market.Changes, []string, error) {
	out := market.Changes{Suppliers: c.Suppliers, Groups: c.Groups, Addresses: c.Addresses, GroupAddresses: c.GroupAddresses, Models: c.Models, Governance: c.Governance, Quotes: []market.Quote{}}
	for i := range out.Suppliers {
		if out.Suppliers[i].Value.IconData != "" {
			icon, err := supplier.NormalizeIcon(out.Suppliers[i].Value.IconData)
			if err != nil {
				return out, nil, err
			}
			out.Suppliers[i].Value.IconData = icon
		}
	}
	declared := []string{}
	for _, q := range c.Quotes {
		complete := true
		parse := func(raw string, required bool) (money.Amount, error) {
			if raw == "" {
				if required {
					complete = false
				}
				return 0, nil
			}
			v, err := money.Parse(raw)
			if err != nil || v < 0 || v > market.MaxPrice {
				return 0, contribution.ErrInvalid
			}
			return v, nil
		}
		parsePrices := func(p contributionPrices, required bool) (market.TokenPrices, error) {
			v := market.TokenPrices{Details: map[string]money.Amount{}}
			pairs := []struct {
				raw string
				to  *money.Amount
			}{{p.Input, &v.Input}, {p.Output, &v.Output}, {p.CacheWrite, &v.CacheWrite}, {p.CacheRead, &v.CacheRead}}
			for _, pair := range pairs {
				amount, err := parse(pair.raw, required)
				if err != nil {
					return v, err
				}
				*pair.to = amount
			}
			for k, raw := range p.Details {
				amount, err := parse(raw, true)
				if err != nil {
					return v, err
				}
				v.Details[k] = amount
			}
			return v, nil
		}
		v := market.Quote{ID: q.ID, GroupModelID: q.GroupModelID, Version: q.Version, Currency: q.Currency, BillingUnit: q.BillingUnit, CreatedAt: q.CreatedAt, Tiers: []market.Tier{}, UnitPrices: []market.UnitPrice{}}
		var err error
		v.Prices, err = parsePrices(q.Prices, q.BillingUnit == "per_million_tokens")
		if err != nil {
			return out, nil, err
		}
		for _, t := range q.Tiers {
			p, err := parsePrices(t.Prices, true)
			if err != nil {
				return out, nil, err
			}
			v.Tiers = append(v.Tiers, market.Tier{Name: t.Name, Conditions: t.Conditions, Prices: p})
		}
		primary := q.BillingUnit == "per_million_tokens"
		for _, u := range q.UnitPrices {
			price, err := parse(u.Price, true)
			if err != nil {
				return out, nil, err
			}
			v.UnitPrices = append(v.UnitPrices, market.UnitPrice{Unit: u.Unit, Tool: u.Tool, ImageSize: u.ImageSize, ImageQuality: u.ImageQuality, ImageBackground: u.ImageBackground, ServiceTier: u.ServiceTier, Price: price})
			if u.Tool == "" && (q.BillingUnit == "per_request" && u.Unit == "request" || q.BillingUnit == "per_successful_image" && u.Unit == "successful_image") {
				primary = true
			}
		}
		out.Quotes = append(out.Quotes, v)
		if complete && primary {
			declared = append(declared, q.GroupModelID)
		}
	}
	return out, declared, nil
}

// Price presence belongs to the private proposal. Persisting it separately
// preserves an explicit zero and each unfinished cell across draft saves.
func (c contributionChanges) priceBlanks() map[string][]string {
	out := map[string][]string{}
	for _, q := range c.Quotes {
		paths := []string{}
		prices := func(prefix string, p contributionPrices) {
			for _, v := range []struct{ key, raw string }{{"input", p.Input}, {"output", p.Output}, {"cache_write", p.CacheWrite}, {"cache_read", p.CacheRead}} {
				if v.raw == "" {
					paths = append(paths, prefix+"/"+v.key)
				}
			}
			for k, v := range p.Details {
				if v == "" {
					paths = append(paths, prefix+"/details/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1"))
				}
			}
		}
		prices("/prices", q.Prices)
		for i, t := range q.Tiers {
			prices("/tiers/"+strconv.Itoa(i)+"/prices", t.Prices)
		}
		for i, u := range q.UnitPrices {
			if u.Price == "" {
				paths = append(paths, "/unit_prices/"+strconv.Itoa(i)+"/price")
			}
		}
		out[q.GroupModelID] = paths
	}
	return out
}
func maskQuoteBlanks(q *contributionQuote, paths []string) {
	if len(paths) == 0 {
		return
	}
	data, _ := json.Marshal(q)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if decoder.Decode(&root) != nil {
		return
	}
	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		node := root
		for i, part := range parts {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch v := node.(type) {
			case map[string]any:
				if i == len(parts)-1 {
					if _, ok := v[part]; ok {
						v[part] = ""
					}
				} else {
					node = v[part]
				}
			case []any:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(v) {
					node = nil
					continue
				}
				node = v[index]
			default:
				node = nil
			}
		}
	}
	data, _ = json.Marshal(root)
	_ = json.Unmarshal(data, q)
}
