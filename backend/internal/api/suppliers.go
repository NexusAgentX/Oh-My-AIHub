package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
)

type supplierJSON struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary"`
	Description string    `json:"description"`
	WebsiteURL  string    `json:"website_url"`
	DocsURL     string    `json:"docs_url"`
	StatusURL   string    `json:"status_url"`
	IconData    string    `json:"icon_data"`
	IconSource  string    `json:"icon_source"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type supplierEndpointJSON struct {
	BaseURL string `json:"base_url"`
	Label   string `json:"label"`
}
type adminSupplierJSON struct {
	supplierJSON
	Visible        bool                   `json:"visible"`
	Endpoints      []supplierEndpointJSON `json:"endpoints"`
	LinkedChannels int                    `json:"linked_channels"`
}
type supplierInput struct {
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	WebsiteURL  string                 `json:"website_url"`
	DocsURL     string                 `json:"docs_url"`
	StatusURL   string                 `json:"status_url"`
	IconData    string                 `json:"icon_data"`
	IconSource  string                 `json:"icon_source"`
	Visible     bool                   `json:"visible"`
	Endpoints   []supplierEndpointJSON `json:"endpoints"`
}
type supplierListJSON struct {
	Items      []supplierJSON `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}
type adminSupplierListJSON struct {
	Items      []adminSupplierJSON `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}
type supplierRecognitionJSON struct {
	Supplier *supplierJSON `json:"supplier"`
}
type supplierIconJSON struct {
	IconData   string `json:"icon_data"`
	IconSource string `json:"icon_source"`
}
type hallOfferJSON struct {
	Model               catalogModelJSON `json:"model"`
	Multiplier          string           `json:"multiplier"`
	EffectiveMultiplier string           `json:"effective_multiplier"`
	FXDiscount          bool             `json:"fx_discount"`
}
type hallChannelJSON struct {
	ID                       string           `json:"id"`
	Name                     string           `json:"name"`
	Summary                  string           `json:"summary"`
	Description              string           `json:"description"`
	Owner                    partyJSON        `json:"owner"`
	IsMine                   bool             `json:"is_mine"`
	Supplier                 *supplierJSON    `json:"supplier"`
	Models                   []hallOfferJSON  `json:"models"`
	Formats                  []channel.Format `json:"formats"`
	State                    string           `json:"state"`
	CooldownRemainingSeconds *int64           `json:"cooldown_remaining_seconds"`
	SuccessRate24h           *string          `json:"success_rate_24h"`
	TTFTP50MS                *int64           `json:"ttft_p50_ms"`
}
type hallListJSON struct {
	Items      []hallChannelJSON `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}
type supplierDetailJSON struct {
	Supplier supplierJSON       `json:"supplier"`
	Channels []hallChannelJSON  `json:"channels"`
	Models   []catalogModelJSON `json:"models"`
}

func newSupplierJSON(value supplier.Supplier) supplierJSON {
	return supplierJSON{ID: value.ID, Name: value.Name, Kind: value.Kind, Summary: value.Summary, Description: value.Description, WebsiteURL: value.WebsiteURL, DocsURL: value.DocsURL, StatusURL: value.StatusURL, IconData: value.IconData, IconSource: value.IconSource, UpdatedAt: value.UpdatedAt}
}
func newAdminSupplierJSON(value supplier.Supplier, listings []supplier.Listing) adminSupplierJSON {
	item := adminSupplierJSON{supplierJSON: newSupplierJSON(value), Visible: value.Visible, Endpoints: []supplierEndpointJSON{}}
	for _, endpoint := range value.Endpoints {
		item.Endpoints = append(item.Endpoints, supplierEndpointJSON{BaseURL: endpoint.BaseURL, Label: endpoint.Label})
		for _, listing := range listings {
			if listing.BaseURL == endpoint.BaseURL {
				item.LinkedChannels++
			}
		}
	}
	return item
}
func directoryPage[T any](r *http.Request, items []T, id func(T) string) ([]T, *string, error) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return nil, nil, supplier.ErrInvalid
		}
	}
	slices.SortFunc(items, func(a, b T) int { return strings.Compare(id(a), id(b)) })
	after := r.URL.Query().Get("cursor")
	page := make([]T, 0, limit)
	for _, item := range items {
		if id(item) <= after {
			continue
		}
		if len(page) == limit {
			next := id(page[len(page)-1])
			return page, &next, nil
		}
		page = append(page, item)
	}
	return page, nil, nil
}
func (a *app) listSuppliers(w http.ResponseWriter, r *http.Request) {
	values, err := a.suppliers.Store.List(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := []supplierJSON{}
	for _, value := range values {
		if value.Visible {
			items = append(items, newSupplierJSON(value))
		}
	}
	page, next, err := directoryPage(r, items, func(v supplierJSON) string { return v.ID })
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, supplierListJSON{Items: page, NextCursor: next})
}
func (a *app) adminSuppliers(w http.ResponseWriter, r *http.Request) {
	values, err := a.suppliers.Store.List(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	listings, err := a.suppliers.Store.Listings(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := []adminSupplierJSON{}
	for _, value := range values {
		items = append(items, newAdminSupplierJSON(value, listings))
	}
	page, next, err := directoryPage(r, items, func(v adminSupplierJSON) string { return v.ID })
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, adminSupplierListJSON{Items: page, NextCursor: next})
}
func (a *app) saveSupplier(w http.ResponseWriter, r *http.Request) {
	var input supplierInput
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	value := supplier.Supplier{ID: r.PathValue("supplierID"), Name: input.Name, Kind: input.Kind, Summary: input.Summary, Description: input.Description, WebsiteURL: input.WebsiteURL, DocsURL: input.DocsURL, StatusURL: input.StatusURL, IconData: input.IconData, IconSource: input.IconSource, Visible: input.Visible, Endpoints: []supplier.Endpoint{}}
	for _, endpoint := range input.Endpoints {
		value.Endpoints = append(value.Endpoints, supplier.Endpoint{BaseURL: endpoint.BaseURL, Label: endpoint.Label})
	}
	create := r.Method == http.MethodPost
	if !create && !uuidPattern.MatchString(value.ID) {
		writeDomainError(w, supplier.ErrNotFound)
		return
	}
	value, err := a.suppliers.Save(r.Context(), accountFromContext(r.Context()).ID, value, create)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := 200
	if create {
		status = 201
	}
	listings, err := a.suppliers.Store.Listings(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, status, newAdminSupplierJSON(value, listings))
}
func (a *app) recognizeSupplier(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BaseURL string `json:"base_url"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	base, err := a.suppliers.Outbound.NormalizeBaseURL(input.BaseURL)
	if err != nil {
		writeDomainError(w, supplier.ErrInvalid)
		return
	}
	values, err := a.suppliers.Store.List(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	result := supplierRecognitionJSON{}
	if value := supplier.Match(values, base); value != nil {
		item := newSupplierJSON(*value)
		result.Supplier = &item
	}
	writeJSON(w, 200, result)
}
func (a *app) fetchSupplierIcon(w http.ResponseWriter, r *http.Request) {
	if !a.channelProbes.take(accountFromContext(r.Context()).ID) {
		writeError(w, 429, "rate_limited", "请求过于频繁，请稍后重试")
		return
	}
	var input struct {
		WebsiteURL string `json:"website_url"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	// Apply the current administrator-maintained egress block list too.
	settings, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	policy, err := a.suppliers.Outbound.WithExtraBlockedHosts(settings.ExtraBlockedHosts)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	service := supplier.Service{Outbound: policy}
	data, source, err := service.FetchIcon(r.Context(), input.WebsiteURL)
	if err != nil {
		code, message := supplierIconError(err)
		writeError(w, 422, code, message)
		return
	}
	writeJSON(w, 200, supplierIconJSON{IconData: data, IconSource: source})
}

// Directory data is read from current stores. It never enters the gateway's
// routing snapshot and never changes how an upstream request is sent.
type hallView struct {
	suppliers []supplier.Supplier
	channels  []hallChannelJSON
	online    []gateway.OnlineChannel
	catalog   map[string]catalog.Model
	rate      int64
}

func (a *app) loadHall(ctx context.Context) (hallView, error) {
	view := hallView{channels: []hallChannelJSON{}, catalog: map[string]catalog.Model{}}
	values, err := a.suppliers.Store.List(ctx)
	if err != nil {
		return view, err
	}
	view.suppliers = values
	listings, err := a.suppliers.Store.Listings(ctx)
	if err != nil {
		return view, err
	}
	online, err := a.browse.OnlineChannels(ctx)
	if err != nil {
		return view, err
	}
	models, err := a.catalog.List(ctx, false)
	if err != nil {
		return view, err
	}
	for _, model := range models {
		view.catalog[model.ID] = model
	}
	defaults, _, err := a.gateway.Settings(ctx)
	if err != nil {
		return view, err
	}
	view.rate = defaults.USDCNYRateNano
	now := time.Now()
	stats := a.gateway.Stats()
	byChannel := map[string][]gateway.OnlineChannel{}
	for _, entry := range online {
		if _, ok := view.catalog[entry.ModelID]; ok || entry.Custom != nil {
			byChannel[entry.ChannelID] = append(byChannel[entry.ChannelID], entry)
		}
	}
	for _, listing := range listings {
		item := hallChannelJSON{ID: listing.ID, Name: listing.Name, Summary: listing.Summary, Description: listing.Description, Owner: partyJSON{ID: listing.OwnerID, DisplayName: listing.OwnerName}, IsMine: listing.OwnerID == accountFromContext(ctx).ID, Models: []hallOfferJSON{}, Formats: []channel.Format{}}
		if value := supplier.Match(values, listing.BaseURL); value != nil {
			public := newSupplierJSON(*value)
			item.Supplier = &public
		}
		state, remaining := a.gateway.State().Describe(listing.ID, gateway.EffectiveLimits(listing.Advanced, defaults), now)
		item.State = string(state)
		if state == gateway.StateCooldown {
			seconds := int64(remaining.Seconds()) + 1
			item.CooldownRemainingSeconds = &seconds
		}
		if health, ok := stats[listing.ID]; ok && health.Attempts > 0 {
			rate := ratio(health.SuccessRate())
			item.SuccessRate24h = &rate
			if health.TTFTP50MS != nil {
				ms := int64(*health.TTFTP50MS)
				item.TTFTP50MS = &ms
			}
		}
		for _, entry := range byChannel[listing.ID] {
			model := entryModel(view.catalog[entry.ModelID], entry)
			model.ID = entry.ModelID
			summary := newCatalogModelJSON(model, []gateway.OnlineChannel{entry}, now, view.rate)
			if entry.Custom != nil {
				summary = newCustomModelJSON(entry.ModelID, []gateway.OnlineChannel{entry})
			}
			pricing := newChannelPricing(model, entry.MultiplierNano, entry.FXDiscount, view.rate)
			item.Models = append(item.Models, hallOfferJSON{Model: summary, Multiplier: money.FromNano(entry.MultiplierNano).String(), EffectiveMultiplier: money.FromNano(pricing.EffectiveNano).String(), FXDiscount: pricing.FXDiscount})
			for _, format := range entry.Formats {
				if !slices.Contains(item.Formats, format) {
					item.Formats = append(item.Formats, format)
				}
			}
			view.online = append(view.online, entry)
		}
		slices.SortFunc(item.Models, func(a, b hallOfferJSON) int { return strings.Compare(a.Model.ID, b.Model.ID) })
		view.channels = append(view.channels, item)
	}
	return view, nil
}
func (a *app) listHall(w http.ResponseWriter, r *http.Request) {
	view, err := a.loadHall(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := []hallChannelJSON{}
	query := r.URL.Query()
	needle := strings.ToLower(strings.TrimSpace(query.Get("q")))
	for _, item := range view.channels {
		if needle != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Summary+" "+item.Owner.DisplayName), needle) {
			continue
		}
		if id := query.Get("supplier"); id != "" && (item.Supplier == nil || item.Supplier.ID != id) {
			continue
		}
		model, format := query.Get("model"), channel.Format(query.Get("format"))
		if (model != "" || format != "") && !slices.ContainsFunc(item.Models, func(offer hallOfferJSON) bool {
			return (model == "" || offer.Model.ID == model) && (format == "" || slices.Contains(offer.Model.Formats, format))
		}) {
			continue
		}
		items = append(items, item)
	}
	page, next, err := directoryPage(r, items, func(v hallChannelJSON) string { return v.ID })
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, hallListJSON{Items: page, NextCursor: next})
}
func (a *app) getHallChannel(w http.ResponseWriter, r *http.Request) {
	view, err := a.loadHall(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	for _, item := range view.channels {
		if item.ID == r.PathValue("channelID") {
			writeJSON(w, 200, item)
			return
		}
	}
	writeDomainError(w, supplier.ErrNotFound)
}
func (a *app) getSupplier(w http.ResponseWriter, r *http.Request) {
	view, err := a.loadHall(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	id := r.PathValue("supplierID")
	var selected *supplier.Supplier
	for _, value := range view.suppliers {
		if value.ID == id && value.Visible {
			selected = &value
			break
		}
	}
	if selected == nil {
		writeDomainError(w, supplier.ErrNotFound)
		return
	}
	result := supplierDetailJSON{Supplier: newSupplierJSON(*selected), Channels: []hallChannelJSON{}, Models: []catalogModelJSON{}}
	channelIDs := map[string]bool{}
	for _, item := range view.channels {
		if item.Supplier != nil && item.Supplier.ID == id {
			result.Channels = append(result.Channels, item)
			channelIDs[item.ID] = true
		}
	}
	entries := []gateway.OnlineChannel{}
	for _, entry := range view.online {
		if channelIDs[entry.ChannelID] {
			entries = append(entries, entry)
		}
	}
	for modelID, offers := range groupByModel(entries) {
		if model, ok := view.catalog[modelID]; ok {
			result.Models = append(result.Models, newCatalogModelJSON(model, offers, time.Now(), view.rate))
		} else if custom := customOnline(offers); len(custom) > 0 {
			result.Models = append(result.Models, newCustomModelJSON(modelID, custom))
		}
	}
	slices.SortFunc(result.Models, func(a, b catalogModelJSON) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, 200, result)
}

type supplierImpactJSON struct {
	LinkedChannels int `json:"linked_channels"`
}

func (a *app) previewSupplierImpact(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Endpoints []supplierEndpointJSON `json:"endpoints"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	if len(input.Endpoints) > 32 {
		writeDomainError(w, supplier.ErrInvalid)
		return
	}
	addresses := map[string]bool{}
	for _, endpoint := range input.Endpoints {
		base, err := a.suppliers.Outbound.NormalizeBaseURL(endpoint.BaseURL)
		if err != nil {
			writeDomainError(w, supplier.ErrInvalid)
			return
		}
		addresses[base] = true
	}
	listings, err := a.suppliers.Store.Listings(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	result := supplierImpactJSON{}
	for _, listing := range listings {
		if addresses[listing.BaseURL] {
			result.LinkedChannels++
		}
	}
	writeJSON(w, 200, result)
}

func (a *app) registerSupplierRoutes(r *router) {
	r.implement("407", "POST /api/admin/suppliers/preview", accessAdmin, a.previewSupplierImpact)
	r.implement("407", "GET /api/suppliers", accessReady, a.listSuppliers)
	r.implement("407", "GET /api/suppliers/{supplierID}", accessReady, a.getSupplier)
	r.implement("407", "POST /api/suppliers/recognize", accessReady, a.recognizeSupplier)
	r.implement("407", "GET /api/channel-hall", accessReady, a.listHall)
	r.implement("407", "GET /api/channel-hall/{channelID}", accessReady, a.getHallChannel)
	r.implement("407", "GET /api/channel-hall/{channelID}/analysis", accessReady, a.analyzeHallChannel)
	r.implement("407", "GET /api/suppliers/{supplierID}/analysis", accessReady, a.analyzeSupplier)
	r.implement("407", "GET /api/admin/suppliers", accessAdmin, a.adminSuppliers)
	r.implement("407", "POST /api/admin/suppliers", accessAdmin, a.saveSupplier)
	r.implement("407", "PUT /api/admin/suppliers/{supplierID}", accessAdmin, a.saveSupplier)
	r.implement("407", "POST /api/admin/suppliers/icon", accessAdmin, a.fetchSupplierIcon)
}

// Only expose classified failures, never upstream bodies or transport errors.
func supplierIconError(err error) (string, string) {
	var httpError *supplier.IconHTTPError
	switch {
	case errors.Is(err, supplier.ErrInvalid):
		return "icon_invalid_url", "请填写有效的 HTTPS 官网地址"
	case errors.Is(err, supplier.ErrIconTimeout):
		return "icon_timeout", "获取图标超时，请重试或上传图片"
	case errors.Is(err, supplier.ErrIconFormat):
		return "icon_unsupported", "图标格式不支持或文件损坏，请上传 PNG、JPEG 或 GIF"
	case errors.Is(err, supplier.ErrIconTooLarge):
		return "icon_too_large", "网页或图标超过大小限制，请上传 256 KB、1024 × 1024 以内的图片"
	case errors.As(err, &httpError):
		return "icon_http_error", fmt.Sprintf("官网或图标服务器返回 HTTP %d，可上传图片", httpError.Status)
	case errors.Is(err, supplier.ErrIconRedirect):
		return "icon_redirect", "官网或图标地址发生跳转，请填写跳转后的官网地址或上传图片"
	case errors.Is(err, supplier.ErrIconDownload):
		return "icon_download_failed", "图标下载失败，请检查官网地址或上传图片"
	default:
		return "icon_unavailable", "未找到可用图标，可上传图片或保留默认图标"
	}
}
