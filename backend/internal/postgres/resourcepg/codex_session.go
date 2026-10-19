package resourcepg

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexsession"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/jackc/pgx/v5"
)

func (s *Store) LookupSession(ctx context.Context, id codexsession.Identity) (codexsession.Route, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := New(s.pool).LookupCodexSession(ctx, LookupCodexSessionParams{AccountID: id.AccountID, KeyID: id.KeyID, SessionID: id.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return codexsession.Route{}, gatewaycontract.ErrNotFound
	}
	return codexsession.Route{Binding: gatewaycontract.Binding{GroupID: r.GroupID, ChannelID: r.ChannelID, CredentialVersion: r.CredentialVersion}, CredentialScope: r.CredentialScope}, err
}

func (s *Store) ClaimSession(ctx context.Context, id codexsession.Identity, route codexsession.Route) (codexsession.Route, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := New(s.pool).ClaimCodexSession(ctx, ClaimCodexSessionParams{AccountID: id.AccountID, KeyID: id.KeyID, SessionID: id.SessionID, GroupID: route.GroupID, ChannelID: route.ChannelID, CredentialVersion: route.CredentialVersion, CredentialScope: route.CredentialScope})
	return codexsession.Route{Binding: gatewaycontract.Binding{GroupID: r.GroupID, ChannelID: r.ChannelID, CredentialVersion: r.CredentialVersion}, CredentialScope: r.CredentialScope}, err
}

var _ codexsession.Repository = (*Store)(nil)
