package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func settingsResponse(value settings.Settings) map[string]any {
	hosts := value.ExtraBlockedHosts
	if hosts == nil {
		hosts = []string{}
	}
	return map[string]any{
		"fee_rate_nano":               value.FeeRateNano,
		"c2c_payment_timeout_minutes": value.C2CPaymentTimeoutMinutes,
		"default_credit_limit":        value.DefaultCreditLimit.String(),
		"default_max_attempts":        value.DefaultMaxAttempts,
		"default_ttft_timeout_ms":     value.DefaultTTFTTimeoutMS,
		"default_total_timeout_ms":    value.DefaultTotalTimeoutMS,
		"default_cooldown_failures":   value.DefaultCooldownFailures,
		"default_cooldown_seconds":    value.DefaultCooldownSeconds,
		"extra_blocked_hosts":         hosts,
		"updated_at":                  value.UpdatedAt,
	}
}

func (a *app) getAdminSettings(w http.ResponseWriter, r *http.Request) {
	value, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settingsResponse(value)})
}

func (a *app) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FeeRateNano              *int64    `json:"fee_rate_nano"`
		C2CPaymentTimeoutMinutes *int32    `json:"c2c_payment_timeout_minutes"`
		DefaultCreditLimit       *string   `json:"default_credit_limit"`
		DefaultMaxAttempts       *int32    `json:"default_max_attempts"`
		DefaultTTFTTimeoutMS     *int32    `json:"default_ttft_timeout_ms"`
		DefaultTotalTimeoutMS    *int32    `json:"default_total_timeout_ms"`
		DefaultCooldownFailures  *int32    `json:"default_cooldown_failures"`
		DefaultCooldownSeconds   *int32    `json:"default_cooldown_seconds"`
		ExtraBlockedHosts        *[]string `json:"extra_blocked_hosts"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.FeeRateNano == nil || request.C2CPaymentTimeoutMinutes == nil || request.DefaultCreditLimit == nil ||
		request.DefaultMaxAttempts == nil || request.DefaultTTFTTimeoutMS == nil || request.DefaultTotalTimeoutMS == nil ||
		request.DefaultCooldownFailures == nil || request.DefaultCooldownSeconds == nil || request.ExtraBlockedHosts == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请提交全部设置项")
		return
	}
	creditLimit, err := money.Parse(*request.DefaultCreditLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	updated, err := a.settings.Update(r.Context(), accountFromContext(r.Context()).ID, settings.Settings{
		FeeRateNano: *request.FeeRateNano, C2CPaymentTimeoutMinutes: *request.C2CPaymentTimeoutMinutes,
		DefaultCreditLimit: creditLimit, DefaultMaxAttempts: *request.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS: *request.DefaultTTFTTimeoutMS, DefaultTotalTimeoutMS: *request.DefaultTotalTimeoutMS,
		DefaultCooldownFailures: *request.DefaultCooldownFailures, DefaultCooldownSeconds: *request.DefaultCooldownSeconds,
		ExtraBlockedHosts: *request.ExtraBlockedHosts,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settingsResponse(updated)})
}

func auditEntryResponse(entry audit.Entry) map[string]any {
	var actor any
	if entry.Actor != nil {
		actor = map[string]any{"id": entry.Actor.ID, "username": entry.Actor.Username, "display_name": entry.Actor.DisplayName}
	}
	detail := json.RawMessage(entry.Detail)
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}
	return map[string]any{
		"id": strconv.FormatInt(entry.ID, 10), "actor": actor, "action": entry.Action,
		"target_type": entry.TargetType, "target_id": entry.TargetID, "reason": entry.Reason,
		"detail": detail, "created_at": entry.CreatedAt,
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
	items := make([]map[string]any, 0, len(entries))
	cursor := ""
	for _, entry := range entries {
		items = append(items, auditEntryResponse(entry))
		cursor = strconv.FormatInt(entry.ID, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nextCursor(hasMore, cursor)})
}

// registerAdminSettingsRoutes 注册平台设置与操作记录路由。
func (a *app) registerAdminSettingsRoutes(r *router) {
	r.admin("GET /api/admin/settings", a.getAdminSettings)
	r.admin("PUT /api/admin/settings", a.updateAdminSettings)
	r.admin("GET /api/admin/audit", a.listAdminAudit)
}
