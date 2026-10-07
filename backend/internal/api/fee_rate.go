package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type feeRateRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	FeeRate         string `json:"fee_rate"`
	Reason          string `json:"reason"`
}

// feeRatePattern accepts a ratio between 0 and 1 with at most nine decimals.
var feeRatePattern = regexp.MustCompile(`^(0|1)(\.[0-9]{1,9})?$`)

func parseFeeRate(value string) (money.Amount, error) {
	value = strings.TrimSpace(value)
	if !feeRatePattern.MatchString(value) {
		return 0, feerate.ErrInvalidInput
	}
	rate, err := money.Parse(value)
	if err != nil || rate < 0 || rate > feerate.MaxRate {
		return 0, feerate.ErrInvalidInput
	}
	return rate, nil
}

func (a *app) adminFeeRates(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > feerate.MaxListLimit {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit 必须在 1 到 100 之间")
			return
		}
		limit = parsed
	}
	versions, err := a.feeRates.List(r.Context(), accountFromContext(r.Context()), limit)
	if err != nil {
		writeFeeRateError(w, err)
		return
	}
	history := make([]map[string]any, 0, len(versions))
	for _, version := range versions {
		history = append(history, feeRateResponse(version))
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": history[0], "history": history})
}

func (a *app) adminSetFeeRate(w http.ResponseWriter, r *http.Request) {
	var request feeRateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	rate, err := parseFeeRate(request.FeeRate)
	if err != nil {
		writeFeeRateError(w, err)
		return
	}
	created, err := a.feeRates.Set(r.Context(), accountFromContext(r.Context()), request.ExpectedVersion, rate, request.Reason)
	if err != nil {
		writeFeeRateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fee_rate": feeRateResponse(created)})
}

func feeRateResponse(version feerate.Version) map[string]any {
	var createdBy any
	if version.CreatedByID != "" {
		createdBy = map[string]string{"id": version.CreatedByID, "username": version.CreatedByUsername}
	}
	return map[string]any{
		"version":    version.Version,
		"fee_rate":   version.Rate.String(),
		"reason":     version.Reason,
		"created_by": createdBy,
		"created_at": version.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeFeeRateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, feerate.ErrConflict):
		writeError(w, http.StatusConflict, "fee_rate_conflict", "手续费率已被更新，请刷新后重试")
	case errors.Is(err, feerate.ErrUnchanged):
		writeError(w, http.StatusUnprocessableEntity, "fee_rate_unchanged", "新费率与当前费率相同")
	case errors.Is(err, feerate.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
	default:
		writeDomainError(w, err)
	}
}
