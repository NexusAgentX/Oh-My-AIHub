package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// optional distinguishes an absent JSON field from an explicit null.
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type pricesRequest struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheWrite string `json:"cache_write"`
	CacheRead  string `json:"cache_read"`
}

type priceTierRequest struct {
	Name            string        `json:"name"`
	MinPromptTokens *int64        `json:"min_prompt_tokens"`
	MaxPromptTokens *int64        `json:"max_prompt_tokens"`
	Timezone        string        `json:"timezone"`
	Weekdays        []int         `json:"weekdays"`
	StartMinute     *int16        `json:"start_minute_of_day"`
	EndMinute       *int16        `json:"end_minute_of_day"`
	Prices          pricesRequest `json:"prices"`
}

type modelRequest struct {
	ID                       *string             `json:"id"`
	DisplayName              *string             `json:"display_name"`
	BasePrices               *pricesRequest      `json:"base_prices"`
	PriceTiers               *[]priceTierRequest `json:"price_tiers"`
	Enabled                  *bool               `json:"enabled"`
	SortOrder                *int32              `json:"sort_order"`
	Provider                 *string             `json:"provider"`
	ContextWindow            optional[int64]     `json:"context_window"`
	InputModalities          *[]string           `json:"input_modalities"`
	OutputModalities         *[]string           `json:"output_modalities"`
	SupportsTools            *bool               `json:"supports_tools"`
	SupportsStructuredOutput *bool               `json:"supports_structured_output"`
	SupportsVision           *bool               `json:"supports_vision"`
	ParameterInfo            *string             `json:"parameter_info"`
}

var modelPricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$`)

// parseModelPrice accepts a non-negative decimal of at most 100000 points
// with at most nine decimals.
func parseModelPrice(value string) (money.Amount, error) {
	value = strings.TrimSpace(value)
	if !modelPricePattern.MatchString(value) {
		return 0, money.ErrInvalidAmount
	}
	amount, err := money.Parse(value)
	if err != nil || amount > catalog.MaxPriceNanoPerMillion {
		return 0, money.ErrInvalidAmount
	}
	return amount, nil
}

func (p pricesRequest) parse() ([4]money.Amount, error) {
	var result [4]money.Amount
	for index, value := range []string{p.Input, p.Output, p.CacheWrite, p.CacheRead} {
		amount, err := parseModelPrice(value)
		if err != nil {
			return result, err
		}
		result[index] = amount
	}
	return result, nil
}

func parsePriceTiers(requests []priceTierRequest) ([]ledger.PriceTier, error) {
	tiers := make([]ledger.PriceTier, 0, len(requests))
	for _, request := range requests {
		prices, err := request.Prices.parse()
		if err != nil {
			return nil, err
		}
		tiers = append(tiers, ledger.PriceTier{
			Name: request.Name, MinPromptTokens: request.MinPromptTokens, MaxPromptTokens: request.MaxPromptTokens,
			Timezone: request.Timezone, Weekdays: request.Weekdays, StartMinute: request.StartMinute, EndMinute: request.EndMinute,
			InputPrice: prices[0], OutputPrice: prices[1], CacheWritePrice: prices[2], CacheReadPrice: prices[3],
		})
	}
	return tiers, nil
}

// patch converts the request into a catalog patch; base prices are replaced as a set.
func (request modelRequest) patch() (catalog.ModelPatch, error) {
	patch := catalog.ModelPatch{
		DisplayName: request.DisplayName, Enabled: request.Enabled, SortOrder: request.SortOrder,
		Provider: request.Provider, InputModalities: request.InputModalities, OutputModalities: request.OutputModalities,
		SupportsTools: request.SupportsTools, SupportsStructuredOutput: request.SupportsStructuredOutput,
		SupportsVision: request.SupportsVision, ParameterInfo: request.ParameterInfo,
	}
	if request.ContextWindow.Set {
		patch.ContextWindow = &request.ContextWindow.Value
	}
	if request.BasePrices != nil {
		prices, err := request.BasePrices.parse()
		if err != nil {
			return patch, err
		}
		patch.InputPrice, patch.OutputPrice, patch.CacheWritePrice, patch.CacheReadPrice = &prices[0], &prices[1], &prices[2], &prices[3]
	}
	if request.PriceTiers != nil {
		tiers, err := parsePriceTiers(*request.PriceTiers)
		if err != nil {
			return patch, err
		}
		patch.PriceTiers = &tiers
	}
	return patch, nil
}

func pricesResponse(input, output, cacheWrite, cacheRead money.Amount) map[string]any {
	return map[string]any{
		"input": input.String(), "output": output.String(),
		"cache_write": cacheWrite.String(), "cache_read": cacheRead.String(),
	}
}

func priceTierResponse(seq int, tier ledger.PriceTier) map[string]any {
	var weekdays any
	if len(tier.Weekdays) > 0 {
		weekdays = tier.Weekdays
	}
	return map[string]any{
		"seq": seq, "name": tier.Name, "timezone": tier.Timezone,
		"min_prompt_tokens": tier.MinPromptTokens, "max_prompt_tokens": tier.MaxPromptTokens,
		"weekdays": weekdays, "start_minute_of_day": tier.StartMinute, "end_minute_of_day": tier.EndMinute,
		"prices": pricesResponse(tier.InputPrice, tier.OutputPrice, tier.CacheWritePrice, tier.CacheReadPrice),
	}
}

func adminModelResponse(model catalog.Model) map[string]any {
	tiers := make([]map[string]any, 0, len(model.PriceTiers))
	for index, tier := range model.PriceTiers {
		tiers = append(tiers, priceTierResponse(index+1, tier))
	}
	return map[string]any{
		"id": model.ID, "display_name": model.DisplayName,
		"base_prices": pricesResponse(model.InputPrice, model.OutputPrice, model.CacheWritePrice, model.CacheReadPrice),
		"price_tiers": tiers, "enabled": model.Enabled, "sort_order": model.SortOrder,
		"provider": model.Provider, "context_window": model.ContextWindow,
		"input_modalities": model.InputModalities, "output_modalities": model.OutputModalities,
		"supports_tools": model.SupportsTools, "supports_structured_output": model.SupportsStructuredOutput,
		"supports_vision": model.SupportsVision, "parameter_info": model.ParameterInfo,
		"created_at": model.CreatedAt, "updated_at": model.UpdatedAt,
	}
}

func (a *app) listAdminModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.catalog.List(r.Context(), true)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, adminModelResponse(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *app) createAdminModel(w http.ResponseWriter, r *http.Request) {
	var request modelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.ID == nil || request.DisplayName == nil || request.BasePrices == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
		return
	}
	patch, err := request.patch()
	if err != nil {
		writeDomainError(w, err)
		return
	}
	model := patch.Apply(catalog.Model{ID: *request.ID, Enabled: true})
	created, err := a.catalog.Create(r.Context(), accountFromContext(r.Context()).ID, model)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"model": adminModelResponse(created)})
}

func (a *app) updateAdminModel(w http.ResponseWriter, r *http.Request) {
	var request modelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.ID != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "模型名不能修改")
		return
	}
	patch, err := request.patch()
	if err != nil {
		writeDomainError(w, err)
		return
	}
	updated, err := a.catalog.Update(r.Context(), accountFromContext(r.Context()).ID, r.PathValue("modelID"), patch)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": adminModelResponse(updated)})
}

func (a *app) deleteAdminModel(w http.ResponseWriter, r *http.Request) {
	if err := a.catalog.Delete(r.Context(), accountFromContext(r.Context()).ID, r.PathValue("modelID")); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// registerAdminModelRoutes 注册管理员模型目录路由。
func (a *app) registerAdminModelRoutes(r *router) {
	r.admin("GET /api/admin/models", a.listAdminModels)
	r.admin("POST /api/admin/models", a.createAdminModel)
	r.admin("PATCH /api/admin/models/{modelID}", a.updateAdminModel)
	r.admin("DELETE /api/admin/models/{modelID}", a.deleteAdminModel)
}
