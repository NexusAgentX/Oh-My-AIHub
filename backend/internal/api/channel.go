package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type offerRequest struct {
	ModelID         string `json:"model_id"`
	Protocol        string `json:"protocol"`
	UpstreamModelID string `json:"upstream_model_id"`
	Multiplier      string `json:"multiplier"`
	ExpectedVersion int64  `json:"expected_version"`
}

type offerUpdateRequest struct {
	UpstreamModelID string `json:"upstream_model_id"`
	Multiplier      string `json:"multiplier"`
	ExpectedVersion int64  `json:"expected_version"`
}

type createChannelRequest struct {
	DisplayName string         `json:"display_name"`
	BaseURL     string         `json:"base_url"`
	Credential  string         `json:"credential"`
	Offers      []offerRequest `json:"offers"`
}

type updateChannelRequest struct {
	DisplayName     string  `json:"display_name"`
	BaseURL         string  `json:"base_url"`
	Credential      *string `json:"credential"`
	ExpectedVersion int64   `json:"expected_version"`
}

type versionRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type validationRequest struct {
	ConfirmedUpstreamCost bool `json:"confirmed_upstream_cost"`
}

type ratingRequest struct {
	Score int `json:"score"`
}

func (a *app) listChannels(w http.ResponseWriter, r *http.Request) {
	items, err := a.channels.ListMine(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, ownerChannelResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": responses})
}

func (a *app) createChannel(w http.ResponseWriter, r *http.Request) {
	var request createChannelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	offers, err := parseOfferRequests(request.Offers)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	created, err := a.channels.Create(r.Context(), accountFromContext(r.Context()), request.DisplayName, request.BaseURL, request.Credential, offers)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"channel": ownerChannelResponse(created)})
}

func (a *app) getChannel(w http.ResponseWriter, r *http.Request) {
	item, err := a.channels.GetMine(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": ownerChannelResponse(item)})
}

func (a *app) updateChannel(w http.ResponseWriter, r *http.Request) {
	var request updateChannelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.channels.Update(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.ExpectedVersion, request.DisplayName, request.BaseURL, request.Credential)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": ownerChannelResponse(updated)})
}

func (a *app) publishChannel(w http.ResponseWriter, r *http.Request) {
	a.setOwnerChannelStatus(w, r, channel.StatusPublished)
}

func (a *app) pauseChannel(w http.ResponseWriter, r *http.Request) {
	a.setOwnerChannelStatus(w, r, channel.StatusPaused)
}

func (a *app) deleteChannel(w http.ResponseWriter, r *http.Request) {
	a.setOwnerChannelStatus(w, r, channel.StatusDeleted)
}

func (a *app) setOwnerChannelStatus(w http.ResponseWriter, r *http.Request, target channel.Status) {
	var request versionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.channels.SetStatus(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.ExpectedVersion, target, request.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": ownerChannelResponse(updated)})
}

func (a *app) revokeChannelCredential(w http.ResponseWriter, r *http.Request) {
	var request versionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.channels.RevokeCredential(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.ExpectedVersion)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": ownerChannelResponse(updated)})
}

func (a *app) addChannelOffer(w http.ResponseWriter, r *http.Request) {
	var request offerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	input, err := parseOfferRequest(request)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	created, err := a.channels.AddOffer(
		r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.ExpectedVersion, input,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"offer": ownerOfferResponse(created)})
}

func (a *app) updateChannelOffer(w http.ResponseWriter, r *http.Request) {
	var request offerUpdateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	multiplier, err := parseMultiplier(request.Multiplier)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	updated, err := a.channels.UpdateOffer(r.Context(), accountFromContext(r.Context()), r.PathValue("offerID"), request.ExpectedVersion, request.UpstreamModelID, multiplier)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"offer": ownerOfferResponse(updated)})
}

func (a *app) disableChannelOffer(w http.ResponseWriter, r *http.Request) {
	a.setOfferStatus(w, r, channel.OfferDisabled)
}

func (a *app) resumeChannelOffer(w http.ResponseWriter, r *http.Request) {
	a.setOfferStatus(w, r, channel.OfferActive)
}

func (a *app) deleteChannelOffer(w http.ResponseWriter, r *http.Request) {
	a.setOfferStatus(w, r, channel.OfferDeleted)
}

func (a *app) setOfferStatus(w http.ResponseWriter, r *http.Request, status channel.OfferStatus) {
	var request versionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	updated, err := a.channels.SetOfferStatus(r.Context(), accountFromContext(r.Context()), r.PathValue("offerID"), request.ExpectedVersion, status)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"offer": ownerOfferResponse(updated)})
}

func (a *app) validateChannelOffer(w http.ResponseWriter, r *http.Request) {
	var request validationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	attempt, err := a.channels.ValidateOffer(r.Context(), accountFromContext(r.Context()), r.PathValue("offerID"), request.ConfirmedUpstreamCost)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"validation": validationResponse(attempt, true)})
}

func (a *app) listOfferValidationAttempts(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeDomainError(w, channel.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	attempts, err := a.channels.ListValidationAttempts(r.Context(), accountFromContext(r.Context()), r.PathValue("offerID"), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		responses = append(responses, validationResponse(attempt, true))
	}
	writeJSON(w, http.StatusOK, map[string]any{"validation_attempts": responses})
}

func (a *app) listMarketOffers(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeDomainError(w, channel.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	items, next, err := a.channels.ListMarket(r.Context(), accountFromContext(r.Context()), channel.MarketQuery{
		ModelID: r.URL.Query().Get("model_id"), Protocol: channel.Protocol(r.URL.Query().Get("protocol")),
		OwnerQuery: r.URL.Query().Get("owner"), Sort: r.URL.Query().Get("sort"),
		Cursor: r.URL.Query().Get("after"), Limit: limit,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, marketOfferResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"offers": responses, "next_after": next})
}

func (a *app) getMarketChannel(w http.ResponseWriter, r *http.Request) {
	item, err := a.channels.GetMarketChannel(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": marketChannelResponse(item)})
}

func (a *app) rateMarketChannel(w http.ResponseWriter, r *http.Request) {
	var request ratingRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	item, err := a.channels.Rate(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.Score)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": marketChannelResponse(item)})
}

func (a *app) listAdminChannels(w http.ResponseWriter, r *http.Request) {
	items, err := a.channels.ListAdmin(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, adminChannelResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": responses})
}

func (a *app) getAdminChannel(w http.ResponseWriter, r *http.Request) {
	item, err := a.channels.GetAdmin(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": adminChannelResponse(item)})
}

func (a *app) adminPauseChannel(w http.ResponseWriter, r *http.Request) {
	a.adminSetChannelStatus(w, r, channel.StatusPaused)
}

func (a *app) adminDeleteChannel(w http.ResponseWriter, r *http.Request) {
	a.adminSetChannelStatus(w, r, channel.StatusDeleted)
}

func (a *app) adminSetChannelStatus(w http.ResponseWriter, r *http.Request, target channel.Status) {
	var request versionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	item, err := a.channels.AdminSetStatus(r.Context(), accountFromContext(r.Context()), r.PathValue("channelID"), request.ExpectedVersion, target, request.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": adminChannelResponse(item)})
}

func (a *app) reencryptChannelCredentials(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Limit int `json:"limit"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	count, err := a.channels.ReencryptCredentials(r.Context(), accountFromContext(r.Context()), request.Limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reencrypted": count})
}

func parseOfferRequests(requests []offerRequest) ([]channel.OfferInput, error) {
	result := make([]channel.OfferInput, 0, len(requests))
	for _, request := range requests {
		value, err := parseOfferRequest(request)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func parseOfferRequest(request offerRequest) (channel.OfferInput, error) {
	multiplier, err := parseMultiplier(request.Multiplier)
	if err != nil {
		return channel.OfferInput{}, err
	}
	return channel.OfferInput{ModelID: request.ModelID, Protocol: channel.Protocol(request.Protocol), UpstreamModelID: request.UpstreamModelID, Multiplier: multiplier}, nil
}

func parseMultiplier(value string) (money.Amount, error) {
	value = strings.TrimSpace(value)
	amount, err := money.Parse(value)
	if err != nil || amount < 0 || amount > money.Amount(1000*money.Scale) {
		return 0, channel.ErrInvalidInput
	}
	return amount, nil
}

func ownerChannelResponse(item channel.Channel) map[string]any {
	offers := make([]map[string]any, 0, len(item.Offers))
	for _, offer := range item.Offers {
		offers = append(offers, ownerOfferResponse(offer))
	}
	return map[string]any{
		"id": item.ID, "owner_account_id": item.OwnerAccountID, "owner_display_name": item.OwnerDisplayName,
		"display_name": item.DisplayName, "base_url": item.NormalizedBaseURL,
		"credential_configured": item.CredentialConfigured, "credential_version": item.CredentialVersion,
		"credential_updated_at": item.CredentialUpdatedAt, "status": item.Status, "version": item.Version,
		"offers": offers, "average_rating": item.AverageRating, "rating_count": item.RatingCount,
		"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func ownerOfferResponse(item channel.Offer) map[string]any {
	prices, priceErr := channel.CalculateBenchmarkPrices(item)
	var input, output, cacheWrite, cacheRead, providerIncome any
	eligible, ineligibleReason := item.Eligible, item.IneligibleReason
	if priceErr == nil {
		input, output = prices.Input.String(), prices.Output.String()
		cacheWrite, cacheRead = prices.CacheWrite.String(), prices.CacheRead.String()
	} else {
		eligible, ineligibleReason = false, "price_unrepresentable"
	}
	if item.ProviderIncome != nil {
		providerIncome = item.ProviderIncome.String()
	}
	return map[string]any{
		"id": item.ID, "model_id": item.ModelID, "model_name": item.ModelName, "model_provider": item.ModelProvider,
		"protocol": item.Protocol, "upstream_model_id": item.UpstreamModelID, "multiplier": item.Multiplier.String(),
		"status": item.Status, "validation_version": item.ValidationVersion, "version": item.Version,
		"eligible": eligible, "ineligible_reason": ineligibleReason,
		"input_price": input, "output_price": output, "cache_write_price": cacheWrite, "cache_read_price": cacheRead,
		"call_success_rate": item.CallSuccessRate, "ttft_milliseconds": item.TTFTMilliseconds,
		"tokens_per_second": item.TokensPerSecond, "call_count": item.CallCount, "provider_income": providerIncome,
		"latest_validation": validationResponsePointer(item.LatestValidation, false), "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func marketOfferResponse(item channel.MarketOffer) map[string]any {
	return map[string]any{
		"offer_id": item.OfferID, "channel_id": item.ChannelID, "channel_display_name": item.ChannelDisplayName,
		"owner_account_id": item.OwnerAccountID, "owner_display_name": item.OwnerDisplayName,
		"model_id": item.ModelID, "model_name": item.ModelName, "model_provider": item.ModelProvider,
		"protocol": item.Protocol, "multiplier": item.Multiplier.String(), "input_price": item.InputPrice.String(),
		"output_price": item.OutputPrice.String(), "cache_write_price": item.CacheWritePrice.String(), "cache_read_price": item.CacheReadPrice.String(),
		"validation_status": item.ValidationStatus, "average_rating": item.AverageRating, "rating_count": item.RatingCount,
		"last_tested_at":    item.LastTestedAt,
		"call_success_rate": item.CallSuccessRate, "ttft_milliseconds": item.TTFTMilliseconds,
		"tokens_per_second": item.TokensPerSecond, "call_count": item.CallCount,
	}
}

func marketChannelResponse(item channel.Channel) map[string]any {
	offers := make([]map[string]any, 0, len(item.Offers))
	for _, offer := range item.Offers {
		prices, err := channel.CalculateBenchmarkPrices(offer)
		if err != nil {
			continue
		}
		offers = append(offers, marketOfferResponse(channel.MarketOffer{
			OfferID: offer.ID, ChannelID: item.ID, ChannelDisplayName: item.DisplayName,
			OwnerAccountID: item.OwnerAccountID, OwnerDisplayName: item.OwnerDisplayName,
			ModelID: offer.ModelID, ModelName: offer.ModelName, ModelProvider: offer.ModelProvider,
			Protocol: offer.Protocol, Multiplier: offer.Multiplier, InputPrice: prices.Input, OutputPrice: prices.Output,
			CacheWritePrice: prices.CacheWrite, CacheReadPrice: prices.CacheRead, ValidationStatus: channel.ValidationPassed,
			LastTestedAt:  offer.LatestValidation.CompletedAt,
			AverageRating: item.AverageRating, RatingCount: item.RatingCount,
			CallSuccessRate: offer.CallSuccessRate, TTFTMilliseconds: offer.TTFTMilliseconds,
			TokensPerSecond: offer.TokensPerSecond, CallCount: offer.CallCount,
		}))
	}
	return map[string]any{
		"id": item.ID, "display_name": item.DisplayName, "owner_account_id": item.OwnerAccountID,
		"owner_display_name": item.OwnerDisplayName, "status": item.Status, "offers": offers,
		"average_rating": item.AverageRating, "rating_count": item.RatingCount, "current_user_rating": item.CurrentUserRating,
	}
}

func adminChannelResponse(item channel.Channel) map[string]any {
	offers := make([]map[string]any, 0, len(item.Offers))
	for _, offer := range item.Offers {
		offers = append(offers, map[string]any{
			"id": offer.ID, "model_id": offer.ModelID, "model_name": offer.ModelName, "model_provider": offer.ModelProvider,
			"protocol": offer.Protocol, "multiplier": offer.Multiplier.String(), "status": offer.Status,
			"validation_version": offer.ValidationVersion, "latest_validation": validationResponsePointer(offer.LatestValidation, false),
		})
	}
	return map[string]any{
		"id": item.ID, "owner_account_id": item.OwnerAccountID, "owner_display_name": item.OwnerDisplayName,
		"display_name": item.DisplayName, "credential_configured": item.CredentialConfigured,
		"credential_version": item.CredentialVersion, "credential_updated_at": item.CredentialUpdatedAt,
		"status": item.Status, "version": item.Version, "offers": offers,
		"average_rating": item.AverageRating, "rating_count": item.RatingCount, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func validationResponsePointer(attempt *channel.ValidationAttempt, includeRaw bool) any {
	if attempt == nil {
		return nil
	}
	return validationResponse(*attempt, includeRaw)
}

func validationResponse(attempt channel.ValidationAttempt, includeRaw bool) map[string]any {
	response := map[string]any{
		"id": attempt.ID, "validation_version": attempt.ValidationVersion, "attempt_seq": attempt.AttemptSeq,
		"status": attempt.Status, "error_category": attempt.ErrorCategory,
		"http_status":           nullableHTTPStatus(attempt.HTTPStatus),
		"duration_milliseconds": attempt.Duration.Milliseconds(), "started_at": attempt.StartedAt, "completed_at": attempt.CompletedAt,
	}
	if includeRaw {
		response["actor_account_id"] = attempt.ActorAccountID
		response["raw_error"] = attempt.RawError
		response["raw_error_truncated"] = attempt.RawErrorTruncated
	}
	return response
}

func nullableHTTPStatus(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
