// Package resourcestate persists only trusted upstream resource identity and
// ownership metadata. It never stores message, image, file or opaque contents.
package resourcestate

import (
	"context"
	"errors"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

var ErrQuarantined = errors.New("upstream resource identifier quarantined")

// Record's credential version is a logical channel-key revision, not an AEAD
// key-ring version. Scope is the protected equality tag of the actual secret.
type Record struct {
	gatewaycontract.Resource
	ModelID string
	// These fields are transient context inferred across a validated request's
	// references. An explicitly supplied authorized model takes precedence.
	ContextModelID        string
	AmbiguousModelContext bool
	Quarantined           bool
	Deleted               bool
}

type Repository interface {
	// Lookup returns at most 17 rows. More than 16 is rejected, never truncated
	// into an apparently unambiguous match.
	Lookup(context.Context, gatewaycontract.Reference) ([]Record, error)
	Observe(context.Context, []Record) error
	Delete(context.Context, gatewaycontract.Principal, gatewaycontract.Reference, gatewaycontract.Binding) error
}

func ValidRecord(r Record) bool {
	switch r.Kind {
	case "response", "conversation", "item", "stored_chat", "cache", "audio", "container":
	default:
		return false
	}
	for _, v := range []string{r.ID, r.ParentID} {
		if len(v) > 2048 || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	return r.ID != "" && r.AccountID != "" && r.KeyID != "" && r.Binding.GroupID != "" && r.Binding.ChannelID != "" && r.Binding.CredentialVersion != "" && r.CredentialScope != "" && r.CreationID != "" && (r.ModelID == "" || gatewaycontract.ValidModelID(r.ModelID)) && (r.Kind != "item" || r.ParentID != "")
}

func Resolve(ctx context.Context, repo Repository, actor gatewaycontract.Principal, refs []gatewaycontract.Reference) (*Record, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if repo == nil {
		return nil, gatewaycontract.ErrNotFound
	}
	var chosen *Record
	contextModel := ""
	ambiguousModel := false
	lookup := func(ref gatewaycontract.Reference) ([]gatewaycontract.Resource, error) {
		rows, err := repo.Lookup(ctx, ref)
		if err != nil {
			return nil, err
		}
		if len(rows) > 16 {
			return nil, gatewaycontract.ErrAmbiguous
		}
		out := make([]gatewaycontract.Resource, 0, len(rows))
		for _, r := range rows {
			if r.Quarantined {
				// A collision in any collision domain must only hide matching owned
				// records, not unrelated resources at another upstream with the same ID.
				if r.AccountID == actor.AccountID && r.KeyID == actor.KeyID {
					return nil, ErrQuarantined
				}
				continue
			}
			if r.Deleted {
				continue
			}
			out = append(out, r.Resource)
			if r.AccountID == actor.AccountID && r.KeyID == actor.KeyID && r.Kind == ref.Kind && r.ID == ref.ID && (ref.ParentID == "" || ref.ParentID == r.ParentID) {
				if r.ModelID != "" {
					if contextModel != "" && contextModel != r.ModelID {
						ambiguousModel = true
					}
					contextModel = r.ModelID
				}
				copy := r
				chosen = &copy
			}
		}
		return out, nil
	}
	_, err := gatewaycontract.ResolveReferences(actor, refs, lookup)
	if err != nil {
		return nil, err
	}
	if chosen != nil {
		chosen.AmbiguousModelContext = ambiguousModel
		if !ambiguousModel {
			chosen.ContextModelID = contextModel
		}
	}
	return chosen, nil
}
