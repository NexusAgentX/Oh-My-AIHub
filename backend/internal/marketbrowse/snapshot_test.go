package marketbrowse

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestApprovedSnapshotKeepsProtocolWitnessesAndPrivateFieldsSeparate(t *testing.T) {
	state := &groupchannel.State{Version: "v1", Directory: market.Directory{Suppliers: []market.Supplier{{ID: "supplier", Name: "Supplier", Enabled: true, ContributorID: "PRIVATE-owner"}}, Groups: []market.Group{{ID: "group", SupplierID: "supplier", Enabled: true, Defaults: market.Defaults{HeaderRules: &market.HeaderRules{Set: []market.HeaderSet{{Name: "Authorization", Value: "PRIVATE-header"}}}}}}, Models: []market.GroupModel{{ID: "offer", ModelID: "same/model", GroupID: "group", ModelType: "text", Enabled: true, UpstreamModel: "PRIVATE-upstream"}}}, Views: map[string]groupchannel.View{"channel": {Name: "Channel", Owner: groupchannel.OwnerView{ID: "PRIVATE-owner"}, Addresses: []groupchannel.AddressView{{URL: "https://PRIVATE-origin.example"}}}}}
	proof := map[string]bool{"upstream_protocol/openai-responses.base/accepted_native_message_shapes": true, "upstream_protocol/openai-responses.tools/native_declarations": true, "upstream_protocol/openai-responses.tools/call_result_association": true, "upstream_protocol/codex-special.RL14/uncompressed_request": true, "upstream_protocol/codex-special.RL01/actual_protocol_marker": true}
	state.Candidates = []gateway.CandidateRecord{{ModelID: "same/model", Formats: []channel.Format{channel.FormatOpenAIResponses}, Candidate: gateway.Candidate{ChannelID: "channel", GroupID: "group", GroupModelID: "offer", OwnerID: "PRIVATE-owner", BaseURL: "https://PRIVATE-origin.example", Evidence: proof, VerifiedCapacity: map[string]int64{"openai-responses.length_search@upstream_protocol/context": 8192, "native-messages.length_search@upstream_protocol/context": 999999}, KeyProbeEndpoints: map[channel.Format]string{channel.FormatOpenAIResponses: "codex.responses"}}}}
	s, err := Build(state, nil, 7_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	q := Query{OnlyOnline: true, Features: []string{"tools"}}
	page, err := Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, q, "", 20)
	if err != nil || page.Total != 1 || len(page.Items[0].Protocols) != 1 || page.Items[0].Protocols[0] != "openai-responses" {
		t.Fatal("borrowed public tool proof into Codex", page, err)
	}
	q.Protocol = "codex-special"
	page, err = Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, q, "", 20)
	if err != nil || page.Total != 0 {
		t.Fatal("Codex claimed public tool support", page, err)
	}
	q = Query{OnlyOnline: true, MinContext: 8193}
	page, _ = Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, q, "", 20)
	if page.Total != 0 {
		t.Fatal("borrowed capacity from another protocol", page)
	}
	for _, value := range []any{s.Suppliers, s.Groups, s.Models, s.Channels} {
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), "PRIVATE-") {
			t.Fatal("private source leaked", string(encoded))
		}
	}
	delete(proof, "upstream_protocol/openai-responses.base/accepted_native_message_shapes")
	s, err = Build(state, nil, 7_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, Query{OnlyOnline: true, Protocol: "openai-responses"}, "", 20)
	if page.Total != 0 {
		t.Fatal("native-only Key probe became public endpoint", page)
	}
}

func TestDeclaredBoundsAndOtherProofLayersDoNotBecomeVerifiedFacts(t *testing.T) {
	p := "native-messages"
	f := EvidenceFacts(p, map[string]bool{"gateway_proxy/" + p + ".tools/native_declarations": true, "official_cli/" + p + ".tools/call_result_association": true}, map[string]int64{p + ".length_search@official_cli/context": 999999, p + ".length_search@upstream_protocol/max_output": 1024})
	if f.Supported["tools"] || f.ContextLower != 0 || f.OutputLower != 1024 {
		t.Fatal("declarations or unrelated proof promoted", f)
	}
}
func TestNativeImageModelsRetainGenerateContentAndResponsesOfferings(t *testing.T) {
	state := &groupchannel.State{Version: "image-v1", Directory: market.Directory{Suppliers: []market.Supplier{{ID: "s", Enabled: true}}, Groups: []market.Group{{ID: "g", SupplierID: "s", Enabled: true}}, Models: []market.GroupModel{{ID: "o", ModelID: "image/model", GroupID: "g", Enabled: true, ModelType: "image"}}}, Views: map[string]groupchannel.View{"c": {Name: "Image provider"}}, Candidates: []gateway.CandidateRecord{{ModelID: "image/model", Formats: []channel.Format{channel.FormatGemini, channel.FormatOpenAIResponses}, Candidate: gateway.Candidate{ChannelID: "c", GroupID: "g", GroupModelID: "o", Evidence: map[string]bool{"upstream_protocol/native-generate-content.image_tool/native_image_output": true, "upstream_protocol/openai-responses.image_tool/native_image_output": true}}}}}
	s, err := Build(state, nil, 7_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"native-generate-content", "openai-responses"} {
		p, err := Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, Query{Protocol: protocol, OnlyOnline: true, ModelType: "image", Features: []string{"output:image"}}, "", 10)
		if err != nil || p.Total != 1 {
			t.Fatal("native image offering hidden", protocol, p, err)
		}
	}
}
