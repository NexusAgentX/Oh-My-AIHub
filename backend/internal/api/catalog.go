package api

import (
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
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
