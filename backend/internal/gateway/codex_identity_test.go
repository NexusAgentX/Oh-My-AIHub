package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
)

type identityStore struct {
	Store
	key KeyAuth
}

func (s identityStore) LookupKey(context.Context, []byte) (KeyAuth, error) { return s.key, nil }

func TestCodexIdentityIsLocalAndRejectsRevokedKeys(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	for _, accounts := range []bool{false, true} {
		for _, key := range []KeyAuth{{OwnerID: "owner", KeyID: "key", Enabled: true, AccountActive: true}, {OwnerID: "owner", KeyID: "key", Enabled: false, AccountActive: true}, {OwnerID: "owner", KeyID: "key", Enabled: true, AccountActive: false}, {OwnerID: "owner", KeyID: "key", Enabled: true, AccountActive: true, ExpiresAt: &past}} {
			e := NewEngine(Dependencies{Store: identityStore{key: key}, Ready: func() error { panic("identity depended on market") }, Now: func() time.Time { return now }})
			r := httptest.NewRequest("GET", "/v1/user-auth-credential/whoami?account_id=foreign", nil)
			r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"synthetic")
			r.Header.Set("ChatGPT-Account-ID", "foreign")
			w := httptest.NewRecorder()
			e.ServeCodexIdentity(w, r, accounts)
			valid := key.Enabled && key.AccountActive && key.ExpiresAt == nil
			if valid {
				body := w.Body.String()
				if w.Code != 200 || !strings.Contains(body, "aihub-account-owner") || strings.Contains(body, "foreign") || strings.Contains(body, apikey.Prefix) || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("identity disclosed foreign identity or depended on upstream", w.Code, body)
				}
				if !accounts && (!strings.Contains(body, `"email":null`) || !strings.Contains(body, `"chatgpt_plan_type":"aihub"`)) {
					t.Fatal("identity claimed official subscription")
				}
			} else if w.Code != 401 {
				t.Fatal("inactive key accepted", w.Code)
			}
		}
	}
}
