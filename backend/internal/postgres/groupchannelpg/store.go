package groupchannelpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A separate two-connection pool keeps configuration/probe work outside the
// forwarding and settlement pool. No method is called by an inference request.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool} }

var _ groupchannel.Repository = (*Store)(nil)

func domainError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return groupchannel.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505", "23503", "40001", "40P01":
			return groupchannel.ErrConflict
		case "23514", "22P02", "23502":
			return groupchannel.ErrInvalid
		}
	}
	return err
}
func record(r ListChannelsRow) (groupchannel.Record, error) {
	if r.MarketGroupID == nil {
		return groupchannel.Record{}, groupchannel.ErrInvalid
	}
	v := groupchannel.Record{ID: r.ID, Owner: channel.Owner{ID: r.OwnerID, Username: r.Username, DisplayName: r.DisplayName}, OwnerActive: r.OwnerStatus == "active", Name: r.Name, Summary: r.Summary, Description: r.Description, Status: channel.Status(r.Status), SuspendedReason: r.SuspendedReason, GroupID: *r.MarketGroupID, Revision: r.Revision, CredentialRevision: r.CredentialRevision, ProbeEpoch: r.ProbeEpoch, AddressIDs: append([]string{}, r.AddressIds...), Credential: channel.EncryptedCredential{Version: 1, Ciphertext: r.ApiKeyCiphertext, Nonce: r.ApiKeyNonce, KeyID: r.ApiKeyKeyID}, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if err := market.Decode(r.Overrides, &v.Overrides); err != nil {
		return v, err
	}
	return v, nil
}
func (s *Store) Get(ctx context.Context, id string) (out groupchannel.Record, err error) {
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		r, e := New(tx).LockChannel(ctx, id)
		if e != nil {
			return e
		}
		out, e = record(ListChannelsRow(r))
		return e
	})
	return out, domainError(err)
}
func list(ctx context.Context, q *Queries) ([]groupchannel.Record, error) {
	rows, err := q.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]groupchannel.Record, 0, len(rows))
	for _, r := range rows {
		v, e := record(r)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) List(ctx context.Context, owner string, admin bool) ([]groupchannel.Record, error) {
	rows, err := list(ctx, New(s.pool))
	if err != nil {
		return nil, domainError(err)
	}
	out := []groupchannel.Record{}
	for _, r := range rows {
		if admin || r.Owner.ID == owner {
			out = append(out, r)
		}
	}
	return out, nil
}
func putChoices(ctx context.Context, q *Queries, id string, choices []groupchannel.Choice, clear bool) error {
	if clear {
		if err := q.DeleteChoices(ctx, id); err != nil {
			return err
		}
	}
	for _, c := range choices {
		if err := q.PutChoice(ctx, PutChoiceParams{ChannelID: id, GroupModelID: c.GroupModelID, Excluded: c.Excluded, DiscountNano: c.DiscountNano}); err != nil {
			return err
		}
	}
	return nil
}
func audit(ctx context.Context, tx pgx.Tx, actor, id, action string, revision int64) error {
	return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "group_channel." + action, TargetType: "channel", TargetID: id, Detail: map[string]any{"revision": revision}})
}
func fence(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(4704367245194902356)")
	return err
}
func (s *Store) Create(ctx context.Context, r groupchannel.Record, choices []groupchannel.Choice) error {
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		q := New(tx)
		g, err := q.LockGroup(ctx, r.GroupID)
		if err != nil {
			return err
		}
		if !g.Enabled || g.DeletedAt != nil {
			return groupchannel.ErrConflict
		}
		raw, err := json.Marshal(r.Overrides)
		if err != nil {
			return err
		}
		err = q.InsertChannel(ctx, InsertChannelParams{ID: r.ID, OwnerID: r.Owner.ID, Name: r.Name, Summary: r.Summary, Description: r.Description, ApiKeyCiphertext: r.Credential.Ciphertext, ApiKeyNonce: r.Credential.Nonce, ApiKeyKeyID: r.Credential.KeyID, Status: string(r.Status), MarketGroupID: &r.GroupID})
		if err != nil {
			return err
		}
		if err = q.InsertConfiguration(ctx, InsertConfigurationParams{ChannelID: r.ID, AddressIds: r.AddressIDs, Overrides: raw}); err != nil {
			return err
		}
		if err = putChoices(ctx, q, r.ID, choices, true); err != nil {
			return err
		}
		if err = q.InsertEvent(ctx, InsertEventParams{ChannelID: r.ID, Kind: channel.EventListed, Reason: ""}); err != nil {
			return err
		}
		return audit(ctx, tx, r.Owner.ID, r.ID, "created", 1)
	}))
}
func (s *Store) Update(ctx context.Context, m groupchannel.Mutation) error {
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		q := New(tx)
		old, err := q.LockChannel(ctx, m.Record.ID)
		if err != nil {
			return err
		}
		if old.Revision != m.ExpectedRevision || old.OwnerID != m.Record.Owner.ID {
			return groupchannel.ErrConflict
		}
		r := m.Record
		if old.Status == "suspended" && r.Status != channel.StatusSuspended {
			return groupchannel.ErrSuspended
		}
		g, err := q.LockGroup(ctx, r.GroupID)
		if err != nil {
			return err
		}
		if !g.Enabled || g.DeletedAt != nil {
			return groupchannel.ErrConflict
		}
		raw, err := json.Marshal(r.Overrides)
		if err != nil {
			return err
		}
		n, err := q.UpdateConfiguration(ctx, UpdateConfigurationParams{ChannelID: r.ID, CredentialRevision: r.CredentialRevision, ProbeEpoch: r.ProbeEpoch, AddressIds: r.AddressIDs, Overrides: raw, ExpectedRevision: m.ExpectedRevision})
		if err != nil {
			return err
		}
		if n != 1 {
			return groupchannel.ErrConflict
		}
		_, err = q.UpdateChannel(ctx, UpdateChannelParams{ID: r.ID, Name: r.Name, Summary: r.Summary, Description: r.Description, Status: string(r.Status), MarketGroupID: &r.GroupID, ApiKeyCiphertext: r.Credential.Ciphertext, ApiKeyNonce: r.Credential.Nonce, ApiKeyKeyID: r.Credential.KeyID})
		if err != nil {
			return err
		}
		if m.ReplaceChoices {
			if err = putChoices(ctx, q, r.ID, m.Choices, m.ClearChoices); err != nil {
				return err
			}
		}
		if old.Status != string(r.Status) {
			kind := channel.EventUnlisted
			if r.Status == channel.StatusListed {
				kind = channel.EventListed
			}
			if err = q.InsertEvent(ctx, InsertEventParams{ChannelID: r.ID, Kind: kind, Reason: ""}); err != nil {
				return err
			}
		}
		return audit(ctx, tx, r.Owner.ID, r.ID, "updated", m.ExpectedRevision+1)
	}))
}
func (s *Store) Delete(ctx context.Context, id string, revision int64) error {
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		r, err := q.LockChannel(ctx, id)
		if err != nil {
			return err
		}
		if r.Revision != revision {
			return groupchannel.ErrConflict
		}
		if _, err = q.DeleteChannel(ctx, id); err != nil {
			return err
		}
		return audit(ctx, tx, r.OwnerID, id, "deleted", revision)
	}))
}
func (s *Store) Moderate(ctx context.Context, actor, id, reason string) error {
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		r, err := q.LockChannel(ctx, id)
		if err != nil {
			return err
		}
		status := "suspended"
		var why *string
		if reason == "" {
			status = "listed"
		} else {
			why = &reason
		}
		if _, err = q.ModerateChannel(ctx, ModerateChannelParams{ID: id, Status: status, Reason: why}); err != nil {
			return err
		}
		kind := channel.EventSuspended
		if status == "listed" {
			kind = channel.EventUnsuspended
		}
		if err = q.InsertEvent(ctx, InsertEventParams{ChannelID: id, Kind: kind, Reason: reason}); err != nil {
			return err
		}
		return audit(ctx, tx, actor, id, "moderated", r.Revision)
	}))
}
func (s *Store) Snapshot(ctx context.Context) (out groupchannel.Data, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := New(tx)
	if out.Channels, err = list(ctx, q); err != nil {
		return out, err
	}
	choices, err := q.ListChoices(ctx)
	if err != nil {
		return out, err
	}
	for _, c := range choices {
		out.Choices = append(out.Choices, groupchannel.Choice{ChannelID: c.ChannelID, GroupModelID: c.GroupModelID, Excluded: c.Excluded, DiscountNano: c.DiscountNano})
	}
	probes, err := q.ListProbes(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range probes {
		v := groupchannel.Probe{ChannelID: p.ChannelID, GroupModelID: p.GroupModelID, Binding: p.Binding, TestedAt: p.TestedAt}
		if err = market.Decode(p.Tests, &v.Tests); err != nil {
			return out, err
		}
		out.Probes = append(out.Probes, v)
	}
	return out, tx.Commit(ctx)
}
func (s *Store) SaveProbe(ctx context.Context, old groupchannel.Record, g market.Group, m market.GroupModel, p groupchannel.Probe) (saved bool, err error) {
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		q := New(tx)
		row, err := q.LockChannel(ctx, old.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		current, err := record(ListChannelsRow(row))
		if err != nil {
			return err
		}
		if current.GroupID != g.ID || groupchannel.ProbeBinding(current, g, m) != p.Binding || groupchannel.ProbeBinding(old, g, m) != p.Binding {
			return nil
		}
		group, err := q.LockGroup(ctx, g.ID)
		if err != nil {
			return err
		}
		model, err := q.LockModel(ctx, m.ID)
		if err != nil {
			return err
		}
		// The probe's inputs are the Key, the group and the model row: only those
		// can have moved since it ran. Group UA and header rules may have
		// changed, which no longer invalidates the result.
		if group.DeletedAt != nil || !group.Enabled || model.DeletedAt != nil || !model.Enabled || model.GroupID != g.ID || model.CapabilityDigest != m.CapabilityDigest {
			return nil
		}
		raw, err := json.Marshal(p.Tests)
		if err != nil {
			return err
		}
		err = q.SaveProbe(ctx, SaveProbeParams{ChannelID: old.ID, GroupModelID: m.ID, Binding: p.Binding, Tests: raw, TestedAt: p.TestedAt})
		saved = err == nil
		return err
	})
	return saved, domainError(err)
}

func (s *Store) Events(ctx context.Context, id string) ([]channel.Event, error) {
	rows, err := New(s.pool).ListEvents(ctx, id)
	if err != nil {
		return nil, domainError(err)
	}
	out := make([]channel.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, channel.Event{ID: r.ID, Kind: r.Kind, Reason: r.Reason, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
