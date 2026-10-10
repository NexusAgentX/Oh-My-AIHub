package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// ---- responses ----

// formatTestJSON is the OpenAPI FormatTest schema.
type formatTestJSON struct {
	OK         bool      `json:"ok"`
	StatusCode *int      `json:"status_code"`
	Error      *string   `json:"error"`
	DurationMS *int      `json:"duration_ms"`
	TestedAt   time.Time `json:"tested_at"`
}

// headerSetJSON is one set rule of the OpenAPI HeaderRules schema.
type headerSetJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// headerRulesJSON is the OpenAPI HeaderRules schema.
type headerRulesJSON struct {
	Set    []headerSetJSON `json:"set"`
	Remove []string        `json:"remove"`
}

// channelAdvancedJSON is the OpenAPI ChannelAdvanced schema.
type channelAdvancedJSON struct {
	UserAgent        *string         `json:"user_agent"`
	HeaderRules      headerRulesJSON `json:"header_rules"`
	ConcurrencyLimit *int32          `json:"concurrency_limit"`
	RPMLimit         *int32          `json:"rpm_limit"`
	DailyRevenueCap  *string         `json:"daily_revenue_cap"`
	TTFTTimeoutMS    *int32          `json:"ttft_timeout_ms"`
	TotalTimeoutMS   *int32          `json:"total_timeout_ms"`
	CooldownFailures *int32          `json:"cooldown_failures"`
	CooldownSeconds  *int32          `json:"cooldown_seconds"`
}

// channelModelJSON is the OpenAPI ChannelModel schema.
type channelModelJSON struct {
	ModelID       string                    `json:"model_id"`
	DisplayName   string                    `json:"display_name"`
	UpstreamModel string                    `json:"upstream_model"`
	Multiplier    string                    `json:"multiplier"`
	Formats       []channel.Format          `json:"formats"`
	FormatTests   map[string]formatTestJSON `json:"format_tests"`
	Enabled       bool                      `json:"enabled"`
	CurrentPrices effectivePricesJSON       `json:"current_prices"`
}

// channelTodayJSON is the OpenAPI ChannelToday schema.
type channelTodayJSON struct {
	Revenue     string  `json:"revenue"`
	Calls       int64   `json:"calls"`
	SuccessRate *string `json:"success_rate"`
}

// channelJSON is the OpenAPI Channel schema.
type channelJSON struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	BaseURL         string              `json:"base_url"`
	Status          channel.Status      `json:"status"`
	SuspendedReason *string             `json:"suspended_reason"`
	CooldownUntil   *time.Time          `json:"cooldown_until"`
	Models          []channelModelJSON  `json:"models"`
	Advanced        channelAdvancedJSON `json:"advanced"`
	Today           channelTodayJSON    `json:"today"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// channelListJSON is the OpenAPI ChannelList schema.
type channelListJSON struct {
	Items []channelJSON `json:"items"`
}

// channelEnvelopeJSON is the OpenAPI ChannelEnvelope schema.
type channelEnvelopeJSON struct {
	Channel channelJSON `json:"channel"`
}

// channelEventJSON is the OpenAPI ChannelEvent schema.
type channelEventJSON struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// channelDetailJSON is the OpenAPI ChannelDetail schema.
type channelDetailJSON struct {
	Channel channelJSON        `json:"channel"`
	Events  []channelEventJSON `json:"events"`
}

// discoveredModelJSON is the OpenAPI DiscoveredModel schema.
type discoveredModelJSON struct {
	ID               string           `json:"id"`
	MatchedModelID   *string          `json:"matched_model_id"`
	SuggestedFormats []channel.Format `json:"suggested_formats"`
}

// channelDiscoverResponseJSON is the OpenAPI ChannelDiscoverResponse schema.
type channelDiscoverResponseJSON struct {
	BaseURL        string                `json:"base_url"`
	UpstreamModels []discoveredModelJSON `json:"upstream_models"`
}

// channelTestResultJSON is the OpenAPI ChannelTestResult schema.
type channelTestResultJSON struct {
	ModelID    string         `json:"model_id"`
	Format     channel.Format `json:"format"`
	OK         bool           `json:"ok"`
	StatusCode *int           `json:"status_code"`
	Error      *string        `json:"error"`
	DurationMS int            `json:"duration_ms"`
}

// channelTestResponseJSON is the OpenAPI ChannelTestResponse schema.
type channelTestResponseJSON struct {
	Results []channelTestResultJSON `json:"results"`
	Channel channelJSON             `json:"channel"`
}

// channelOwnerJSON is the OpenAPI AccountRef schema of an administrator's channel owner. It is
// not named accountRefJSON because observe.go still uses that name for its map-based helper.
type channelOwnerJSON struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// adminChannelJSON is the OpenAPI AdminChannel schema: a Channel plus its owner.
type adminChannelJSON struct {
	channelJSON
	Owner channelOwnerJSON `json:"owner"`
}

// adminChannelEnvelopeJSON is the OpenAPI AdminChannelEnvelope schema.
type adminChannelEnvelopeJSON struct {
	Channel adminChannelJSON `json:"channel"`
}

// adminChannelDetailJSON is the OpenAPI AdminChannelDetail schema.
type adminChannelDetailJSON struct {
	Channel adminChannelJSON   `json:"channel"`
	Events  []channelEventJSON `json:"events"`
}

func newFormatTestsJSON(tests map[channel.Format]channel.FormatTest) map[string]formatTestJSON {
	result := make(map[string]formatTestJSON, len(tests))
	for format, test := range tests {
		result[string(format)] = formatTestJSON{
			OK: test.OK, StatusCode: test.StatusCode, Error: test.Error, DurationMS: test.DurationMS, TestedAt: test.TestedAt,
		}
	}
	return result
}

func newChannelAdvancedJSON(advanced channel.Advanced) channelAdvancedJSON {
	set := make([]headerSetJSON, 0, len(advanced.HeaderRules.Set))
	for _, rule := range advanced.HeaderRules.Set {
		set = append(set, headerSetJSON{Name: rule.Name, Value: rule.Value})
	}
	remove := advanced.HeaderRules.Remove
	if remove == nil {
		remove = []string{}
	}
	return channelAdvancedJSON{
		UserAgent:        advanced.UserAgent,
		HeaderRules:      headerRulesJSON{Set: set, Remove: remove},
		ConcurrencyLimit: advanced.ConcurrencyLimit,
		RPMLimit:         advanced.RPMLimit,
		DailyRevenueCap:  nullableAmount(advanced.DailyRevenueCap),
		TTFTTimeoutMS:    advanced.TTFTTimeoutMS,
		TotalTimeoutMS:   advanced.TotalTimeoutMS,
		CooldownFailures: advanced.CooldownFailures,
		CooldownSeconds:  advanced.CooldownSeconds,
	}
}

func (a *app) newChannelJSON(item channel.Channel, models map[string]catalog.Model, now time.Time) channelJSON {
	items := make([]channelModelJSON, 0, len(item.Models))
	for _, model := range item.Models {
		known := models[model.ModelID]
		prices, _ := currentPrices(known, now)
		items = append(items, channelModelJSON{
			ModelID:       model.ModelID,
			DisplayName:   known.DisplayName,
			UpstreamModel: model.UpstreamModel,
			Multiplier:    money.FromNano(model.MultiplierNano).String(),
			Formats:       model.Formats,
			FormatTests:   newFormatTestsJSON(model.FormatTests),
			Enabled:       model.Enabled,
			CurrentPrices: newEffectivePricesJSON(prices, model.MultiplierNano),
		})
	}
	var successRate *string
	var cooldownUntil *time.Time
	if item.Today.SuccessRate != nil {
		rate := ratio(*item.Today.SuccessRate)
		successRate = &rate
	}
	if until := a.gateway.State().CooldownUntil(item.ID, now); !until.IsZero() {
		cooldownUntil = &until
	}
	return channelJSON{
		ID:              item.ID,
		Name:            item.Name,
		BaseURL:         item.BaseURL,
		Status:          item.Status,
		SuspendedReason: item.SuspendedReason,
		CooldownUntil:   cooldownUntil,
		Models:          items,
		Advanced:        newChannelAdvancedJSON(item.Advanced),
		Today:           channelTodayJSON{Revenue: item.Today.Revenue.String(), Calls: item.Today.Calls, SuccessRate: successRate},
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func (a *app) newAdminChannelJSON(item channel.Channel, models map[string]catalog.Model, now time.Time) adminChannelJSON {
	return adminChannelJSON{
		channelJSON: a.newChannelJSON(item, models, now),
		Owner:       channelOwnerJSON{ID: item.Owner.ID, Username: item.Owner.Username, DisplayName: item.Owner.DisplayName},
	}
}

func (a *app) catalogByID(r *http.Request) (map[string]catalog.Model, error) {
	models, err := a.catalog.List(r.Context(), true)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]catalog.Model, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	return byID, nil
}

func newChannelEventsJSON(events []channel.Event) []channelEventJSON {
	items := make([]channelEventJSON, 0, len(events))
	for _, event := range events {
		items = append(items, channelEventJSON{ID: itoa(event.ID), Kind: event.Kind, Reason: event.Reason, CreatedAt: event.CreatedAt})
	}
	return items
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

// ---- requests ----

type headerRulesRequest struct {
	Set []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"set"`
	Remove []string `json:"remove"`
}

type advancedRequest struct {
	UserAgent        *string             `json:"user_agent"`
	HeaderRules      *headerRulesRequest `json:"header_rules"`
	ConcurrencyLimit *int32              `json:"concurrency_limit"`
	RPMLimit         *int32              `json:"rpm_limit"`
	DailyRevenueCap  *string             `json:"daily_revenue_cap"`
	TTFTTimeoutMS    *int32              `json:"ttft_timeout_ms"`
	TotalTimeoutMS   *int32              `json:"total_timeout_ms"`
	CooldownFailures *int32              `json:"cooldown_failures"`
	CooldownSeconds  *int32              `json:"cooldown_seconds"`
}

func (r advancedRequest) advanced() (channel.Advanced, error) {
	advanced := channel.Advanced{
		UserAgent: r.UserAgent, ConcurrencyLimit: r.ConcurrencyLimit, RPMLimit: r.RPMLimit, TTFTTimeoutMS: r.TTFTTimeoutMS,
		TotalTimeoutMS: r.TotalTimeoutMS, CooldownFailures: r.CooldownFailures, CooldownSeconds: r.CooldownSeconds,
	}
	if r.DailyRevenueCap != nil {
		cap, err := money.Parse(*r.DailyRevenueCap)
		if err != nil {
			return channel.Advanced{}, channel.ErrInvalidInput
		}
		advanced.DailyRevenueCap = &cap
	}
	if r.HeaderRules != nil {
		for _, rule := range r.HeaderRules.Set {
			advanced.HeaderRules.Set = append(advanced.HeaderRules.Set, channel.HeaderSet{Name: rule.Name, Value: rule.Value})
		}
		advanced.HeaderRules.Remove = r.HeaderRules.Remove
	}
	return advanced, nil
}

type channelModelRequest struct {
	ModelID       string           `json:"model_id"`
	UpstreamModel string           `json:"upstream_model"`
	Multiplier    *string          `json:"multiplier"`
	Formats       []channel.Format `json:"formats"`
	Enabled       *bool            `json:"enabled"`
}

func modelsFromRequest(requests []channelModelRequest) ([]channel.Model, error) {
	models := make([]channel.Model, 0, len(requests))
	for _, request := range requests {
		multiplier := int64(money.Scale)
		if request.Multiplier != nil {
			parsed, err := money.Parse(*request.Multiplier)
			if err != nil || parsed < 0 {
				return nil, channel.ErrInvalidInput
			}
			multiplier = parsed.Nano()
		}
		enabled := request.Enabled == nil || *request.Enabled
		models = append(models, channel.Model{
			ModelID: request.ModelID, UpstreamModel: request.UpstreamModel, MultiplierNano: multiplier, Formats: request.Formats, Enabled: enabled,
		})
	}
	return models, nil
}

type channelRequest struct {
	Name     *string                `json:"name"`
	BaseURL  *string                `json:"base_url"`
	APIKey   *string                `json:"api_key"`
	Status   *channel.Status        `json:"status"`
	Models   *[]channelModelRequest `json:"models"`
	Advanced *advancedRequest       `json:"advanced"`
}

// ---- handlers ----

func (a *app) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := a.channels.List(r.Context(), accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	now := time.Now()
	items := make([]channelJSON, 0, len(channels))
	for _, item := range channels {
		items = append(items, a.newChannelJSON(item, models, now))
	}
	writeJSON(w, http.StatusOK, channelListJSON{Items: items})
}

func (a *app) createChannel(w http.ResponseWriter, r *http.Request) {
	var request channelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.Name == nil || request.BaseURL == nil || request.APIKey == nil || request.Models == nil {
		writeDomainError(w, channel.ErrInvalidInput)
		return
	}
	models, err := modelsFromRequest(*request.Models)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	input := channel.CreateInput{Name: *request.Name, BaseURL: *request.BaseURL, APIKey: *request.APIKey, Models: models}
	if request.Status != nil {
		input.Status = *request.Status
	}
	if request.Advanced != nil {
		if input.Advanced, err = request.Advanced.advanced(); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	created, err := a.channels.Create(r.Context(), accountFromContext(r.Context()).ID, input)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	catalogModels, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, channelEnvelopeJSON{Channel: a.newChannelJSON(created, catalogModels, time.Now())})
}

func (a *app) discoverChannel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if !a.channelProbes.take(accountFromContext(r.Context()).ID) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后再试")
		return
	}
	baseURL, models, err := a.channels.Discover(r.Context(), request.BaseURL, request.APIKey)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]discoveredModelJSON, 0, len(models))
	for _, model := range models {
		items = append(items, discoveredModelJSON{ID: model.ID, MatchedModelID: model.MatchedModelID, SuggestedFormats: model.SuggestedFormats})
	}
	writeJSON(w, http.StatusOK, channelDiscoverResponseJSON{BaseURL: baseURL, UpstreamModels: items})
}

func (a *app) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	item, events, err := a.channels.Get(r.Context(), accountFromContext(r.Context()).ID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channelDetailJSON{Channel: a.newChannelJSON(item, models, time.Now()), Events: newChannelEventsJSON(events)})
}

func (a *app) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	var request channelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.Name == nil && request.BaseURL == nil && request.APIKey == nil && request.Status == nil && request.Models == nil && request.Advanced == nil {
		writeDomainError(w, channel.ErrInvalidInput)
		return
	}
	input := channel.UpdateInput{Name: request.Name, BaseURL: request.BaseURL, APIKey: request.APIKey, Status: request.Status}
	var err error
	if request.Models != nil {
		models, convertErr := modelsFromRequest(*request.Models)
		if convertErr != nil {
			writeDomainError(w, convertErr)
			return
		}
		input.Models = &models
	}
	if request.Advanced != nil {
		advanced, convertErr := request.Advanced.advanced()
		if convertErr != nil {
			writeDomainError(w, convertErr)
			return
		}
		input.Advanced = &advanced
	}
	updated, err := a.channels.Update(r.Context(), accountFromContext(r.Context()).ID, id, input)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channelEnvelopeJSON{Channel: a.newChannelJSON(updated, models, time.Now())})
}

func (a *app) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	if err := a.channels.Delete(r.Context(), accountFromContext(r.Context()).ID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	var request struct {
		ModelIDs []string         `json:"model_ids"`
		Formats  []channel.Format `json:"formats"`
		Apply    bool             `json:"apply"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	ownerID := accountFromContext(r.Context()).ID
	if !a.channelProbes.take(ownerID) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后再试")
		return
	}
	outcomes, updated, err := a.channels.Test(r.Context(), ownerID, id, channel.TestRequest{ModelIDs: request.ModelIDs, Formats: request.Formats, Apply: request.Apply})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	results := make([]channelTestResultJSON, 0, len(outcomes))
	for _, outcome := range outcomes {
		results = append(results, channelTestResultJSON{
			ModelID: outcome.ModelID, Format: outcome.Format, OK: outcome.OK, StatusCode: outcome.StatusCode,
			Error: outcome.Error, DurationMS: outcome.DurationMS,
		})
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channelTestResponseJSON{Results: results, Channel: a.newChannelJSON(updated, models, time.Now())})
}

// ---- administrators ----

func (a *app) listAdminChannels(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	if !ok {
		writeBadCursor(w)
		return
	}
	query := r.URL.Query()
	status := channel.Status(query.Get("status"))
	if status != "" && status != channel.StatusListed && status != channel.StatusUnlisted && status != channel.StatusSuspended {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	ownerID := query.Get("owner_id")
	if ownerID != "" && !uuidPattern.MatchString(ownerID) {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	after := ""
	if raw := query.Get("cursor"); raw != "" {
		if after, ok = decodeTextCursor(raw); !ok {
			writeBadCursor(w)
			return
		}
	}
	channels, err := a.channels.AdminList(r.Context(), channel.AdminFilter{Query: query.Get("q"), Status: status, OwnerID: ownerID, AfterKey: after, Limit: limit + 1})
	if err != nil {
		if err == channel.ErrInvalidInput {
			writeBadCursor(w)
			return
		}
		writeDomainError(w, err)
		return
	}
	hasMore := len(channels) > limit
	if hasMore {
		channels = channels[:limit]
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	now := time.Now()
	items := make([]adminChannelJSON, 0, len(channels))
	cursor := ""
	for _, item := range channels {
		items = append(items, a.newAdminChannelJSON(item, models, now))
		cursor = encodeTextCursor(item.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + item.ID)
	}
	var next *string
	if hasMore {
		next = &cursor
	}
	writeJSON(w, http.StatusOK, pageJSON[adminChannelJSON]{Items: items, NextCursor: next})
}

func (a *app) getAdminChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "channelID")
	if !ok {
		return
	}
	item, events, err := a.channels.AdminGet(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminChannelDetailJSON{Channel: a.newAdminChannelJSON(item, models, time.Now()), Events: newChannelEventsJSON(events)})
}

func (a *app) moderateChannel(suspend bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathUUID(w, r, "channelID")
		if !ok {
			return
		}
		var request struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			writeInvalidJSON(w)
			return
		}
		actor := accountFromContext(r.Context()).ID
		var item channel.Channel
		var err error
		if suspend {
			item, err = a.channels.Suspend(r.Context(), actor, id, request.Reason)
		} else {
			item, err = a.channels.Unsuspend(r.Context(), actor, id, request.Reason)
		}
		if err != nil {
			writeDomainError(w, err)
			return
		}
		models, err := a.catalogByID(r)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, adminChannelEnvelopeJSON{Channel: a.newAdminChannelJSON(item, models, time.Now())})
	}
}

// registerChannelRoutes 注册渠道与管理员渠道路由（Feature B）。
func (a *app) registerChannelRoutes(r *router) {
	r.implement("B", "GET /api/channels", accessReady, a.listChannels)
	r.implement("B", "POST /api/channels", accessReady, a.createChannel)
	r.implement("B", "POST /api/channels/discover", accessReady, a.discoverChannel)
	r.implement("B", "GET /api/channels/{channelID}", accessReady, a.getChannel)
	r.implement("B", "PATCH /api/channels/{channelID}", accessReady, a.updateChannel)
	r.implement("B", "DELETE /api/channels/{channelID}", accessReady, a.deleteChannel)
	r.implement("B", "POST /api/channels/{channelID}/test", accessReady, a.testChannel)
	r.implement("B", "GET /api/admin/channels", accessAdmin, a.listAdminChannels)
	r.implement("B", "GET /api/admin/channels/{channelID}", accessAdmin, a.getAdminChannel)
	r.implement("B", "POST /api/admin/channels/{channelID}/suspend", accessAdmin, a.moderateChannel(true))
	r.implement("B", "POST /api/admin/channels/{channelID}/unsuspend", accessAdmin, a.moderateChannel(false))
}
