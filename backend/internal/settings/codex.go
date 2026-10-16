package settings

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Codex stores exact response documents as strings so serving never rewrites
// unknown fields or whitespace. Empty documents disable their endpoints.
type Codex struct {
	Whoami   string `json:"whoami"`
	Accounts string `json:"accounts"`
	Models   string `json:"models"`
}

const MaxCodexDocumentBytes = 512 << 10

func (c Codex) Validate() error {
	docs := []struct{ name, raw string }{{"whoami", c.Whoami}, {"accounts", c.Accounts}, {"models", c.Models}}
	for _, d := range docs {
		if d.raw == "" {
			continue
		}
		var object map[string]json.RawMessage
		if len(d.raw) > MaxCodexDocumentBytes || json.Unmarshal([]byte(d.raw), &object) != nil || object == nil {
			return fmt.Errorf("%w: %s 必须是 JSON 对象，且不超过 512 KiB", ErrInvalidInput, d.name)
		}
		if err := validateCodexFields(object); err != nil {
			return err
		}
		if d.name == "whoami" {
			for _, key := range []string{"chatgpt_user_id", "chatgpt_account_id", "chatgpt_plan_type"} {
				var value string
				if json.Unmarshal(object[key], &value) != nil || value == "" {
					return fmt.Errorf("%w: whoami.%s 缺失", ErrInvalidInput, key)
				}
			}
		}
		if d.name == "models" {
			var models []struct {
				Slug string `json:"slug"`
			}
			if json.Unmarshal(object["models"], &models) != nil || models == nil || len(models) > 200 {
				return fmt.Errorf("%w: models.models 必须是模型数组（最多 200 项）", ErrInvalidInput)
			}
			seen := map[string]bool{}
			for _, m := range models {
				if m.Slug == "" || seen[m.Slug] {
					return fmt.Errorf("%w: 模型 slug 缺失或重复", ErrInvalidInput)
				}
				seen[m.Slug] = true
			}
		}
	}
	if c.Whoami != "" && c.Accounts != "" {
		var w struct {
			ID string `json:"chatgpt_account_id"`
		}
		_ = json.Unmarshal([]byte(c.Whoami), &w)
		var a struct {
			Accounts map[string]json.RawMessage `json:"accounts"`
		}
		_ = json.Unmarshal([]byte(c.Accounts), &a)
		if len(a.Accounts) == 0 || a.Accounts[w.ID] == nil {
			return fmt.Errorf("%w: accounts.accounts 必须包含 whoami.chatgpt_account_id 对应账号", ErrInvalidInput)
		}
	}
	return nil
}
func validateCodexFields(object map[string]json.RawMessage) error {
	for key, raw := range object {
		name := strings.ToLower(key)
		switch name {
		case "access_token", "refresh_token", "personal_access_token", "openai_api_key", "authorization", "cookie", "api_key":
			return fmt.Errorf("%w: 固定响应不能包含凭据字段 %s", ErrInvalidInput, key)
		}
		if (name == "prefer_websockets" || name == "supports_websockets" || name == "supports_websocket" || name == "websocket_enabled") && string(raw) == "true" {
			return fmt.Errorf("%w: 本轮不支持 WebSocket，请关闭 %s", ErrInvalidInput, key)
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil {
			if err := validateCodexFields(nested); err != nil {
				return err
			}
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			for _, item := range items {
				if json.Unmarshal(item, &nested) == nil && nested != nil {
					if err := validateCodexFields(nested); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
