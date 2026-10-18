package gatewaycontract

import (
	"errors"
	"testing"
)

func TestOwnershipAndIndependentReferenceConflicts(t *testing.T) {
	actor := Principal{"account-a", "key-a", true}
	a := Binding{"group-a", "channel-a", "cred-v1"}
	b := Binding{"group-b", "channel-b", "cred-v2"}
	rows := []Resource{
		{"response", "same-id", "", "account-a", "key-a", a},
		{"response", "same-id", "", "account-b", "key-b", b},
		{"conversation", "conv-b", "", "account-a", "key-a", b},
		{"item", "item-a", "conv-a", "account-a", "key-a", a},
		{"codex_note", "session/path", "", "account-a", "key-a", Binding{}},
	}
	lookup := func(Reference) ([]Resource, error) { return rows, nil }
	got, err := ResolveReferences(actor, []Reference{{Kind: "response", ID: "same-id"}}, lookup)
	if err != nil || got == nil || *got != a {
		t.Fatalf("wrong collision owner: %v %v", got, err)
	}
	for _, tc := range []struct {
		name  string
		actor Principal
		refs  []Reference
		want  error
	}{
		{"other_key", Principal{"account-a", "other-key", true}, []Reference{{Kind: "response", ID: "same-id"}}, ErrNotFound},
		{"revoked", Principal{"account-a", "key-a", false}, []Reference{{Kind: "response", ID: "same-id"}}, ErrInactive},
		{"unknown", actor, []Reference{{Kind: "response", ID: "guess"}}, ErrNotFound},
		{"wrong_parent", actor, []Reference{{Kind: "item", ID: "item-a", ParentID: "conv-b"}}, ErrNotFound},
		{"two_origins", actor, []Reference{{Kind: "response", ID: "same-id"}, {Kind: "conversation", ID: "conv-b"}}, ErrConflict},
		{"unknown_file", actor, []Reference{{Kind: "file", ID: "same-id"}}, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveReferences(tc.actor, tc.refs, lookup); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	rows = append(rows, Resource{"response", "same-id", "", "account-a", "key-a", b})
	if _, err := ResolveReferences(actor, []Reference{{Kind: "response", ID: "same-id"}}, lookup); !errors.Is(err, ErrAmbiguous) {
		t.Fatal("ambiguous bare ID routed arbitrarily", err)
	}
}

func TestCodexOwnershipDoesNotConstrainChannel(t *testing.T) {
	actor := Principal{"a", "k", true}
	lookup := func(r Reference) ([]Resource, error) {
		return []Resource{{Kind: r.Kind, ID: r.ID, AccountID: "a", KeyID: "k"}}, nil
	}
	for _, kind := range []string{"codex_session", "codex_note", "codex_window", "codex_item"} {
		bound, err := ResolveReferences(actor, []Reference{{Kind: kind, ID: "opaque-owned-id"}}, lookup)
		if err != nil || bound != nil {
			t.Fatalf("%s acquired source binding: %v %v", kind, bound, err)
		}
	}
	bound, err := ResolveReferences(actor, nil, func(Reference) ([]Resource, error) {
		t.Fatal("self-contained content performed lookup")
		return nil, nil
	})
	if err != nil || bound != nil {
		t.Fatal(bound, err)
	}
}

func TestCursorAndWriteReplayBoundaries(t *testing.T) {
	actor := Principal{"a", "k", true}
	scope := CursorScope{AccountID: "a", KeyID: "k", Kind: "response", Binding: Binding{"g", "c", "v1"}, QueryDigest: "filters-v1"}
	if err := scope.Authorize(actor, scope); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"key", "credential", "filters", "parent"} {
		next := scope
		switch field {
		case "key":
			next.KeyID = "other"
		case "credential":
			next.Binding.CredentialVersion = "v2"
		case "filters":
			next.QueryDigest = "changed"
		case "parent":
			next.ParentID = "other"
		}
		if scope.Authorize(actor, next) == nil {
			t.Errorf("cursor scope changed %s", field)
		}
	}
	for _, tc := range []struct {
		policy                       string
		sent, rejected, output, want bool
	}{
		{"write", true, false, false, false}, {"write", false, false, false, true},
		{"write", true, true, false, true}, {"read", true, false, false, true},
		{"read", true, false, true, false}, {"inference", false, false, false, false}, {"", false, false, false, false},
	} {
		if got := MayReplay(Endpoint{Replay: tc.policy}, tc.sent, tc.rejected, tc.output); got != tc.want {
			t.Errorf("%+v got %t", tc, got)
		}
	}
}
