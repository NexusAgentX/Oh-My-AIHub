package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, c2c.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, identity.ErrNotFound), errors.Is(err, catalog.ErrNotFound), errors.Is(err, ledger.ErrNotFound), errors.Is(err, channel.ErrNotFound), errors.Is(err, gateway.ErrNotFound), errors.Is(err, c2c.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, identity.ErrConflict), errors.Is(err, catalog.ErrConflict), errors.Is(err, channel.ErrConflict), errors.Is(err, gateway.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "资源状态冲突或标识已被使用")
	case errors.Is(err, ledger.ErrConflict), errors.Is(err, ledger.ErrHoldClosed), errors.Is(err, ledger.ErrHoldAmountExceeded):
		writeError(w, http.StatusConflict, "ledger_conflict", "账本操作与当前状态冲突")
	case errors.Is(err, c2c.ErrConflict):
		writeError(w, http.StatusConflict, "c2c_conflict", "订单或交易状态已变化，请刷新后重试")
	case errors.Is(err, c2c.ErrExpired):
		writeError(w, http.StatusConflict, "payment_deadline_expired", "付款期限已结束")
	case errors.Is(err, ledger.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_spendable_capacity", "可消费额度不足")
	case errors.Is(err, ledger.ErrCreditFrozen):
		writeError(w, http.StatusForbidden, "credit_frozen", "账户信用已冻结")
	case errors.Is(err, channel.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, gateway.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, gateway.ErrSnapshotRetry):
		writeError(w, http.StatusConflict, "snapshot_conflict", "资源正在更新，请重试")
	case errors.Is(err, channel.ErrUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "channel_unavailable", "至少需要一个通过当前验证的可用报价")
	case errors.Is(err, channel.ErrUnsafeUpstream):
		writeError(w, http.StatusUnprocessableEntity, "unsafe_upstream", "Base URL 无法通过安全解析")
	case errors.Is(err, identity.ErrInvalidInput), errors.Is(err, catalog.ErrInvalidInput), errors.Is(err, channel.ErrInvalidInput), errors.Is(err, gateway.ErrInvalidInput), errors.Is(err, ledger.ErrInvalidInput), errors.Is(err, ledger.ErrUnbalanced), errors.Is(err, ledger.ErrAmountOverflow), errors.Is(err, money.ErrInvalidAmount), errors.Is(err, c2c.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作")
	}
}
