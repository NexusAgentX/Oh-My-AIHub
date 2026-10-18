package gatewaycontract

import "errors"

var (
	ErrNotFound  = errors.New("resource not found")
	ErrConflict  = errors.New("conflicting resource scopes")
	ErrAmbiguous = errors.New("ambiguous upstream identifier")
	ErrInactive  = errors.New("account or platform key inactive")
)

type Principal struct {
	AccountID string
	KeyID     string
	Active    bool
}

// Binding identifies an engine and credential scope, never a particular CF/CDN
// address. Self-contained opaque contents and Codex context IDs have no Binding.
type Binding struct {
	GroupID           string
	ChannelID         string
	CredentialVersion string
}

type Resource struct {
	Kind      string
	ID        string
	ParentID  string
	AccountID string
	KeyID     string
	Binding   Binding
	// CredentialScope is a protected, stable equality tag for the real upstream
	// credential within the group. It must not be channel-local: two channels
	// sharing the same upstream Key share this collision domain. Never return it
	// to consumers. Codex context ownership has neither this tag nor Binding.
	CredentialScope string
	CreationID      string
}

type Reference struct {
	Kind     string
	ID       string
	ParentID string
}

// Lookup returns verified ownership metadata only. Implementations must bound
// collision buckets and use durable indexes before the formal F7/F8 rollout.
// A client-supplied session/path/ID is not an ownership record.
type Lookup func(Reference) ([]Resource, error)

func contextOnly(kind string) bool {
	switch kind {
	case "codex_session", "codex_note", "codex_window", "codex_item":
		return true
	default:
		return false
	}
}

func remoteResource(kind string) bool {
	switch kind {
	case "response", "conversation", "item", "stored_chat", "cache":
		return true
	default:
		return false
	}
}

// ResolveReferences checks current access and all independent references. A
// non-nil result constrains routing; Codex context ownership alone never does.
// Complete encrypted reasoning/signatures/compaction must not be passed here.
func ResolveReferences(actor Principal, refs []Reference, lookup Lookup) (*Binding, error) {
	if !actor.Active || actor.AccountID == "" || actor.KeyID == "" {
		return nil, ErrInactive
	}
	if len(refs) > 128 {
		return nil, ErrConflict
	}
	var selected *Binding
	for _, ref := range refs {
		if ref.ID == "" || (!remoteResource(ref.Kind) && !contextOnly(ref.Kind)) {
			return nil, ErrNotFound
		}
		rows, err := lookup(ref)
		if err != nil {
			return nil, err
		}
		if len(rows) > 16 {
			return nil, ErrAmbiguous
		}
		var match *Resource
		for i := range rows {
			r := &rows[i]
			if r.AccountID != actor.AccountID || r.KeyID != actor.KeyID || r.Kind != ref.Kind || r.ID != ref.ID || r.ParentID != ref.ParentID {
				continue
			}
			if match != nil {
				return nil, ErrAmbiguous
			}
			match = r
		}
		if match == nil {
			return nil, ErrNotFound
		}
		if contextOnly(ref.Kind) {
			if match.Binding != (Binding{}) || match.CredentialScope != "" {
				return nil, ErrConflict
			}
			for _, r := range rows {
				if r.Kind == ref.Kind && r.ID == ref.ID && r.ParentID == ref.ParentID && (r.AccountID != match.AccountID || r.KeyID != match.KeyID) {
					return nil, ErrAmbiguous
				}
			}
			continue
		}
		b := match.Binding
		if b.GroupID == "" || b.ChannelID == "" || b.CredentialVersion == "" || match.CredentialScope == "" || match.CreationID == "" || (selected != nil && *selected != b) {
			return nil, ErrConflict
		}
		for _, r := range rows {
			if sameRemoteID(*match, r) && r != *match {
				return nil, ErrAmbiguous
			}
		}
		selected = &b
	}
	return selected, nil
}

func sameRemoteID(a, b Resource) bool {
	return a.Kind == b.Kind && a.ID == b.ID && a.ParentID == b.ParentID && a.Binding.GroupID == b.Binding.GroupID && a.CredentialScope == b.CredentialScope
}

// CheckRemoteCreation validates an observation from a successful gateway create
// operation, never a client ownership claim or a shared-account list. The real
// index must atomically check + persist before exposing the ID; on collision it
// must retain a quarantine marker so the old owner cannot read overwritten data.
// Reobserving the exact same creation operation is idempotent.
func CheckRemoteCreation(actor Principal, observed Resource, collisions []Resource) error {
	if !actor.Active || actor.AccountID == "" || actor.KeyID == "" {
		return ErrInactive
	}
	if !remoteResource(observed.Kind) || observed.ID == "" || (observed.Kind == "item" && observed.ParentID == "") || observed.AccountID != actor.AccountID || observed.KeyID != actor.KeyID ||
		observed.Binding.GroupID == "" || observed.Binding.ChannelID == "" || observed.Binding.CredentialVersion == "" || observed.CredentialScope == "" || observed.CreationID == "" {
		return ErrConflict
	}
	if len(collisions) > 16 {
		return ErrAmbiguous
	}
	for _, prior := range collisions {
		if sameRemoteID(observed, prior) && prior != observed {
			return ErrAmbiguous
		}
	}
	return nil
}

// CursorScope binds a server-validated native cursor to its original ACL,
// filters and engine. The wire token must be authenticated by F7; accepting a
// caller-supplied struct is not a valid public cursor implementation.
type CursorScope struct {
	AccountID   string
	KeyID       string
	Kind        string
	ParentID    string
	Binding     Binding
	QueryDigest string
}

func (c CursorScope) Authorize(actor Principal, current CursorScope) error {
	if !actor.Active {
		return ErrInactive
	}
	if actor.AccountID == "" || actor.KeyID == "" || c.AccountID != actor.AccountID || c.KeyID != actor.KeyID || c != current {
		return ErrNotFound
	}
	return nil
}

// MayReplay is for auxiliary operations. An indeterminate resource write must
// not be repeated solely because it produced no client-visible output. Native
// idempotency is not assumed. Inference retains its existing runtime policy.
func MayReplay(endpoint Endpoint, sent, explicitlyNotApplied, outputStarted bool) bool {
	if outputStarted || (endpoint.Replay != "read" && endpoint.Replay != "write") {
		return false
	}
	return endpoint.Replay == "read" || !sent || explicitlyNotApplied
}
