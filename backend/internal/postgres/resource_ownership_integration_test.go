package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/resourcepg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
)

func TestResourceOwnershipCollisionQuarantinesOldOwnerAndSurvivesStoreRestart(t *testing.T) {
	pool, _ := isolatedDatabase(t)
	ctx := context.Background()
	repo := resourcepg.NewStore(pool)
	owner, key, group, ch := marketID(t), marketID(t), marketID(t), marketID(t)
	actor := gatewaycontract.Principal{AccountID: owner, KeyID: key, Active: true}
	row := resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "response", ID: "resp-native-collision", AccountID: owner, KeyID: key, Binding: gatewaycontract.Binding{GroupID: group, ChannelID: ch, CredentialVersion: "1"}, CredentialScope: "same-actual-upstream-credential", CreationID: "synthetic-operation-a"}, ModelID: "vendor/model:2026"}
	if err := repo.Observe(ctx, []resourcestate.Record{row}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Observe(ctx, []resourcestate.Record{row}); err != nil {
		t.Fatal("same producer observation is not idempotent", err)
	}
	ref := gatewaycontract.Reference{Kind: row.Kind, ID: row.ID}
	if got, err := resourcestate.Resolve(ctx, repo, actor, []gatewaycontract.Reference{ref}); err != nil || got == nil || got.ContextModelID != row.ModelID {
		t.Fatal("owner lost resource", got, err)
	}
	foreign := actor
	foreign.KeyID = marketID(t)
	if _, err := resourcestate.Resolve(ctx, repo, foreign, []gatewaycontract.Reference{ref}); !errors.Is(err, gatewaycontract.ErrNotFound) {
		t.Fatal("other platform Key gained ownership", err)
	}
	// Same account and upstream Key on another channel is still one upstream
	// collision domain. It cannot bypass the quarantine by changing channel ID.
	reused := row
	reused.Binding.ChannelID = marketID(t)
	reused.KeyID = foreign.KeyID
	reused.CreationID = "synthetic-operation-b"
	if err := repo.Observe(ctx, []resourcestate.Record{reused}); !errors.Is(err, resourcestate.ErrQuarantined) {
		t.Fatal("native ID collision accepted", err)
	}
	repo = resourcepg.NewStore(pool)
	if _, err := resourcestate.Resolve(ctx, repo, actor, []gatewaycontract.Reference{ref}); !errors.Is(err, resourcestate.ErrQuarantined) {
		t.Fatal("old owner regained overwritten object after restart", err)
	}
	if _, err := resourcestate.Resolve(ctx, repo, foreign, []gatewaycontract.Reference{ref}); !errors.Is(err, gatewaycontract.ErrNotFound) {
		t.Fatal("new collision owner gained object", err)
	}
	// An unrelated upstream can use the same native ID without being replaced or
	// quarantined. Its owned record remains independently addressable by its Key.
	other := reused
	other.Binding.GroupID = marketID(t)
	other.CredentialScope = "different-upstream-credential"
	if err := repo.Observe(ctx, []resourcestate.Record{other}); err != nil {
		t.Fatal(err)
	}
	if got, err := resourcestate.Resolve(ctx, repo, foreign, []gatewaycontract.Reference{ref}); err != nil || got == nil || got.Binding.GroupID != other.Binding.GroupID {
		t.Fatal("unrelated upstream ID incorrectly quarantined", got, err)
	}
}
func TestResourceChildrenRequireLiveOwnedParentAndLongIDsStayBounded(t *testing.T) {
	pool, _ := isolatedDatabase(t)
	ctx := context.Background()
	repo := resourcepg.NewStore(pool)
	actor := gatewaycontract.Principal{AccountID: marketID(t), KeyID: marketID(t), Active: true}
	parent := resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "conversation", ID: strings.Repeat("p", 1800), AccountID: actor.AccountID, KeyID: actor.KeyID, Binding: gatewaycontract.Binding{GroupID: marketID(t), ChannelID: marketID(t), CredentialVersion: "1"}, CredentialScope: "synthetic-scope", CreationID: "synthetic-parent"}}
	child := parent
	child.Kind = "item"
	child.ID = strings.Repeat("i", 1800)
	child.ParentID = parent.ID
	child.CreationID = "synthetic-item"
	if err := repo.Observe(ctx, []resourcestate.Record{parent, child}); err != nil {
		t.Fatal("bounded opaque ID exceeds PostgreSQL index entry", err)
	}
	ref := gatewaycontract.Reference{Kind: "item", ID: child.ID, ParentID: parent.ID}
	if _, err := resourcestate.Resolve(ctx, repo, actor, []gatewaycontract.Reference{ref}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, actor, gatewaycontract.Reference{Kind: parent.Kind, ID: parent.ID}, parent.Binding); err != nil {
		t.Fatal(err)
	}
	if _, err := resourcestate.Resolve(ctx, repo, actor, []gatewaycontract.Reference{ref}); !errors.Is(err, gatewaycontract.ErrNotFound) {
		t.Fatal("detached child revived deleted conversation", err)
	}
}
