package gatewaycontract

import (
	"errors"
	"testing"
)

func TestHardCapabilitiesMustMeetOnOneCandidate(t *testing.T) {
	valid := Evidence{Supported: true, Valid: true}
	all := []Candidate{
		{Binding: Binding{"g", "b", "v"}, ModelID: "model", Authorized: true, Callable: true, Enabled: true, Capabilities: map[string]Evidence{"vision": valid}},
		{Binding: Binding{"g", "a", "v"}, ModelID: "model", Authorized: true, Callable: true, Enabled: true, Capabilities: map[string]Evidence{"tools": valid}},
	}
	_, err := Candidates(Selection{ModelID: "model", Required: []string{"vision", "tools"}}, all)
	var no *NoCandidatesError
	if !errors.As(err, &no) || len(no.MissingEvidence) != 2 {
		t.Fatalf("capability union admitted: %v", err)
	}
	all[1].Capabilities["vision"] = valid
	got, err := Candidates(Selection{ModelID: "model", Required: []string{"vision", "tools"}}, all)
	if err != nil || len(got) != 1 || got[0].ChannelID != "a" {
		t.Fatal(got, err)
	}
	all[1].Capabilities["vision"] = Evidence{Supported: true, Valid: false}
	if _, err := Candidates(Selection{ModelID: "model", Required: []string{"vision", "tools"}}, all); err == nil {
		t.Fatal("invalidated evidence admitted")
	}
	if _, err := Candidates(Selection{ModelID: "model"}, all); err != nil {
		t.Fatal("unrelated unknown stopped model", err)
	}
}

func TestModellessOrderAndResourceBinding(t *testing.T) {
	all := []Candidate{
		{Binding: Binding{"g", "b", "v1"}, Authorized: true, Callable: true, Enabled: true},
		{Binding: Binding{"g", "a", "v2"}, Authorized: true, Callable: true, Enabled: true},
	}
	got, err := Candidates(Selection{}, all)
	if err != nil || got[0].ChannelID != "a" {
		t.Fatal(got, err)
	}
	got, err = Candidates(Selection{Manual: true, Order: []string{"b"}}, all)
	if err != nil || len(got) != 1 || got[0].ChannelID != "b" {
		t.Fatal(got, err)
	}
	if _, err := Candidates(Selection{Manual: true}, all); err == nil {
		t.Fatal("manual silently included new channels")
	}
	got, err = Candidates(Selection{Bound: &all[0].Binding}, all)
	if err != nil || len(got) != 1 || got[0].ChannelID != "b" {
		t.Fatal(got, err)
	}
	all[0].CredentialVersion = "v3"
	if _, err := Candidates(Selection{Bound: &Binding{"g", "b", "v1"}}, all); err == nil {
		t.Fatal("resource moved to rotated credential")
	}
}
