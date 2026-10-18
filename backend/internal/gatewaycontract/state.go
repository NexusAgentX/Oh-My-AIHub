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
			if match.Binding != (Binding{}) {
				return nil, ErrConflict
			}
			continue
		}
		b := match.Binding
		if b.GroupID == "" || b.ChannelID == "" || b.CredentialVersion == "" || (selected != nil && *selected != b) {
			return nil, ErrConflict
		}
		selected = &b
	}
	return selected, nil
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
