// Package channelrefpg reads retained channel identities and sealed credentials.
// It has no configuration, listing, routing or mutation API.
package channelrefpg

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/keyringcheck"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ keyringcheck.CredentialSource = (*Store)(nil)

// CredentialsAfter includes business-deleted rows. Their IDs and sealed bytes
// still belong to protected historical references and must retain old AAD/key
// IDs after configuration cutover. This scan happens only at startup.
func (s *Store) CredentialsAfter(ctx context.Context, afterID string, limit int) ([]channel.StoredCredential, error) {
	if limit < 1 || limit > 500 {
		return nil, channel.ErrInvalidInput
	}
	var after any
	if afterID != "" {
		after = afterID
	}
	rows, err := s.pool.Query(ctx, `SELECT id,api_key_ciphertext,api_key_nonce,api_key_key_id FROM channels
 WHERE ($1::uuid IS NULL OR id>$1) ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]channel.StoredCredential, 0, limit)
	for rows.Next() {
		v := channel.StoredCredential{Sealed: channel.EncryptedCredential{Version: 1}}
		if err = rows.Scan(&v.RecordID, &v.Sealed.Ciphertext, &v.Sealed.Nonce, &v.Sealed.KeyID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return out, nil
}
