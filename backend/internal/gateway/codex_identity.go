package gateway

import (
	"encoding/json"
	"net/http"
)

// ServeCodexIdentity is local platform authentication for the fixed client's
// personal_access_token mode. It has no supplier, catalog or billing dependency.
// The deliberately unknown plan does not claim official subscription rights.
func (e *Engine) ServeCodexIdentity(w http.ResponseWriter, r *http.Request, accounts bool) {
	key, _, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		WriteError(w, DialectOf(r), 401, "invalid_api_key", "API Key 或账号已停用或过期", "")
		return
	}
	account := "aihub-account-" + key.OwnerID
	var value any
	if accounts {
		value = map[string]any{
			"accounts":         []any{map[string]any{"id": account, "plan_type": nil, "workspace_backend_origin": "NO_CONSTRAINT", "account_routing_override": "NO_CONSTRAINT", "structure": "personal"}},
			"account_ordering": []string{account},
		}
	} else {
		value = map[string]any{"email": nil, "chatgpt_user_id": "aihub-user-" + key.OwnerID, "chatgpt_account_id": account, "chatgpt_plan_type": "aihub", "chatgpt_account_is_fedramp": false}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
