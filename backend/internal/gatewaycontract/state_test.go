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
		{"response", "same-id", "", "account-a", "key-a", a, "upstream-a", "creation-a"},
		{"response", "same-id", "", "account-b", "key-b", b, "upstream-b", "creation-b"},
		{"conversation", "conv-b", "", "account-a", "key-a", b, "upstream-b", "creation-c"},
		{"item", "item-a", "conv-a", "account-a", "key-a", a, "upstream-a", "creation-d"},
		{"codex_note", "session/path", "", "account-a", "key-a", Binding{}, "", ""},
	}
	lookup := func(Reference) ([]Resource, error) { return rows, nil }
	got, err := ResolveReferences(actor, []Reference{{Kind: "response", ID: "same-id"}}, lookup)
	if err != nil || got == nil || *got != a {
		t.Fatalf("wrong collision owner: %v %v", got, err)
	}
	if got, err := ResolveReferences(actor, []Reference{{Kind: "item", ID: "item-a"}}, lookup); err != nil || got == nil || *got != a {
		t.Fatal("bare owned item_reference lost its recorded parent", got, err)
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
	rows = append(rows, Resource{"response", "same-id", "", "account-a", "key-a", b, "upstream-b", "creation-e"})
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

func TestCreationCollisionWithSharedCredentialQuarantinesBothOwners(t *testing.T) {
	a, b := Principal{"a", "key-a", true}, Principal{"b", "key-b", true}
	first := Resource{Kind: "conversation", ID: "upstream-reused-id", AccountID: "a", KeyID: "key-a", Binding: Binding{"group", "channel-a", "version-1"}, CredentialScope: "same-actual-upstream-key", CreationID: "operation-a"}
	if err := CheckRemoteCreation(a, first, nil); err != nil {
		t.Fatal(err)
	}
	rows := []Resource{first}
	if err := CheckRemoteCreation(a, first, rows); err != nil {
		t.Fatal("same successful operation could not be reobserved", err)
	}
	second := first
	second.AccountID, second.KeyID, second.CreationID = "b", "key-b", "operation-b"
	second.Binding.ChannelID, second.Binding.CredentialVersion = "channel-b", "version-2"
	if err := CheckRemoteCreation(b, second, rows); !errors.Is(err, ErrAmbiguous) {
		t.Fatal("same real credential escaped collision checks through another channel", err)
	}
	// A real transactional index persists a quarantine marker on this failure.
	// Keeping both synthetic observations demonstrates why preserving only the
	// first owner is insufficient: the second creation may have replaced data.
	rows = append(rows, second)
	for _, actor := range []Principal{a, b} {
		if _, err := ResolveReferences(actor, []Reference{{Kind: "conversation", ID: first.ID}}, func(Reference) ([]Resource, error) { return rows, nil }); !errors.Is(err, ErrAmbiguous) {
			t.Fatal("collided remote ID was still readable", err)
		}
	}
	for _, change := range []string{"no_operation", "no_scope", "unowned", "wrong_kind", "no_parent"} {
		invalid := first
		switch change {
		case "no_operation":
			invalid.CreationID = ""
		case "no_scope":
			invalid.CredentialScope = ""
		case "unowned":
			invalid.KeyID = "other"
		case "wrong_kind":
			invalid.Kind = "file"
		case "no_parent":
			invalid.Kind = "item"
		}
		if CheckRemoteCreation(a, invalid, nil) == nil {
			t.Errorf("%s created an ownership record", change)
		}
	}
}

func TestContextCollisionCannotBecomeOwnerOrSourceBinding(t *testing.T) {
	rows := []Resource{
		{Kind: "codex_note", ID: "session/notes/path", AccountID: "a", KeyID: "ka"},
		{Kind: "codex_note", ID: "session/notes/path", AccountID: "b", KeyID: "kb"},
	}
	for _, actor := range []Principal{{"a", "ka", true}, {"b", "kb", true}} {
		if _, err := ResolveReferences(actor, []Reference{{Kind: "codex_note", ID: rows[0].ID}}, func(Reference) ([]Resource, error) { return rows, nil }); !errors.Is(err, ErrAmbiguous) {
			t.Fatal("duplicate context ID trusted a client's ownership claim", err)
		}
	}
}
