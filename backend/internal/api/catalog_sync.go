package api

import (
	"net/http"
)

func (a *app) catalogSyncStatus(w http.ResponseWriter, r *http.Request) {
	if a.catalogSync == nil {
		writeError(w, 503, "unavailable", "模型同步未配置")
		return
	}
	value, err := a.catalogSync.Status(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (a *app) catalogSyncRate(w http.ResponseWriter, r *http.Request) {
	if a.catalogSync == nil {
		writeError(w, 503, "unavailable", "模型同步未配置")
		return
	}
	var body struct {
		ExchangeRate *string   `json:"exchange_rate"`
		Providers    *[]string `json:"providers"`
	}
	if decodeJSON(w, r, &body) != nil {
		writeInvalidJSON(w)
		return
	}
	if body.ExchangeRate == nil || body.Providers == nil {
		writeError(w, 422, "invalid_input", "请提供换算率和提供商白名单（可为空）")
		return
	}
	if err := a.catalogSync.SetConfig(r.Context(), accountFromContext(r.Context()).ID, *body.ExchangeRate, *body.Providers); err != nil {
		writeDomainError(w, err)
		return
	}
	a.catalogSyncStatus(w, r)
}
func (a *app) triggerCatalogSync(w http.ResponseWriter, r *http.Request) {
	if a.catalogSync == nil {
		writeError(w, 503, "unavailable", "模型同步未配置")
		return
	}
	if !a.catalogSync.Trigger() {
		writeError(w, 409, "conflict", "同步正在进行")
		return
	}
	writeJSON(w, 202, map[string]bool{"accepted": true})
}
func (a *app) catalogSourceRaw(w http.ResponseWriter, r *http.Request) {
	if a.catalogSync == nil {
		writeError(w, 503, "unavailable", "模型同步未配置")
		return
	}
	raw, err := a.catalogSync.Raw(r.Context(), r.PathValue("modelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"record": raw})
}
