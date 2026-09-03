package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

type apiPoolRequest struct {
	ModelID  string   `json:"model_id"`
	Protocol string   `json:"protocol"`
	OfferIDs []string `json:"offer_ids"`
}

type apiKeyConfigRequest struct {
	DisplayName     string           `json:"display_name"`
	ExpectedVersion int64            `json:"expected_version"`
	Pools           []apiPoolRequest `json:"pools"`
}

type apiKeyVersionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type apiKeyPoolMemberRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	ModelID         string `json:"model_id"`
	Protocol        string `json:"protocol"`
	OfferID         string `json:"offer_id"`
	Priority        int    `json:"priority"`
}

func (a *app) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	items, err := a.gateway.ListAPIKeys(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, apiKeyResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": responses})
}

func (a *app) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var request apiKeyConfigRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	created, err := a.gateway.CreateAPIKey(r.Context(), accountFromContext(r.Context()), gateway.KeyConfigInput{
		DisplayName: request.DisplayName,
		Pools:       apiPoolInputs(request.Pools),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": apiKeyResponse(created.APIKey), "secret": created.Secret})
}

func (a *app) getAPIKey(w http.ResponseWriter, r *http.Request) {
	item, err := a.gateway.GetAPIKey(r.Context(), accountFromContext(r.Context()), r.PathValue("keyID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": apiKeyResponse(item)})
}

func (a *app) updateAPIKey(w http.ResponseWriter, r *http.Request) {
	var request apiKeyConfigRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.gateway.UpdateAPIKey(r.Context(), accountFromContext(r.Context()), r.PathValue("keyID"), request.ExpectedVersion, gateway.KeyConfigInput{
		DisplayName: request.DisplayName,
		Pools:       apiPoolInputs(request.Pools),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": apiKeyResponse(updated)})
}

func (a *app) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	var request apiKeyVersionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	rotated, err := a.gateway.RotateAPIKey(r.Context(), accountFromContext(r.Context()), r.PathValue("keyID"), request.ExpectedVersion)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": apiKeyResponse(rotated.APIKey), "secret": rotated.Secret})
}

func (a *app) disableAPIKey(w http.ResponseWriter, r *http.Request) {
	a.setAPIKeyStatus(w, r, gateway.KeyDisabled)
}

func (a *app) enableAPIKey(w http.ResponseWriter, r *http.Request) {
	a.setAPIKeyStatus(w, r, gateway.KeyActive)
}

func (a *app) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	a.setAPIKeyStatus(w, r, gateway.KeyDeleted)
}

func (a *app) setAPIKeyStatus(w http.ResponseWriter, r *http.Request, status gateway.KeyStatus) {
	var request apiKeyVersionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.gateway.SetAPIKeyStatus(r.Context(), accountFromContext(r.Context()), r.PathValue("keyID"), request.ExpectedVersion, status)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": apiKeyResponse(updated)})
}

func (a *app) addAPIKeyPoolMember(w http.ResponseWriter, r *http.Request) {
	var request apiKeyPoolMemberRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.gateway.AddPoolMember(
		r.Context(), accountFromContext(r.Context()), r.PathValue("keyID"), request.ExpectedVersion,
		request.ModelID, channel.Protocol(request.Protocol), request.OfferID, request.Priority,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": apiKeyResponse(updated)})
}

func (a *app) listGatewayCalls(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeDomainError(w, gateway.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	items, err := a.gateway.ListCalls(r.Context(), accountFromContext(r.Context()), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, gatewayCallResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": responses})
}

func (a *app) getGatewayCall(w http.ResponseWriter, r *http.Request) {
	item, err := a.gateway.GetCall(r.Context(), accountFromContext(r.Context()), r.PathValue("callID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"call": gatewayCallResponse(item)})
}

func (a *app) gatewayDashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := a.gateway.Dashboard(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	recent := make([]map[string]any, 0, len(dashboard.RecentCalls))
	for _, item := range dashboard.RecentCalls {
		recent = append(recent, gatewayCallResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"consumer_spent": dashboard.ConsumerSpent.String(), "provider_income": dashboard.ProviderIncome.String(),
		"active_key_count": dashboard.ActiveKeyCount, "pool_count": dashboard.PoolCount,
		"healthy_offer_count": dashboard.HealthyOfferCount, "unhealthy_offer_count": dashboard.UnhealthyOfferCount,
		"pending_items": dashboard.PendingItems, "recent_calls": recent,
	})
}

func (a *app) proxyChatCompletions(w http.ResponseWriter, r *http.Request) {
	if rejectGatewayMethod(w, r, channel.ProtocolOpenAIChat) {
		return
	}
	a.serveGateway(w, r, channel.ProtocolOpenAIChat, "", false)
}

func (a *app) proxyResponses(w http.ResponseWriter, r *http.Request) {
	if rejectGatewayMethod(w, r, channel.ProtocolOpenAIResponse) {
		return
	}
	a.serveGateway(w, r, channel.ProtocolOpenAIResponse, "", false)
}

func (a *app) proxyAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if rejectGatewayMethod(w, r, channel.ProtocolAnthropic) {
		return
	}
	a.serveGateway(w, r, channel.ProtocolAnthropic, "", false)
}

func (a *app) proxyGemini(w http.ResponseWriter, r *http.Request) {
	if rejectGatewayMethod(w, r, channel.ProtocolGemini) {
		return
	}
	if a.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_unavailable", "网关暂不可用")
		return
	}
	modelID, stream, err := gateway.ParseGeminiRequestPath(r)
	if err != nil {
		gateway.WriteProtocolError(w, channel.ProtocolGemini, http.StatusBadRequest, "invalid_model_path", "模型路径无效", "")
		return
	}
	a.gateway.ServeProtocol(w, r, channel.ProtocolGemini, modelID, stream)
}

func rejectGatewayMethod(w http.ResponseWriter, r *http.Request, protocol channel.Protocol) bool {
	if r.Method == http.MethodPost {
		return false
	}
	gateway.WriteProtocolError(w, protocol, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST", "")
	return true
}

func (a *app) serveGateway(w http.ResponseWriter, r *http.Request, protocol channel.Protocol, modelID string, stream bool) {
	if a.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_unavailable", "网关暂不可用")
		return
	}
	a.gateway.ServeProtocol(w, r, protocol, modelID, stream)
}

func apiPoolInputs(requests []apiPoolRequest) []gateway.PoolInput {
	result := make([]gateway.PoolInput, 0, len(requests))
	for _, request := range requests {
		result = append(result, gateway.PoolInput{CanonicalModelID: request.ModelID, Protocol: channel.Protocol(request.Protocol), OfferIDs: request.OfferIDs})
	}
	return result
}

func apiKeyResponse(item gateway.APIKey) map[string]any {
	pools := make([]map[string]any, 0, len(item.Pools))
	for _, pool := range item.Pools {
		members := make([]map[string]any, 0, len(pool.Members))
		for _, member := range pool.Members {
			members = append(members, map[string]any{
				"priority": member.Priority, "offer_id": member.OfferID, "channel_id": member.ChannelID,
				"channel_name": member.ChannelDisplayName, "provider_name": member.OwnerDisplayName,
				"added_validation_version": member.AddedValidationVersion, "current_validation_version": member.CurrentValidationVersion,
				"eligible": member.Eligible, "ineligible_reason": member.IneligibleReason,
				"input_price": member.InputPrice.String(), "output_price": member.OutputPrice.String(),
				"cache_write_price": member.CacheWritePrice.String(), "cache_read_price": member.CacheReadPrice.String(),
				"success_rate": member.CallSuccessRate, "ttft_milliseconds": member.TTFTMilliseconds, "tokens_per_second": member.TokensPerSecond,
			})
		}
		pools = append(pools, map[string]any{
			"id": pool.ID, "model_id": pool.CanonicalModelID, "model_name": pool.ModelName,
			"protocol": pool.Protocol, "version": pool.Version, "members": members,
			"created_at": pool.CreatedAt, "updated_at": pool.UpdatedAt,
		})
	}
	return map[string]any{
		"id": item.ID, "display_name": item.DisplayName, "prefix": item.Prefix,
		"generation": item.Generation, "status": item.Status, "version": item.Version,
		"pools": pools, "last_used_at": item.LastUsedAt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func gatewayCallResponse(item gateway.Call) map[string]any {
	attempts := make([]map[string]any, 0, len(item.Attempts))
	for _, attempt := range item.Attempts {
		attempts = append(attempts, map[string]any{
			"id": attempt.ID, "sequence": attempt.Sequence, "offer_id": attempt.OfferID,
			"channel_name": attempt.ChannelDisplayName, "provider_account_id": attempt.ProviderAccountID,
			"status": attempt.Status, "http_status": attempt.HTTPStatus, "error_code": attempt.ErrorCode,
			"raw_error": attempt.RawError, "raw_error_truncated": attempt.RawErrorTruncated,
			"semantic_committed": attempt.SemanticCommitted, "ttft_milliseconds": durationMilliseconds(attempt.TTFT),
			"duration_milliseconds": durationMilliseconds(attempt.Duration), "usage": gatewayUsageResponse(attempt.Usage),
			"tokens_per_second": decimalNanoPointer(attempt.TokensPerSecondNano), "started_at": attempt.StartedAt, "completed_at": attempt.CompletedAt,
		})
	}
	return map[string]any{
		"id": item.ID, "consumer_account_id": item.ConsumerAccountID, "api_key_id": item.APIKeyID,
		"key_prefix": item.KeyPrefix, "key_generation": item.KeyGeneration, "pool_id": item.PoolID,
		"pool_version": item.PoolVersion, "model_id": item.CanonicalModelID, "protocol": item.Protocol,
		"status": item.Status, "decision_code": item.DecisionCode, "candidate_count": item.CandidateCount,
		"attempt_count": item.UpstreamAttemptCount, "hold_id": item.HoldID, "preauthorized": item.Preauthorized.String(),
		"zero_hold_reason": item.ZeroHoldReason, "fee_rate_version": item.FeeRateVersion, "fee_rate_nano": item.FeeRateNano,
		"final_offer_id": item.FinalOfferID, "final_channel_name": item.FinalChannelName,
		"completion_reason": item.CompletionReason, "usage": gatewayUsageResponse(item.Usage),
		"provider_charge": item.ProviderCharge.String(), "platform_fee": item.PlatformFee.String(),
		"final_http_status": item.FinalHTTPStatus, "attempts": attempts, "created_at": item.CreatedAt, "completed_at": item.CompletedAt,
	}
}

func gatewayUsageResponse(usage *ledger.UsageV1) any {
	if usage == nil {
		return nil
	}
	return map[string]int64{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "cache_write_tokens": usage.CacheWriteTokens, "cache_read_tokens": usage.CacheReadTokens}
}

func decimalNano(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	result := strconv.FormatInt(value/1_000_000_000, 10) + "." + strconv.FormatInt(value%1_000_000_000+1_000_000_000, 10)[1:]
	if negative {
		return "-" + result
	}
	return result
}

func decimalNanoPointer(value *int64) any {
	if value == nil {
		return nil
	}
	return decimalNano(*value)
}

func durationMilliseconds(value *time.Duration) any {
	if value == nil {
		return nil
	}
	return value.Milliseconds()
}
