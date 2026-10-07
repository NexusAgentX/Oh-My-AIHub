package api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type priceTierRequest struct {
	Name            string `json:"name"`
	MinPromptTokens *int64 `json:"min_prompt_tokens"`
	MaxPromptTokens *int64 `json:"max_prompt_tokens"`
	Timezone        string `json:"timezone"`
	Weekdays        []int  `json:"weekdays"`
	StartMinute     *int16 `json:"start_minute_of_day"`
	EndMinute       *int16 `json:"end_minute_of_day"`
	InputPrice      string `json:"input_price"`
	OutputPrice     string `json:"output_price"`
	CacheWritePrice string `json:"cache_write_price"`
	CacheReadPrice  string `json:"cache_read_price"`
}

type modelRequest struct {
	ID                       string             `json:"id"`
	Name                     string             `json:"name"`
	Provider                 string             `json:"provider"`
	ContextWindow            int64              `json:"context_window"`
	ParameterInfo            string             `json:"parameter_info"`
	InputModalities          []string           `json:"input_modalities"`
	OutputModalities         []string           `json:"output_modalities"`
	SupportsTools            bool               `json:"supports_tools"`
	SupportsStructuredOutput bool               `json:"supports_structured_output"`
	SupportsVision           bool               `json:"supports_vision"`
	InputPrice               string             `json:"input_price"`
	OutputPrice              string             `json:"output_price"`
	CacheWritePrice          string             `json:"cache_write_price"`
	CacheReadPrice           string             `json:"cache_read_price"`
	PriceTiers               []priceTierRequest `json:"price_tiers"`
	Status                   string             `json:"status"`
	ExpectedVersion          int64              `json:"expected_version"`
}

func (a *app) listPublicModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.catalog.ListPublic(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeModelList(w, models)
}

func (a *app) getPublicModel(w http.ResponseWriter, r *http.Request) {
	model, err := a.catalog.GetPublic(r.Context(), r.PathValue("modelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": modelResponse(model)})
}

func (a *app) listAdminModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.catalog.ListAdmin(r.Context(), accountFromContext(r.Context()), r.URL.Query().Get("q"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeModelList(w, models)
}

func (a *app) getAdminModel(w http.ResponseWriter, r *http.Request) {
	model, err := a.catalog.GetAdmin(r.Context(), accountFromContext(r.Context()), r.PathValue("modelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": modelResponse(model)})
}

func (a *app) createModel(w http.ResponseWriter, r *http.Request) {
	var request modelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	model, err := parseModelRequest(request)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	created, err := a.catalog.Create(r.Context(), accountFromContext(r.Context()), model)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"model": modelResponse(created)})
}

func (a *app) updateModel(w http.ResponseWriter, r *http.Request) {
	var request modelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	request.ID = r.PathValue("modelID")
	model, err := parseModelRequest(request)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	updated, err := a.catalog.Update(r.Context(), accountFromContext(r.Context()), r.PathValue("modelID"), request.ExpectedVersion, model)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": modelResponse(updated)})
}

func writeModelList(w http.ResponseWriter, models []catalog.Model) {
	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, modelResponse(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": items})
}

// registerCatalogRoutes 注册模型目录路由。
func (a *app) registerCatalogRoutes(r *router) {
	r.ready("GET /api/models", a.listPublicModels)
	r.ready("GET /api/models/{modelID...}", a.getPublicModel)
	r.admin("GET /api/admin/models", a.listAdminModels)
	r.admin("POST /api/admin/models", a.createModel)
	r.admin("GET /api/admin/models/{modelID...}", a.getAdminModel)
	r.admin("PUT /api/admin/models/{modelID...}", a.updateModel)
}

var modelPricePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$`)

func modelResponse(model catalog.Model) map[string]any {
	return map[string]any{
		"id":                         model.ID,
		"name":                       model.Name,
		"provider":                   model.Provider,
		"context_window":             model.ContextWindow,
		"parameter_info":             model.ParameterInfo,
		"input_modalities":           model.InputModalities,
		"output_modalities":          model.OutputModalities,
		"supports_tools":             model.SupportsTools,
		"supports_structured_output": model.SupportsStructuredOutput,
		"supports_vision":            model.SupportsVision,
		"input_price":                model.InputPrice.String(),
		"output_price":               model.OutputPrice.String(),
		"cache_write_price":          model.CacheWritePrice.String(),
		"cache_read_price":           model.CacheReadPrice.String(),
		"price_tiers":                benchmarkPriceTierResponses(model.PriceTiers),
		"price_unit":                 "points_per_million_tokens",
		"status":                     model.Status,
		"version":                    model.Version,
		"created_at":                 model.CreatedAt,
		"updated_at":                 model.UpdatedAt,
		"price_updated_at":           model.PriceUpdatedAt,
	}
}

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

func parseModelRequest(request modelRequest) (catalog.Model, error) {
	inputPrice, err := parseModelPrice(request.InputPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	outputPrice, err := parseModelPrice(request.OutputPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	cacheWritePrice, err := parseModelPrice(request.CacheWritePrice)
	if err != nil {
		return catalog.Model{}, err
	}
	cacheReadPrice, err := parseModelPrice(request.CacheReadPrice)
	if err != nil {
		return catalog.Model{}, err
	}
	priceTiers, err := parsePriceTierRequests(request.PriceTiers)
	if err != nil {
		return catalog.Model{}, err
	}
	return catalog.Model{
		ID:                       strings.TrimSpace(request.ID),
		Name:                     request.Name,
		Provider:                 request.Provider,
		ContextWindow:            request.ContextWindow,
		ParameterInfo:            request.ParameterInfo,
		InputModalities:          request.InputModalities,
		OutputModalities:         request.OutputModalities,
		SupportsTools:            request.SupportsTools,
		SupportsStructuredOutput: request.SupportsStructuredOutput,
		SupportsVision:           request.SupportsVision,
		InputPrice:               inputPrice,
		OutputPrice:              outputPrice,
		CacheWritePrice:          cacheWritePrice,
		CacheReadPrice:           cacheReadPrice,
		PriceTiers:               priceTiers,
		Status:                   catalog.Status(request.Status),
	}, nil
}

func parsePriceTierRequests(requests []priceTierRequest) ([]ledger.PriceTier, error) {
	if requests == nil {
		return nil, nil
	}
	tiers := make([]ledger.PriceTier, 0, len(requests))
	for _, request := range requests {
		inputPrice, err := parseModelPrice(request.InputPrice)
		if err != nil {
			return nil, err
		}
		outputPrice, err := parseModelPrice(request.OutputPrice)
		if err != nil {
			return nil, err
		}
		cacheWritePrice, err := parseModelPrice(request.CacheWritePrice)
		if err != nil {
			return nil, err
		}
		cacheReadPrice, err := parseModelPrice(request.CacheReadPrice)
		if err != nil {
			return nil, err
		}
		tiers = append(tiers, ledger.PriceTier{
			Name: request.Name, MinPromptTokens: request.MinPromptTokens, MaxPromptTokens: request.MaxPromptTokens,
			Timezone: request.Timezone, Weekdays: request.Weekdays,
			StartMinute: request.StartMinute, EndMinute: request.EndMinute,
			InputPrice: inputPrice, OutputPrice: outputPrice,
			CacheWritePrice: cacheWritePrice, CacheReadPrice: cacheReadPrice,
		})
	}
	return tiers, nil
}
