package gatewaypg

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/jackc/pgx/v5"
)

func resource(row GatewayResource) gateway.Resource {
	return gateway.Resource{AccountID: row.AccountID, KeyID: row.KeyID, Kind: row.Kind, ID: row.ResourceID, CallID: row.CallID, ChannelID: row.ChannelID, ModelID: row.ModelID, Metadata: row.Metadata, CreatedAt: row.CreatedAt}
}
func (s *Store) SaveResource(ctx context.Context, row gateway.Resource) error {
	n, err := s.q.SaveResource(ctx, SaveResourceParams{AccountID: row.AccountID, KeyID: row.KeyID, Kind: row.Kind, ResourceID: row.ID, CallID: row.CallID, ChannelID: row.ChannelID, ModelID: row.ModelID, Metadata: row.Metadata})
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	existing, err := s.LookupResource(ctx, row.AccountID, row.KeyID, row.Kind, row.ID)
	if err != nil {
		return err
	}
	if existing.CallID != row.CallID || existing.ChannelID != row.ChannelID {
		return errors.New("upstream resource id collision")
	}
	return nil
}
func (s *Store) LookupResource(ctx context.Context, account, key, kind, id string) (gateway.Resource, error) {
	row, err := s.q.LookupResource(ctx, LookupResourceParams{AccountID: account, KeyID: key, Kind: kind, ResourceID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.Resource{}, gateway.ErrResourceNotFound
	}
	return resource(row), err
}
func (s *Store) ListResources(ctx context.Context, account, key, kind, after string, descending bool, limit int) ([]gateway.Resource, error) {
	rows, err := s.q.ListResources(ctx, ListResourcesParams{AccountID: account, KeyID: key, Kind: kind, AfterID: after, Descending: descending, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]gateway.Resource, 0, len(rows))
	for _, row := range rows {
		out = append(out, resource(row))
	}
	return out, nil
}
func (s *Store) DeleteResource(ctx context.Context, account, key, kind, id string) error {
	return s.q.DeleteResource(ctx, DeleteResourceParams{AccountID: account, KeyID: key, Kind: kind, ResourceID: id})
}

// VerifyResourceCredentials covers retained resource snapshots as well as live
// channels, so keyring rotation cannot silently orphan stored upstream objects.
func (s *Store) VerifyResourceCredentials(ctx context.Context, keyring *channel.Keyring) error {
	rows, err := s.pool.Query(ctx, `SELECT metadata FROM gateway_resources`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var route struct{ Candidate gateway.Candidate }
		if err := json.Unmarshal(raw, &route); err != nil {
			return errors.New("invalid resource credential snapshot")
		}
		if _, err := keyring.Decrypt(route.Candidate.ChannelID, route.Candidate.Credential); err != nil {
			return errors.New("resource credential unavailable")
		}
	}
	return rows.Err()
}

func (s *Store) SaveResourceItems(ctx context.Context, row gateway.Resource, ids []string) error {
	return s.q.SaveResourceItems(ctx, SaveResourceItemsParams{AccountID: row.AccountID, KeyID: row.KeyID, CallID: row.CallID, ChannelID: row.ChannelID, ModelID: row.ModelID, Metadata: row.Metadata, ItemIds: ids})
}
