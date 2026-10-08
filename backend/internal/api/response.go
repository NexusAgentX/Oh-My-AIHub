package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
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

// decodeOptionalJSON accepts an empty body as an empty object.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.ContentLength == 0 && r.Header.Get("Transfer-Encoding") == "" {
		return nil
	}
	return decodeJSON(w, r, target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError writes the unified error shape {"error": code, "message": text}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeInvalidJSON(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
}

// notImplemented answers every route that is in the contract but belongs to a
// later feature of Epic #170.
func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "该功能尚未实现")
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, identity.ErrNotFound), errors.Is(err, catalog.ErrNotFound), errors.Is(err, ledger.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, identity.ErrLastAdministrator):
		writeError(w, http.StatusConflict, "last_administrator", "不能移除最后一个启用的管理员")
	case errors.Is(err, identity.ErrSelfModification):
		writeError(w, http.StatusUnprocessableEntity, "cannot_modify_self", "不能停用、降级或重置自己的账户")
	case errors.Is(err, identity.ErrConflict), errors.Is(err, catalog.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "资源状态冲突或标识已被使用")
	case errors.Is(err, ledger.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "幂等键已用于其他操作")
	case errors.Is(err, ledger.ErrNothingToWriteOff):
		writeError(w, http.StatusUnprocessableEntity, "nothing_to_write_off", "余额不为负，无需核销")
	case errors.Is(err, channel.ErrNotFound), errors.Is(err, apikey.ErrNotFound), errors.Is(err, routing.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, apikey.ErrLimitReached):
		writeError(w, http.StatusConflict, "key_limit_reached", "API Key 数量已达上限")
	case errors.Is(err, channel.ErrSuspended):
		writeError(w, http.StatusConflict, "channel_suspended", "渠道已被管理员下架，不能自行上架")
	case errors.Is(err, channel.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "资源状态冲突或标识已被使用")
	case errors.Is(err, channel.ErrUnsafeUpstream):
		writeError(w, http.StatusUnprocessableEntity, "unsafe_upstream", "上游地址未通过出站安全校验")
	case errors.Is(err, channel.ErrInvalidInput), errors.Is(err, apikey.ErrInvalidInput), errors.Is(err, routing.ErrInvalidInput),
		errors.Is(err, identity.ErrInvalidInput), errors.Is(err, catalog.ErrInvalidInput), errors.Is(err, ledger.ErrInvalidInput),
		errors.Is(err, ledger.ErrUnbalanced), errors.Is(err, ledger.ErrAmountOverflow), errors.Is(err, money.ErrInvalidAmount),
		errors.Is(err, settings.ErrInvalidInput), errors.Is(err, audit.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "请检查提交内容")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "服务暂时无法完成操作")
	}
}

// pageLimit parses ?limit= (1..100, default 20).
func pageLimit(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		return 0, false
	}
	return limit, true
}

// idCursor parses a numeric "before id" cursor; empty means the first page.
func idCursor(r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

func encodeTextCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeTextCursor(raw string) (string, bool) {
	value, err := base64.RawURLEncoding.DecodeString(raw)
	return string(value), err == nil
}

func nextCursor(hasMore bool, cursor string) *string {
	if !hasMore {
		return nil
	}
	return &cursor
}

func writeBadCursor(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid_cursor", "分页参数无效")
}

// idempotencyKey namespaces the client's Idempotency-Key, or generates one.
func idempotencyKey(r *http.Request, namespace string) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", false
		}
		key = hex.EncodeToString(random)
	}
	if len(key) > 128 {
		return "", false
	}
	return namespace + ":" + key, true
}
