package marketpg

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
)

func rootPlaintext(keys *channel.Keyring, row MarketProtectedKey) ([]byte, error) {
	plain, err := keys.Decrypt("market-protected:"+row.Purpose, channel.EncryptedCredential{Version: row.CredentialVersion, KeyID: row.EncryptionKeyID, Nonce: row.Nonce, Ciphertext: row.Ciphertext})
	if err != nil {
		return nil, errors.New("protected market root cannot be opened")
	}
	root, err := base64.StdEncoding.DecodeString(plain)
	if err != nil || len(root) != 32 {
		clear(root)
		return nil, errors.New("invalid protected market root")
	}
	return root, nil
}

// VerifyProtectedRoots fails startup before serving if any historical root key
// cannot open. Rotating the wrapping key never changes the root identity.
func (s *Store) VerifyProtectedRoots(ctx context.Context, keys *channel.Keyring) error {
	if keys == nil {
		return market.ErrInvalid
	}
	rows, err := New(s.pool).ListProtectedRoots(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		root, err := rootPlaintext(keys, row)
		clear(root)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ProtectedRoot(ctx context.Context, keys *channel.Keyring, purpose string) ([]byte, error) {
	if keys == nil || !slices.Contains([]string{"codex-context-root-v1", "cursor-root-v1", "credential-scope-root-v1"}, purpose) {
		return nil, market.ErrInvalid
	}
	var root []byte
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.LockProtectedRoot(ctx, &purpose); err != nil {
			return err
		}
		row, err := q.GetProtectedRoot(ctx, purpose)
		if err == nil {
			root, err = rootPlaintext(keys, row)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		root = make([]byte, 32)
		if _, err = rand.Read(root); err != nil {
			return err
		}
		sealed, err := keys.Encrypt("market-protected:"+purpose, 1, base64.StdEncoding.EncodeToString(root))
		if err != nil {
			return err
		}
		return q.InsertProtectedRoot(ctx, InsertProtectedRootParams{Purpose: purpose, EncryptionKeyID: sealed.KeyID, CredentialVersion: sealed.Version, Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	})
	if err != nil {
		clear(root)
		return nil, err
	}
	return root, nil
}
