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
	state.Candidates = []gateway.CandidateRecord{{ModelID: "same/model", Formats: []channel.Format{channel.FormatOpenAIResponses}, Candidate: gateway.Candidate{ChannelID: "channel", GroupID: "group", GroupModelID: "offer", OwnerID: "PRIVATE-owner", BaseURL: "https://PRIVATE-origin.example"}}}
	s, err := Build(state, nil, 7_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	page, err := Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, Query{OnlyOnline: true}, "", 20)
	if err != nil || page.Total != 1 || len(page.Items[0].Protocols) != 1 || page.Items[0].Protocols[0] != "openai-responses" {
		t.Fatal("the probe's protocol did not become the public protocol", page, err)
	}
	channel := s.Channels[ChannelKey("channel", "offer", "openai-responses")]
	if channel.Protocol != "openai-responses" {
		t.Fatal("channel projection lost the protocol", channel)
	}
	for _, value := range []any{s.Suppliers, s.Groups, s.Models, s.Channels} {
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), "PRIVATE-") {
			t.Fatal("private source leaked", string(encoded))
		}
	}
}

func TestNativeImageModelsRetainGenerateContentAndResponsesOfferings(t *testing.T) {
	state := &groupchannel.State{Version: "image-v1", Directory: market.Directory{Suppliers: []market.Supplier{{ID: "s", Enabled: true}}, Groups: []market.Group{{ID: "g", SupplierID: "s", Enabled: true}}, Models: []market.GroupModel{{ID: "o", ModelID: "image/model", GroupID: "g", Enabled: true, ModelType: "image"}}}, Views: map[string]groupchannel.View{"c": {Name: "Image provider"}}, Candidates: []gateway.CandidateRecord{{ModelID: "image/model", Formats: []channel.Format{channel.FormatGemini, channel.FormatOpenAIResponses}, Candidate: gateway.Candidate{ChannelID: "c", GroupID: "g", GroupModelID: "o"}}}}
	s, err := Build(state, nil, 7_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"native-generate-content", "openai-responses"} {
		p, err := Select(s.Offers, Scope{Version: s.Version, ActorID: "consumer"}, Query{Protocol: protocol, OnlyOnline: true, ModelType: "image"}, "", 10)
		if err != nil || p.Total != 1 {
			t.Fatal("native image offering hidden", protocol, p, err)
		}
	}
}
