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
		ExchangeRate *string `json:"exchange_rate"`
	}
	if decodeJSON(w, r, &body) != nil {
		writeInvalidJSON(w)
		return
	}
	if body.ExchangeRate == nil {
		writeError(w, 422, "invalid_input", "请提供换算率，留空字符串暂停价格换算")
		return
	}
	if err := a.catalogSync.SetRate(r.Context(), accountFromContext(r.Context()).ID, *body.ExchangeRate); err != nil {
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
