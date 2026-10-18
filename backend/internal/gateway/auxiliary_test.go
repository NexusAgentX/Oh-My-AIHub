package gateway

import (
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

func TestAuxiliaryUsesContextAllCapabilitiesAndExplicitFallback(t *testing.T) {
	records := []CandidateRecord{}
	for _, id := range []string{"b", "a"} {
		records = append(records, CandidateRecord{ModelID: "model", Formats: []channel.Format{channel.FormatOpenAIResponses}, Candidate: Candidate{ChannelID: id, GroupID: "g", CredentialScope: "credential", Quotes: &quotepricing.Book{}, Evidence: map[string]bool{"upstream_protocol/history": true}}})
	}
	in := AuxiliarySelection{Format: channel.FormatOpenAIResponses, Preference: routing.Default(""), Required: []CapabilityRequirement{{Layer: "upstream_protocol", ID: "history"}}}
	out, err := SelectAuxiliary(records, in)
	if err != nil || len(out) != 2 || out[0].Candidate.ChannelID != "a" {
		t.Fatal("model-less fallback is not stable", out, err)
	}
	in.ModelID = "other"
	if _, err = SelectAuxiliary(records, in); !errors.Is(err, ErrCapabilityCandidates) {
		t.Fatal("explicit context ignored")
	}
	in.ModelID = "model"
	in.AllowedModels = []string{"other"}
	if _, err = SelectAuxiliary(records, in); !errors.Is(err, ErrCapabilityCandidates) {
		t.Fatal("Key model ACL ignored")
	}
	in.AllowedModels = nil
	in.Required = append(in.Required, CapabilityRequirement{Layer: "upstream_protocol", ID: "notes"})
	if _, err = SelectAuxiliary(records, in); !errors.Is(err, ErrCapabilityCandidates) {
		t.Fatal("unknown capability used fallback")
	}
	records[0].Candidate.Evidence["upstream_protocol/notes"] = true
	out, err = SelectAuxiliary(records, in)
	if err != nil || len(out) != 1 || out[0].Candidate.ChannelID != "b" {
		t.Fatal("capabilities combined across candidates")
	}
	in.Preference.Mode = routing.ModeManual
	if _, err = SelectAuxiliary(records, in); !errors.Is(err, ErrManualOrderRequired) {
		t.Fatal("invented manual order")
	}
	in.Preference.Order = []string{"b", "a"}
	out, err = SelectAuxiliary(records, in)
	if err != nil || out[0].Candidate.ChannelID != "b" {
		t.Fatal("manual order lost")
	}
	in.Reference = &ResourceScope{GroupID: "g", ChannelID: "b", CredentialScope: "rotated"}
	if _, err = SelectAuxiliary(records, in); !errors.Is(err, ErrCapabilityCandidates) {
		t.Fatal("resource crossed credential scope")
	}
	in.Reference.CredentialScope = "credential"
	if _, err = SelectAuxiliary(records, in); err != nil {
		t.Fatal(err)
	}
}
