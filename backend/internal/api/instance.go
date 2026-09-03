package api

import (
	"errors"
	"net/http"

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
	account, err := a.identity.CreateBootstrapAdmin(r.Context(), request.Username, request.DisplayName, request.Password)
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, http.StatusConflict, "already_initialized", "实例已有管理员，请直接登录")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"initialized": true,
		"admin": map[string]any{
			"id":            account.ID,
			"username":      account.Username,
			"display_name":  account.DisplayName,
			"must_change_password": account.MustChangePassword,
		},
	})
}
