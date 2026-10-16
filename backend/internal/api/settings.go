package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// settingsJSON is the OpenAPI Settings schema.
type settingsJSON struct {
	Codex                    settings.Codex `json:"codex"`
	FeeRateNano              int64          `json:"fee_rate_nano"`
	C2CPaymentTimeoutMinutes int32          `json:"c2c_payment_timeout_minutes"`
	DefaultCreditLimit       string         `json:"default_credit_limit"`
	DefaultMaxAttempts       int32          `json:"default_max_attempts"`
	DefaultTTFTTimeoutMS     int32          `json:"default_ttft_timeout_ms"`
	DefaultRequestTimeoutMS  int32          `json:"default_request_timeout_ms"`
	DefaultIdleTimeoutMS     int32          `json:"default_idle_timeout_ms"`
	DefaultCooldownFailures  int32          `json:"default_cooldown_failures"`
	DefaultCooldownSeconds   int32          `json:"default_cooldown_seconds"`
	AccountMaxInflightCalls  int32          `json:"account_max_inflight_calls"`
	ExtraBlockedHosts        []string       `json:"extra_blocked_hosts"`
	USDCNYRate               string         `json:"usd_cny_rate"`
	FXAutoSync               bool           `json:"fx_auto_sync"`
	FXSyncMinute             int32          `json:"fx_sync_minute"`
	USDCNYRateSource         string         `json:"usd_cny_rate_source"`
	USDCNYRateUpdatedAt      time.Time      `json:"usd_cny_rate_updated_at"`
	FXSyncAttemptedAt        *time.Time     `json:"fx_sync_attempted_at"`
	FXSyncError              *string        `json:"fx_sync_error"`
	UpdatedAt                time.Time      `json:"updated_at"`
}

// settingsEnvelopeJSON is the OpenAPI SettingsEnvelope schema.
type settingsEnvelopeJSON struct {
	Settings settingsJSON `json:"settings"`
}

func newSettingsJSON(value settings.Settings) settingsJSON {
	hosts := value.ExtraBlockedHosts
	if hosts == nil {
		hosts = []string{}
	}
	return settingsJSON{
		Codex:                    value.Codex,
		FeeRateNano:              value.FeeRateNano,
		C2CPaymentTimeoutMinutes: value.C2CPaymentTimeoutMinutes,
		DefaultCreditLimit:       value.DefaultCreditLimit.String(),
		DefaultMaxAttempts:       value.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS:     value.DefaultTTFTTimeoutMS,
		DefaultRequestTimeoutMS:  value.DefaultRequestTimeoutMS,
		DefaultIdleTimeoutMS:     value.DefaultIdleTimeoutMS,
		DefaultCooldownFailures:  value.DefaultCooldownFailures,
		DefaultCooldownSeconds:   value.DefaultCooldownSeconds,
		AccountMaxInflightCalls:  value.AccountMaxInflightCalls,
		ExtraBlockedHosts:        hosts,
		USDCNYRate:               money.FromNano(value.USDCNYRateNano).String(),
		FXAutoSync:               value.FXAutoSync,
		FXSyncMinute:             value.FXSyncMinute,
		USDCNYRateSource:         value.USDCNYRateSource,
		USDCNYRateUpdatedAt:      value.USDCNYRateUpdatedAt,
		FXSyncAttemptedAt:        value.FXSyncAttemptedAt,
		FXSyncError:              value.FXSyncError,
		UpdatedAt:                value.UpdatedAt,
	}
}

// FormDefaults contains only the settings needed to explain inherited form values.
type formDefaultsJSON struct {
	DefaultMaxAttempts      int32 `json:"default_max_attempts"`
	DefaultTTFTTimeoutMS    int32 `json:"default_ttft_timeout_ms"`
	DefaultRequestTimeoutMS int32 `json:"default_request_timeout_ms"`
	DefaultIdleTimeoutMS    int32 `json:"default_idle_timeout_ms"`
	DefaultCooldownFailures int32 `json:"default_cooldown_failures"`
	DefaultCooldownSeconds  int32 `json:"default_cooldown_seconds"`
}

func (a *app) getFormDefaults(w http.ResponseWriter, r *http.Request) {
	value, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, formDefaultsJSON{
		DefaultMaxAttempts:      value.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS:    value.DefaultTTFTTimeoutMS,
		DefaultRequestTimeoutMS: value.DefaultRequestTimeoutMS,
		DefaultIdleTimeoutMS:    value.DefaultIdleTimeoutMS,
		DefaultCooldownFailures: value.DefaultCooldownFailures,
		DefaultCooldownSeconds:  value.DefaultCooldownSeconds,
	})
}

func (a *app) getAdminSettings(w http.ResponseWriter, r *http.Request) {
	value, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsEnvelopeJSON{Settings: newSettingsJSON(value)})
}

func (a *app) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Codex                    *settings.Codex `json:"codex"`
		FeeRateNano              *int64          `json:"fee_rate_nano"`
		C2CPaymentTimeoutMinutes *int32          `json:"c2c_payment_timeout_minutes"`
		DefaultCreditLimit       *string         `json:"default_credit_limit"`
		DefaultMaxAttempts       *int32          `json:"default_max_attempts"`
		DefaultTTFTTimeoutMS     *int32          `json:"default_ttft_timeout_ms"`
		DefaultRequestTimeoutMS  *int32          `json:"default_request_timeout_ms"`
		DefaultIdleTimeoutMS     *int32          `json:"default_idle_timeout_ms"`
		DefaultCooldownFailures  *int32          `json:"default_cooldown_failures"`
		DefaultCooldownSeconds   *int32          `json:"default_cooldown_seconds"`
		AccountMaxInflightCalls  *int32          `json:"account_max_inflight_calls"`
		ExtraBlockedHosts        *[]string       `json:"extra_blocked_hosts"`
		USDCNYRate               *string         `json:"usd_cny_rate"`
		FXAutoSync               *bool           `json:"fx_auto_sync"`
		FXSyncMinute             *int32          `json:"fx_sync_minute"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.FeeRateNano == nil || request.C2CPaymentTimeoutMinutes == nil || request.DefaultCreditLimit == nil ||
		request.DefaultMaxAttempts == nil || request.DefaultTTFTTimeoutMS == nil || request.DefaultRequestTimeoutMS == nil || request.DefaultIdleTimeoutMS == nil ||
		request.DefaultCooldownFailures == nil || request.DefaultCooldownSeconds == nil || request.AccountMaxInflightCalls == nil ||
		request.ExtraBlockedHosts == nil ||
		request.USDCNYRate == nil || request.FXAutoSync == nil || request.FXSyncMinute == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请提交全部设置项")
		return
	}
	creditLimit, err := money.Parse(*request.DefaultCreditLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	rate, err := money.Parse(*request.USDCNYRate)
	if err != nil {
		writeDomainError(w, settings.ErrInvalidInput)
		return
	}
	codex := settings.Codex{}
	if request.Codex != nil {
		codex = *request.Codex
	} else {
		current, err := a.settings.Get(r.Context())
		if err != nil {
			writeDomainError(w, err)
			return
		}
		codex = current.Codex
	}
	updated, err := a.settings.Update(r.Context(), accountFromContext(r.Context()).ID, settings.Settings{
		Codex: codex, FeeRateNano: *request.FeeRateNano, C2CPaymentTimeoutMinutes: *request.C2CPaymentTimeoutMinutes,
		DefaultCreditLimit: creditLimit, DefaultMaxAttempts: *request.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS: *request.DefaultTTFTTimeoutMS, DefaultRequestTimeoutMS: *request.DefaultRequestTimeoutMS,
		DefaultIdleTimeoutMS:    *request.DefaultIdleTimeoutMS,
		DefaultCooldownFailures: *request.DefaultCooldownFailures, DefaultCooldownSeconds: *request.DefaultCooldownSeconds,
		AccountMaxInflightCalls: *request.AccountMaxInflightCalls, ExtraBlockedHosts: *request.ExtraBlockedHosts,
		USDCNYRateNano: rate.Nano(), FXAutoSync: *request.FXAutoSync, FXSyncMinute: *request.FXSyncMinute,
	})
	if err != nil {
		if errors.Is(err, settings.ErrInvalidInput) {
			writeError(w, 422, "invalid_input", err.Error())
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsEnvelopeJSON{Settings: newSettingsJSON(updated)})
}

// syncExchangeRate fetches the USD/CNY rate now. A failed fetch is stored as
// the sync error and reported as 502 with the reason.
func (a *app) syncExchangeRate(w http.ResponseWriter, r *http.Request) {
	if a.rateSync == nil {
		writeError(w, http.StatusServiceUnavailable, "exchange_rate_unavailable", "未配置汇率同步")
		return
	}
	updated, err := a.rateSync.Sync(r.Context(), accountFromContext(r.Context()).ID)
	if err != nil {
		if r.Context().Err() == nil {
			writeError(w, http.StatusBadGateway, "exchange_rate_sync_failed", "汇率同步失败："+err.Error())
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsEnvelopeJSON{Settings: newSettingsJSON(updated)})
}

// auditEntryJSON is the OpenAPI AuditEntry schema.
type auditEntryJSON struct {
	ID         string          `json:"id"`
	Actor      *accountRefJSON `json:"actor"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Reason     string          `json:"reason"`
	Detail     json.RawMessage `json:"detail"`
	CreatedAt  time.Time       `json:"created_at"`
}

// auditPageJSON is the OpenAPI AuditPage schema.
type auditPageJSON struct {
	Items      []auditEntryJSON `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func newAuditEntryJSON(entry audit.Entry) auditEntryJSON {
	var actor *accountRefJSON
	if entry.Actor != nil {
		actor = &accountRefJSON{ID: entry.Actor.ID, Username: entry.Actor.Username, DisplayName: entry.Actor.DisplayName}
	}
	detail := json.RawMessage(entry.Detail)
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}
	return auditEntryJSON{
		ID:         strconv.FormatInt(entry.ID, 10),
		Actor:      actor,
		Action:     entry.Action,
		TargetType: entry.TargetType,
		TargetID:   entry.TargetID,
		Reason:     entry.Reason,
		Detail:     detail,
		CreatedAt:  entry.CreatedAt,
	}
}

func (a *app) listAdminAudit(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	before, cursorOK := idCursor(r)
	if !ok || !cursorOK {
		writeBadCursor(w)
		return
	}
	query := r.URL.Query()
	if actorID := query.Get("actor_id"); actorID != "" && !uuidPattern.MatchString(actorID) {
		writeError(w, http.StatusBadRequest, "invalid_request", "actor_id 无效")
		return
	}
	entries, err := a.audit.List(r.Context(), audit.Filter{
		Action: query.Get("action"), TargetType: query.Get("target_type"), TargetID: query.Get("target_id"),
		ActorID: query.Get("actor_id"), BeforeID: before, Limit: limit + 1,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	items := make([]auditEntryJSON, 0, len(entries))
	cursor := ""
	for _, entry := range entries {
		items = append(items, newAuditEntryJSON(entry))
		cursor = strconv.FormatInt(entry.ID, 10)
	}
	writeJSON(w, http.StatusOK, auditPageJSON{Items: items, NextCursor: nextCursor(hasMore, cursor)})
}

// registerAdminSettingsRoutes 注册平台设置与操作记录路由。
func (a *app) registerAdminSettingsRoutes(r *router) {
	r.ready("GET /api/form-defaults", a.getFormDefaults)
	r.admin("GET /api/admin/settings", a.getAdminSettings)
	r.admin("PUT /api/admin/settings", a.updateAdminSettings)
	r.admin("POST /api/admin/settings/exchange-rate/sync", a.syncExchangeRate)
	r.admin("GET /api/admin/audit", a.listAdminAudit)
}
