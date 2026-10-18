package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/marketbrowse"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

func (a *app) registerMarketBrowseRoutes(r *router) {
	r.implement("499", "GET /api/market/models", accessReady, a.listMarketModels)
	r.implement("499", "GET /api/market/models/{modelID}", accessReady, a.getMarketModel)
	r.implement("499", "GET /api/market/suppliers", accessReady, a.listMarketSuppliers)
	r.implement("499", "GET /api/market/groups/{groupID}", accessReady, a.getMarketGroup)
	r.implement("499", "GET /api/market/reports/{reportID}", accessReady, a.getMarketReport)
	r.implement("499", "GET /api/market/channels", accessReady, a.listMarketChannels)
	r.implement("499", "GET /api/market/channels/{channelID}", accessReady, a.getMarketChannel)
}
func (a *app) marketSnapshot(w http.ResponseWriter, r *http.Request) (*marketbrowse.Snapshot, *groupchannel.State, bool) {
	if a.groupChannels == nil || a.groupChannels.Runtime == nil {
		writeError(w, 503, "market_unavailable", "市场快照暂不可用")
		return nil, nil, false
	}
	state, err := a.groupChannels.Runtime.Current()
	if err != nil {
		writeError(w, 503, "market_publication_unavailable", "生效快照尚未发布，请稍后重试")
		return nil, nil, false
	}
	config, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return nil, nil, false
	}
	var prices *typical.PriceSnapshot
	if a.typical != nil {
		prices = a.typical.Prices()
	}
	s, err := marketbrowse.Build(state, prices, config.USDCNYRateNano)
	if err != nil {
		writeDomainError(w, err)
		return nil, nil, false
	}
	return s, state, true
}
func marketQuery(r *http.Request) (marketbrowse.Query, int, error) {
	v := r.URL.Query()
	q := marketbrowse.Query{ModelID: v.Get("model_id"), SupplierID: v.Get("supplier_id"), GroupID: v.Get("group_id"), Search: v.Get("q"), ModelType: v.Get("model_type"), Format: channel.Format(v.Get("format")), Protocol: v.Get("protocol"), Sort: v.Get("sort")}
	if value := v.Get("features"); value != "" {
		q.Features = strings.Split(value, ",")
	}
	if value := v.Get("only_online"); value != "" {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return q, 0, marketbrowse.ErrFilter
		}
		q.OnlyOnline = b
	}
	for key, dest := range map[string]*int64{"min_context": &q.MinContext, "min_output": &q.MinOutput} {
		if value := v.Get(key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return q, 0, marketbrowse.ErrFilter
			}
			*dest = n
		}
	}
	limit := 50
	if value := v.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return q, 0, marketbrowse.ErrFilter
		}
		limit = n
	}
	return q, limit, nil
}
func marketError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, marketbrowse.ErrPageChanged):
		writeError(w, 409, "market_page_changed", "目录、价格或权限已变化，请从第一页重新载入")
	case errors.Is(err, marketbrowse.ErrCursor), errors.Is(err, marketbrowse.ErrFilter):
		writeError(w, 422, "invalid_market_filter", "目录筛选或游标无效")
	default:
		writeDomainError(w, err)
	}
}

type marketModelPage struct {
	marketbrowse.Page
	Suppliers []marketbrowse.Supplier `json:"suppliers"`
	Groups    []marketbrowse.Group    `json:"groups"`
}

func marketParents(s *marketbrowse.Snapshot, models []marketbrowse.Model) ([]marketbrowse.Supplier, []marketbrowse.Group) {
	ss, gs := map[string]bool{}, map[string]bool{}
	for _, m := range models {
		for _, w := range m.Offers {
			ss[w.SupplierID] = true
			gs[w.GroupID] = true
		}
	}
	suppliers, groups := []marketbrowse.Supplier{}, []marketbrowse.Group{}
	for id := range ss {
		p := s.Suppliers[id]
		p.Groups = slices.DeleteFunc(slices.Clone(p.Groups), func(id string) bool { return !gs[id] })
		suppliers = append(suppliers, p)
	}
	for id := range gs {
		groups = append(groups, s.Groups[id])
	}
	slices.SortFunc(suppliers, func(a, b marketbrowse.Supplier) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(groups, func(a, b marketbrowse.Group) int { return strings.Compare(a.ID, b.ID) })
	return suppliers, groups
}
func (a *app) listMarketModels(w http.ResponseWriter, r *http.Request) {
	s, _, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	q, limit, err := marketQuery(r)
	if err != nil {
		marketError(w, err)
		return
	}
	page, err := marketbrowse.Select(s.Offers, marketbrowse.Scope{Version: s.Version, ActorID: accountFromContext(r.Context()).ID}, q, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		marketError(w, err)
		return
	}
	suppliers, groups := marketParents(s, page.Items)
	writeJSON(w, 200, marketModelPage{page, suppliers, groups})
}

type marketRoutingChannel struct {
	TypicalPrice             *marketbrowse.Price       `json:"typical_price"`
	ID                       string                    `json:"id"`
	Name                     string                    `json:"name"`
	IsMine                   bool                      `json:"is_mine"`
	Formats                  []channel.Format          `json:"formats"`
	Multiplier               string                    `json:"multiplier"`
	State                    string                    `json:"state"`
	CooldownRemainingSeconds *int64                    `json:"cooldown_remaining_seconds"`
	RoutingMetrics           []gateway.ModelMetricView `json:"routing_metrics"`
}
type marketModelDetail struct {
	Model           marketbrowse.Model           `json:"model"`
	Offers          []marketOfferJSON            `json:"offers"`
	Channels        []marketbrowse.PublicChannel `json:"channels"`
	Suppliers       []marketbrowse.Supplier      `json:"suppliers"`
	Groups          []marketbrowse.Group         `json:"groups"`
	Routing         routingPreferenceJSON        `json:"routing"`
	RoutingChannels []marketRoutingChannel       `json:"routing_channels"`
}
type marketOfferJSON struct {
	marketbrowse.PublicOffer
	Quote *contributionQuote `json:"quote"`
}

func newMarketOfferJSON(v marketbrowse.PublicOffer) marketOfferJSON {
	out := marketOfferJSON{PublicOffer: v}
	if v.Quote != nil {
		q := changesJSON(market.Changes{Quotes: []market.Quote{*v.Quote}}, nil).Quotes[0]
		out.Quote = &q
	}
	return out
}

func (a *app) getMarketModel(w http.ResponseWriter, r *http.Request) {
	q, _, err := marketQuery(r)
	if err != nil {
		marketError(w, err)
		return
	}
	q.ModelID = r.PathValue("modelID")
	if !gatewaycontract.ValidModelID(q.ModelID) {
		marketError(w, marketbrowse.ErrFilter)
		return
	}
	s, state, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	actor := accountFromContext(r.Context()).ID
	page, err := marketbrowse.Select(s.Offers, marketbrowse.Scope{Version: s.Version, ActorID: actor}, q, "", 1)
	if err != nil {
		marketError(w, err)
		return
	}
	if len(page.Items) == 0 {
		writeError(w, 404, "model_not_found", "没有符合条件的生效模型")
		return
	}
	pref, err := a.routing.Resolve(r.Context(), actor, "", q.ModelID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	detail := marketModelDetail{Model: page.Items[0], Offers: []marketOfferJSON{}, Channels: []marketbrowse.PublicChannel{}, RoutingChannels: []marketRoutingChannel{}, Routing: newRoutingPreferenceJSON(pref)}
	detail.Suppliers, detail.Groups = marketParents(s, page.Items)
	seen, channels := map[string]bool{}, map[string]*marketRoutingChannel{}
	for _, offer := range detail.Model.Offers {
		if !seen[offer.GroupModelID] {
			detail.Offers = append(detail.Offers, newMarketOfferJSON(s.Models[offer.GroupModelID]))
			seen[offer.GroupModelID] = true
		}
		for _, id := range offer.ChannelIDs {
			c := s.Channels[marketbrowse.ChannelKey(id, offer.GroupModelID, offer.Protocol)]
			if state.Views[id].Owner.ID == actor {
				c.Price = &marketbrowse.Price{SelfNetZero: true}
			}
			detail.Channels = append(detail.Channels, c)
			v := channels[id]
			if v == nil {
				v = &marketRoutingChannel{TypicalPrice: c.Price, ID: id, Name: c.Name, IsMine: state.Views[id].Owner.ID == actor, Multiplier: c.Discount, Formats: []channel.Format{}, State: "available", RoutingMetrics: []gateway.ModelMetricView{}}
				channels[id] = v
			}
			if !slices.Contains(v.Formats, offer.Format) {
				v.Formats = append(v.Formats, offer.Format)
			}
		}
	}
	metrics := map[string][]gateway.ModelMetricView{}
	if a.gateway != nil {
		metrics = a.gateway.ModelMetrics(q.ModelID)
		defaults, err := a.settings.Get(r.Context())
		if err != nil {
			writeDomainError(w, err)
			return
		}
		now := time.Now()
		for _, row := range state.Candidates {
			v := channels[row.Candidate.ChannelID]
			if row.ModelID != q.ModelID || v == nil {
				continue
			}
			kind, remaining := a.gateway.State().Describe(v.ID, gateway.EffectiveLimits(row.Candidate.Advanced, defaults), now)
			v.State = string(kind)
			if kind == gateway.StateCooldown {
				seconds := int64(remaining.Seconds()) + 1
				v.CooldownRemainingSeconds = &seconds
			}
		}
	}
	for _, v := range channels {
		v.RoutingMetrics = append(v.RoutingMetrics, metrics[v.ID]...)
		slices.Sort(v.Formats)
		detail.RoutingChannels = append(detail.RoutingChannels, *v)
	}
	slices.SortFunc(detail.RoutingChannels, func(a, b marketRoutingChannel) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, 200, detail)
}
func (a *app) listMarketSuppliers(w http.ResponseWriter, r *http.Request) {
	s, _, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	items := []marketbrowse.Supplier{}
	groups := []marketbrowse.Group{}
	for _, v := range s.Suppliers {
		items = append(items, v)
	}
	for _, v := range s.Groups {
		groups = append(groups, v)
	}
	slices.SortFunc(items, func(a, b marketbrowse.Supplier) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(groups, func(a, b marketbrowse.Group) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, 200, map[string]any{"items": items, "groups": groups})
}
func (a *app) getMarketGroup(w http.ResponseWriter, r *http.Request) {
	s, state, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	g, ok := s.Groups[r.PathValue("groupID")]
	if !ok {
		writeError(w, 404, "group_not_found", "分组不存在或已停用")
		return
	}
	reports := []string{}
	for _, j := range state.Data.Reports {
		_, visibleModel := s.Models[j.Target.Binding.TargetID]
		if j.ReportID != nil && (j.Target.Binding.TargetKind == "group" && j.Target.Binding.TargetID == g.ID || j.Target.Binding.TargetKind == "group_model" && j.Target.GroupID == g.ID && visibleModel) {
			reports = append(reports, *j.ReportID)
		}
	}
	slices.Sort(reports)
	reports = slices.Compact(reports)
	writeJSON(w, 200, map[string]any{"group": g, "supplier": s.Suppliers[g.SupplierID], "reports": reports})
}
