package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

func (a *app) instanceState(w http.ResponseWriter, r *http.Request) {
	initialized, err := a.identity.HasAdministrator(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"initialized": initialized})
}

func (a *app) instanceInitialize(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	initialized, err := a.identity.HasAdministrator(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if initialized {
		writeError(w, http.StatusConflict, "already_initialized", "实例已有管理员，请直接登录")
		return
	}
	_, err = a.identity.CreateBootstrapAdmin(r.Context(), request.Username, request.DisplayName, request.Password)
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, http.StatusConflict, "already_initialized", "实例已有管理员，请直接登录")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	// 密码为创始人自设，创建成功即建立会话，免去登录一步。
	result, err := a.identity.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	a.setSessionCookie(w, result.SessionToken)
	writeJSON(w, http.StatusCreated, map[string]any{
		"initialized": true,
		"account":     accountResponse(result.Account),
	})
}

// registerInstanceRoutes 注册实例与健康检查路由。
func (a *app) registerInstanceRoutes(r *router) {
	r.public("GET /api/health", a.health)
	r.public("GET /api/instance", a.instanceState)
	r.public("POST /api/instance/initialize", a.instanceInitialize)
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if a.databaseReady != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.databaseReady(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "oh-my-aihub-backend",
		"status":  "ok",
	})
}
