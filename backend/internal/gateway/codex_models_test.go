package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestCodexCatalogFiltersScopeAliasesAndUnprovenMetadata(t *testing.T) {
	c := Candidate{GroupID: "group", GroupModelID: "group-main", ChannelID: "channel", CredentialScope: "scope", UpstreamModel: "native-main", Quotes: groupBook(t, "CNY", 1), Evidence: map[string]bool{}, VerifiedCapacity: map[string]int64{}}
	records := []CandidateRecord{{ModelID: "main", Formats: []channel.Format{channel.FormatOpenAIResponses}, Candidate: c}}
	input := []byte(`{"models":[{"slug":"native-main","display_name":"provider display","context_window":999999,"max_context_window":999999,"input_modalities":["text","image"],"service_tiers":[{"id":"fast","name":"Fast","description":"declared"},{"id":"ultrafast","name":"Ultra","description":"not observed"}],"additional_speed_tiers":["fast","ultrafast"],"default_service_tier":"ultrafast","use_responses_lite":true,"supports_search_tool":true,"supports_experimental_context":true,"guardian":{"permissions":false},"chatgpt_account_id":"foreign-account","model_messages":{"instructions_template":"opaque instructions"}},{"slug":"foreign-model"}],"accounts":["foreign-account"]}`)
	key := KeyAuth{Aliases: map[string]string{"shortcut": "main"}}
	decode := func(raw []byte) []map[string]any {
		t.Helper()
		var envelope struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Models
	}
	out, err := filterCodexCatalog(input, records, c, key)
	if err != nil {
		t.Fatal(err)
	}
	models := decode(out)
	if len(models) != 2 || models[0]["slug"] != "main" || models[1]["slug"] != "shortcut" || strings.Contains(string(out), "foreign") || strings.Contains(string(out), "guardian") {
		t.Fatal("catalog exposed another scope or lost alias", string(out))
	}
	for _, m := range models {
		if _, present := m["context_window"]; present {
			t.Fatal("declaration became measured context")
		}
		if len(m["service_tiers"].([]any)) != 0 || len(m["input_modalities"].([]any)) != 1 || m["use_responses_lite"] != false || m["supports_experimental_context"] != false {
			t.Fatal("unproven metadata advertised", m)
		}
	}
	c.Evidence["upstream_protocol/service_tier/openai:fast"] = true
	c.Evidence["upstream_protocol/openai-responses.vision/inline_content_recognition"] = true
	c.VerifiedCapacity["openai-responses.context@upstream_protocol/context"] = 1024
	out, err = filterCodexCatalog(input, records, c, key)
	if err != nil {
		t.Fatal(err)
	}
	models = decode(out)
	if models[0]["context_window"] != float64(1024) || len(models[0]["service_tiers"].([]any)) != 1 || len(models[0]["input_modalities"].([]any)) != 2 || strings.Contains(string(out), `"id":"ultrafast"`) {
		t.Fatal("verified metadata or real tier filtering failed", string(out))
	}
	key.AllowedModels = []string{"another-model"}
	out, err = filterCodexCatalog(input, records, c, key)
	if err != nil || len(decode(out)) != 0 {
		t.Fatal("Key model permissions bypassed", err, string(out))
	}
	key.AllowedModels = nil
	key.Aliases["main"] = "unavailable-target"
	out, err = filterCodexCatalog(input, records, c, key)
	if err != nil || len(decode(out)) != 1 || decode(out)[0]["slug"] != "shortcut" {
		t.Fatal("canonical name overrode explicit Key alias", err, string(out))
	}
}
