package resourcepg

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Lookup(ctx context.Context, ref gatewaycontract.Reference) ([]resourcestate.Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := New(s.pool).LookupResource(ctx, LookupResourceParams{Kind: ref.Kind, RemoteID: ref.ID, ParentFilter: ref.ParentID})
	if err != nil {
		return nil, err
	}
	out := make([]resourcestate.Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, resourcestate.Record{Resource: gatewaycontract.Resource{Kind: r.Kind, ID: r.RemoteID, ParentID: r.ParentID, AccountID: r.AccountID, KeyID: r.KeyID, Binding: gatewaycontract.Binding{GroupID: r.GroupID, ChannelID: r.ChannelID, CredentialVersion: r.CredentialVersion}, CredentialScope: r.CredentialScope, CreationID: r.CreationID}, ModelID: r.ModelID, Quarantined: r.Quarantined, Deleted: r.Deleted})
	}
	return out, nil
}
func identity(r resourcestate.Record) string {
	data, _ := json.Marshal([]string{r.Kind, r.ID, r.ParentID, r.Binding.GroupID, r.CredentialScope})
	sum := sha256.Sum256(data)
	return string(sum[:])
}
func (s *Store) Observe(ctx context.Context, records []resourcestate.Record) error {
	if len(records) == 0 {
		return nil
	}
	if len(records) > 128 {
		return gatewaycontract.ErrConflict
	}
	rows := append([]resourcestate.Record(nil), records...)
	for _, r := range rows {
		if !resourcestate.ValidRecord(r) {
			return gatewaycontract.ErrConflict
		}
	}
	sort.Slice(rows, func(i, j int) bool { return identity(rows[i]) < identity(rows[j]) })
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	q := New(tx)
	collision := false
	for _, r := range rows {
		bad, e := q.ObserveResource(ctx, ObserveResourceParams{Kind: r.Kind, RemoteID: r.ID, ParentID: r.ParentID, GroupID: r.Binding.GroupID, CredentialScope: r.CredentialScope, AccountID: r.AccountID, KeyID: r.KeyID, ChannelID: r.Binding.ChannelID, CredentialVersion: r.Binding.CredentialVersion, CreationID: r.CreationID, ModelID: r.ModelID, IdentityHash: []byte(identity(r))})
		if e != nil {
			return e
		}
		collision = collision || bad
	}
	// A collision is committed, including the quarantine marker. Rolling back on
	// this error would re-enable access to overwritten data for the previous owner.
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if collision {
		return resourcestate.ErrQuarantined
	}
	return nil
}
func (s *Store) Delete(ctx context.Context, actor gatewaycontract.Principal, ref gatewaycontract.Reference, b gatewaycontract.Binding) error {
	if !actor.Active {
		return gatewaycontract.ErrInactive
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return New(s.pool).DeleteOwnedResource(ctx, DeleteOwnedResourceParams{Kind: ref.Kind, RemoteID: ref.ID, ParentFilter: ref.ParentID, AccountID: actor.AccountID, KeyID: actor.KeyID, GroupID: b.GroupID, ChannelID: b.ChannelID, CredentialVersion: b.CredentialVersion})
}

var _ resourcestate.Repository = (*Store)(nil)
