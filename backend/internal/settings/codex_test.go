package settings

import (
	"strings"
	"testing"
)

func TestCodexDocumentsValidation(t *testing.T) {
	valid := Codex{Whoami: `{"chatgpt_account_id":"local","chatgpt_user_id":"user","chatgpt_plan_type":"pro"}`, Accounts: `{"accounts":{"local":{"account":{"account_id":"local"}}}}`, Models: `{"models":[{"slug":"m","prefer_websockets":false,"unknown":{"a":1}}]}`}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Codex{
		{Models: `{"models":[{"slug":"m","prefer_websockets":true}]}`},
		{Models: `{"models":[{"slug":"m"},{"slug":"m"}]}`},
		{Models: `{"models":[{"slug":"m","unknown":{"access_token":"secret"}}]}`},
		{Whoami: valid.Whoami, Accounts: `{"accounts":{"other":{}}}`},
		{Models: strings.Repeat("a", MaxCodexDocumentBytes+1)},
		{Whoami: `[]`},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted invalid configuration: %.100s", bad.Models)
		}
	}
}
