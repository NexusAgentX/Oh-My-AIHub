// Package marketbrowse performs account-scoped queries over approved offering
// snapshots. Model rows are derived views, never persistent catalog entities.
package marketbrowse

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var ErrFilter = errors.New("invalid catalog filter")
var ErrCursor = errors.New("invalid catalog cursor")
var ErrPageChanged = errors.New("catalog scope changed; reload first page")

type Price struct {
	Amount      money.Amount `json:"amount"`
	Unit        string       `json:"unit"`
	Quantity    int64        `json:"quantity"`
	SelfNetZero bool         `json:"self_net_zero"`
}

func (p Price) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Amount      string `json:"amount"`
		Unit        string `json:"unit"`
		Quantity    int64  `json:"quantity"`
		SelfNetZero bool   `json:"self_net_zero"`
	}{p.Amount.String(), p.Unit, p.Quantity, p.SelfNetZero})
}

type Channel struct {
	ID, OwnerID                string
	Visible, Enabled, Routable bool
	Price                      *Price
}
type Offering struct {
	Protocol                                              string
	ModelID, GroupModelID, GroupID, SupplierID, ModelType string
	Visible, Enabled                                      bool
	Format                                                channel.Format
	Channels                                              []Channel
}
type Query struct {
	ModelID    string         `json:"model_id"`
	SupplierID string         `json:"supplier_id"`
	GroupID    string         `json:"group_id"`
	Protocol   string         `json:"protocol"`
	Search     string         `json:"search"`
	ModelType  string         `json:"model_type"`
	Format     channel.Format `json:"format"`
	OnlyOnline bool           `json:"only_online"`
	Sort       string         `json:"sort"`
}
type Scope struct {
	// Version must cover the approved offering/candidate/price snapshot
	// generation. The caller also changes it when publication fails.
	Version       string
	ActorID       string
	KeyID         string
	AllowedModels []string
}

// Only public identifiers and measurements leave the selector. Raw records,
// credentials, URLs, headers, upstream names and request bodies are absent.
type Witness struct {
	Protocol         string         `json:"protocol"`
	GroupModelID     string         `json:"group_model_id"`
	GroupID          string         `json:"group_id"`
	SupplierID       string         `json:"supplier_id"`
	Format           channel.Format `json:"format"`
	ChannelIDs       []string       `json:"channel_ids"`
	OnlineChannelIDs []string       `json:"online_channel_ids"`
}
type Model struct {
	Protocols      []string         `json:"protocols"`
	ID             string           `json:"id"`
	ModelTypes     []string         `json:"model_types"`
	Formats        []channel.Format `json:"formats"`
	Offers         []Witness        `json:"offers"`
	OnlineChannels int              `json:"online_channels"`
	Price          *Price           `json:"price"`
}
type Page struct {
	Items             []Model `json:"items"`
	Total             int     `json:"total"`
	NextCursor        *string `json:"next_cursor"`
	PriceSortFallback bool    `json:"price_sort_fallback"`
}
type cursor struct{ Scope, After string }

func (q Query) validate() bool {
	if q.ModelID != "" && !gatewaycontract.ValidModelID(q.ModelID) || len(q.SupplierID) > 128 || len(q.GroupID) > 128 {
		return false
	}
	if q.Protocol != "" && !slices.Contains([]string{"openai-chat-completions", "openai-responses", "native-messages", "native-generate-content", "openai-images"}, q.Protocol) {
		return false
	}
	if len(q.Search) > 256 || q.ModelType != "" && q.ModelType != "text" && q.ModelType != "image" || q.Sort != "" && q.Sort != "id" && q.Sort != "price" {
		return false
	}
	if q.Format != "" && !q.Format.Valid() && q.Format != channel.FormatOpenAIImages {
		return false
	}
	return true
}

func validProtocol(protocol string, format channel.Format) bool {
	switch protocol {
	case "":
		return format == ""
	case "openai-chat-completions":
		return format == channel.FormatOpenAIChat
	case "openai-responses":
		return format == channel.FormatOpenAIResponses
	case "native-messages":
		return format == channel.FormatAnthropic
	case "native-generate-content":
		return format == channel.FormatGemini
	case "openai-images":
		return format == channel.FormatOpenAIImages
	}
	return false
}
func validPrice(p *Price) bool {
	return p != nil && p.Amount >= 0 && !p.SelfNetZero && p.Unit != "" && p.Quantity > 0
}
func comparable(a, b *Price) bool {
	return a.SelfNetZero || b.SelfNetZero || a.Unit == b.Unit && a.Quantity == b.Quantity
}
func priceLess(a, b *Price) bool {
	if a.SelfNetZero != b.SelfNetZero {
		return a.SelfNetZero
	}
	return a.Amount < b.Amount
}

// Select filters permissions, governance and protocol on each witness before
// grouping, counting, sorting or pagination. No union of unrelated groups,
// protocols or channels can create a matching witness.
func Select(offers []Offering, scope Scope, q Query, after string, limit int) (Page, error) {
	if limit < 1 || limit > 100 {
		return Page{Items: []Model{}}, ErrFilter
	}
	return selectPage(offers, scope, q, after, limit)
}

// FilterAll shares the exact same-candidate and authorization rules with the
// model catalog, for in-process channel projections which paginate afterwards.
// It never exposes an unbounded HTTP model page.
func FilterAll(offers []Offering, scope Scope, q Query) (Page, error) {
	return selectPage(offers, scope, q, "", max(1, len(offers)))
}
func selectPage(offers []Offering, scope Scope, q Query, after string, limit int) (Page, error) {
	page := Page{Items: []Model{}}
	if scope.Version == "" || scope.ActorID == "" || !q.validate() || limit < 1 {
		return page, ErrFilter
	}
	q.Search = strings.TrimSpace(q.Search)
	allowed := slices.Clone(scope.AllowedModels)
	slices.Sort(allowed)
	raw, _ := json.Marshal(struct {
		Scope Scope
		Query Query
		Limit int
	}{Scope: Scope{Version: scope.Version, ActorID: scope.ActorID, KeyID: scope.KeyID, AllowedModels: allowed}, Query: q, Limit: limit})
	sum := sha256.Sum256(raw)
	binding := hex.EncodeToString(sum[:])
	resume := ""
	if after != "" {
		if len(after) > 4096 {
			return page, ErrCursor
		}
		raw, err := base64.RawURLEncoding.DecodeString(after)
		var c cursor
		if err != nil || json.Unmarshal(raw, &c) != nil || !gatewaycontract.ValidModelID(c.After) {
			return page, ErrCursor
		}
		if c.Scope != binding {
			return page, ErrPageChanged
		}
		resume = c.After
	}
	type aggregate struct {
		model  Model
		online map[string]bool
		prices []*Price
	}
	models := map[string]*aggregate{}
	for _, offer := range offers {
		if q.ModelID != "" && q.ModelID != offer.ModelID || q.SupplierID != "" && q.SupplierID != offer.SupplierID || q.GroupID != "" && q.GroupID != offer.GroupID {
			continue
		}
		if !offer.Visible || !offer.Enabled || !validProtocol(offer.Protocol, offer.Format) || offer.GroupModelID == "" || offer.GroupID == "" || offer.SupplierID == "" || offer.ModelType != "text" && offer.ModelType != "image" || !gatewaycontract.ValidModelID(offer.ModelID) || len(allowed) > 0 && !slices.Contains(allowed, offer.ModelID) || q.ModelType != "" && offer.ModelType != q.ModelType || q.Format != "" && offer.Format != q.Format || q.Protocol != "" && offer.Protocol != q.Protocol || q.Search != "" && !strings.Contains(strings.ToLower(offer.ModelID), strings.ToLower(q.Search)) {
			continue
		}
		w := Witness{GroupModelID: offer.GroupModelID, GroupID: offer.GroupID, SupplierID: offer.SupplierID, Format: offer.Format, Protocol: offer.Protocol, ChannelIDs: []string{}, OnlineChannelIDs: []string{}}
		var prices []*Price
		for _, c := range offer.Channels {
			if !c.Visible || !c.Enabled || c.ID == "" || offer.Format == "" {
				continue
			}
			w.ChannelIDs = append(w.ChannelIDs, c.ID)
			if !c.Routable {
				continue
			}
			w.OnlineChannelIDs = append(w.OnlineChannelIDs, c.ID)
			if c.OwnerID == scope.ActorID {
				prices = append(prices, &Price{SelfNetZero: true})
			} else if validPrice(c.Price) {
				p := *c.Price
				p.SelfNetZero = false
				prices = append(prices, &p)
			}
		}
		if q.OnlyOnline && len(w.OnlineChannelIDs) == 0 {
			continue
		}
		a := models[offer.ModelID]
		if a == nil {
			a = &aggregate{model: Model{ID: offer.ModelID, ModelTypes: []string{}, Formats: []channel.Format{}, Protocols: []string{}, Offers: []Witness{}}, online: map[string]bool{}}
			models[offer.ModelID] = a
		}
		slices.Sort(w.ChannelIDs)
		w.ChannelIDs = slices.Compact(w.ChannelIDs)
		slices.Sort(w.OnlineChannelIDs)
		w.OnlineChannelIDs = slices.Compact(w.OnlineChannelIDs)
		a.model.Offers = append(a.model.Offers, w)
		a.model.ModelTypes = append(a.model.ModelTypes, offer.ModelType)
		if offer.Format != "" {
			a.model.Formats = append(a.model.Formats, offer.Format)
			a.model.Protocols = append(a.model.Protocols, offer.Protocol)
		}
		for _, id := range w.OnlineChannelIDs {
			a.online[id] = true
		}
		a.prices = append(a.prices, prices...)
	}
	items := make([]Model, 0, len(models))
	for _, a := range models {
		slices.Sort(a.model.ModelTypes)
		a.model.ModelTypes = slices.Compact(a.model.ModelTypes)
		slices.Sort(a.model.Formats)
		a.model.Formats = slices.Compact(a.model.Formats)
		slices.Sort(a.model.Protocols)
		a.model.Protocols = slices.Compact(a.model.Protocols)
		slices.SortFunc(a.model.Offers, func(a, b Witness) int {
			if n := cmp.Compare(a.SupplierID, b.SupplierID); n != 0 {
				return n
			}
			if n := cmp.Compare(a.GroupID, b.GroupID); n != 0 {
				return n
			}
			if n := cmp.Compare(a.GroupModelID, b.GroupModelID); n != 0 {
				return n
			}
			return cmp.Compare(a.Protocol, b.Protocol)
		})
		a.model.OnlineChannels = len(a.online)
		var price *Price
		incompatible := false
		for _, p := range a.prices {
			if p.SelfNetZero {
				price = p
				incompatible = false
				break
			}
			if price == nil {
				price = p
				continue
			}
			if !comparable(price, p) {
				incompatible = true
				continue
			}
			if priceLess(p, price) {
				price = p
			}
		}
		if !incompatible {
			a.model.Price = price
		}
		items = append(items, a.model)
	}
	if q.Sort == "price" {
		var unit *Price
		for _, row := range items {
			if row.Price == nil || row.Price.SelfNetZero {
				continue
			}
			if unit == nil {
				unit = row.Price
			} else if !comparable(unit, row.Price) {
				page.PriceSortFallback = true
				break
			}
		}
	}
	slices.SortFunc(items, func(a, b Model) int {
		if q.Sort == "price" {
			freeA, freeB := a.Price != nil && a.Price.SelfNetZero, b.Price != nil && b.Price.SelfNetZero
			if freeA != freeB {
				if freeA {
					return -1
				}
				return 1
			}
			if !page.PriceSortFallback {
				if (a.Price != nil) != (b.Price != nil) {
					if a.Price != nil {
						return -1
					}
					return 1
				}
				if a.Price != nil {
					if n := cmp.Compare(a.Price.Amount, b.Price.Amount); n != 0 {
						return n
					}
				}
			}
		}
		return cmp.Compare(a.ID, b.ID)
	})
	page.Total = len(items)
	start := 0
	if resume != "" {
		start = slices.IndexFunc(items, func(m Model) bool { return m.ID == resume })
		if start < 0 {
			return Page{Items: []Model{}}, ErrCursor
		}
		start++
	}
	end := min(start+limit, len(items))
	page.Items = items[start:end]
	if end < len(items) {
		raw, _ := json.Marshal(cursor{Scope: binding, After: items[end-1].ID})
		token := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &token
	}
	return page, nil
}
