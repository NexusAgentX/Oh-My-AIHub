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

func formatTestsResponse(tests map[channel.Format]channel.FormatTest) map[string]any {
	result := make(map[string]any, len(tests))
	for format, test := range tests {
		result[string(format)] = map[string]any{
			"ok": test.OK, "status_code": test.StatusCode, "error": test.Error, "duration_ms": test.DurationMS, "tested_at": test.TestedAt,
		}
	}
	return result
}

func advancedResponse(advanced channel.Advanced) map[string]any {
	set := make([]map[string]string, 0, len(advanced.HeaderRules.Set))
	for _, rule := range advanced.HeaderRules.Set {
		set = append(set, map[string]string{"name": rule.Name, "value": rule.Value})
	}
	remove := advanced.HeaderRules.Remove
	if remove == nil {
		remove = []string{}
	}
	return map[string]any{
		"user_agent": advanced.UserAgent, "header_rules": map[string]any{"set": set, "remove": remove},
		"concurrency_limit": advanced.ConcurrencyLimit, "rpm_limit": advanced.RPMLimit,
		"daily_revenue_cap": nullableAmount(advanced.DailyRevenueCap), "ttft_timeout_ms": advanced.TTFTTimeoutMS,
		"total_timeout_ms": advanced.TotalTimeoutMS, "cooldown_failures": advanced.CooldownFailures, "cooldown_seconds": advanced.CooldownSeconds,
	}
}

func (a *app) channelResponse(item channel.Channel, models map[string]catalog.Model, now time.Time) map[string]any {
	items := make([]map[string]any, 0, len(item.Models))
	for _, model := range item.Models {
		known := models[model.ModelID]
		prices, _ := currentPrices(known, now)
		formats := model.Formats
		items = append(items, map[string]any{
			"model_id": model.ModelID, "display_name": known.DisplayName, "upstream_model": model.UpstreamModel,
			"multiplier": money.FromNano(model.MultiplierNano).String(), "formats": formats,
			"format_tests": formatTestsResponse(model.FormatTests), "enabled": model.Enabled,
			"current_prices": scaledPrices(prices, model.MultiplierNano),
		})
	}
	var successRate, cooldownUntil any
	if item.Today.SuccessRate != nil {
		successRate = ratio(*item.Today.SuccessRate)
	}
	if until := a.gateway.State().CooldownUntil(item.ID, now); !until.IsZero() {
		cooldownUntil = until
	}
	return map[string]any{
		"id": item.ID, "name": item.Name, "base_url": item.BaseURL, "status": item.Status, "suspended_reason": item.SuspendedReason,
		"cooldown_until": cooldownUntil, "models": items, "advanced": advancedResponse(item.Advanced),
		"today":      map[string]any{"revenue": item.Today.Revenue.String(), "calls": item.Today.Calls, "success_rate": successRate},
		"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
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

func eventsResponse(events []channel.Event) []map[string]any {
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, map[string]any{"id": itoa(event.ID), "kind": event.Kind, "reason": event.Reason, "created_at": event.CreatedAt})
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
	items := make([]map[string]any, 0, len(channels))
	for _, item := range channels {
		items = append(items, a.channelResponse(item, models, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
	writeJSON(w, http.StatusCreated, map[string]any{"channel": a.channelResponse(created, catalogModels, time.Now())})
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
	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, map[string]any{"id": model.ID, "matched_model_id": model.MatchedModelID, "suggested_formats": model.SuggestedFormats})
	}
	writeJSON(w, http.StatusOK, map[string]any{"base_url": baseURL, "upstream_models": items})
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
	writeJSON(w, http.StatusOK, map[string]any{"channel": a.channelResponse(item, models, time.Now()), "events": eventsResponse(events)})
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
	writeJSON(w, http.StatusOK, map[string]any{"channel": a.channelResponse(updated, models, time.Now())})
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
	results := make([]map[string]any, 0, len(outcomes))
	for _, outcome := range outcomes {
		results = append(results, map[string]any{
			"model_id": outcome.ModelID, "format": outcome.Format, "ok": outcome.OK, "status_code": outcome.StatusCode,
			"error": outcome.Error, "duration_ms": outcome.DurationMS,
		})
	}
	models, err := a.catalogByID(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "channel": a.channelResponse(updated, models, time.Now())})
}

// ---- administrators ----

func (a *app) adminChannelResponse(item channel.Channel, models map[string]catalog.Model, now time.Time) map[string]any {
	response := a.channelResponse(item, models, now)
	response["owner"] = map[string]any{"id": item.Owner.ID, "username": item.Owner.Username, "display_name": item.Owner.DisplayName}
	return response
}

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
	items := make([]map[string]any, 0, len(channels))
	cursor := ""
	for _, item := range channels {
		items = append(items, a.adminChannelResponse(item, models, now))
		cursor = encodeTextCursor(item.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + item.ID)
	}
	var next *string
	if hasMore {
		next = &cursor
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
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
	writeJSON(w, http.StatusOK, map[string]any{"channel": a.adminChannelResponse(item, models, time.Now()), "events": eventsResponse(events)})
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
		writeJSON(w, http.StatusOK, map[string]any{"channel": a.adminChannelResponse(item, models, time.Now())})
	}
}

// registerChannelRoutes 注册渠道与管理员渠道路由（Feature B）。
func (a *app) registerChannelRoutes(r *router) {
	r.feature("B", "GET /api/channels", accessReady, a.listChannels)
	r.feature("B", "POST /api/channels", accessReady, a.createChannel)
	r.feature("B", "POST /api/channels/discover", accessReady, a.discoverChannel)
	r.feature("B", "GET /api/channels/{channelID}", accessReady, a.getChannel)
	r.feature("B", "PATCH /api/channels/{channelID}", accessReady, a.updateChannel)
	r.feature("B", "DELETE /api/channels/{channelID}", accessReady, a.deleteChannel)
	r.feature("B", "POST /api/channels/{channelID}/test", accessReady, a.testChannel)
	r.feature("B", "GET /api/admin/channels", accessAdmin, a.listAdminChannels)
	r.feature("B", "GET /api/admin/channels/{channelID}", accessAdmin, a.getAdminChannel)
	r.feature("B", "POST /api/admin/channels/{channelID}/suspend", accessAdmin, a.moderateChannel(true))
	r.feature("B", "POST /api/admin/channels/{channelID}/unsuspend", accessAdmin, a.moderateChannel(false))
}
