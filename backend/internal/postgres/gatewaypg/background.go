package gatewaypg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/jackc/pgx/v5"
)

func background(row GatewayBackgroundJob) gateway.BackgroundJob {
	return gateway.BackgroundJob{CallID: row.CallID, AccountID: row.AccountID, KeyID: row.KeyID, ChannelID: row.ChannelID, ResponseID: row.ResponseID, Metadata: row.Metadata, Deadline: row.DeadlineAt, Settled: row.SettledAt != nil}
}
func (s *Store) SaveBackground(ctx context.Context, job gateway.BackgroundJob) error {
	return s.q.SaveBackground(ctx, SaveBackgroundParams{CallID: job.CallID, AccountID: job.AccountID, KeyID: job.KeyID, ChannelID: job.ChannelID, ResponseID: job.ResponseID, Metadata: job.Metadata, DeadlineAt: job.Deadline})
}
func (s *Store) LookupBackground(ctx context.Context, accountID, keyID, responseID string) (gateway.BackgroundJob, error) {
	row, err := s.q.LookupBackground(ctx, LookupBackgroundParams{AccountID: accountID, KeyID: keyID, ResponseID: responseID})
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.BackgroundJob{}, gateway.ErrBackgroundNotFound
	}
	return background(row), err
}
func backgrounds(rows []GatewayBackgroundJob) []gateway.BackgroundJob {
	result := make([]gateway.BackgroundJob, 0, len(rows))
	for _, row := range rows {
		result = append(result, background(row))
	}
	return result
}
func (s *Store) PendingBackground(ctx context.Context) ([]gateway.BackgroundJob, error) {
	rows, err := s.q.PendingBackground(ctx)
	return backgrounds(rows), err
}
func (s *Store) ClaimBackground(ctx context.Context, now time.Time) ([]gateway.BackgroundJob, error) {
	rows, err := s.q.ClaimBackground(ctx, now)
	return backgrounds(rows), err
}
func (s *Store) RetryBackground(ctx context.Context, id string, at time.Time) error {
	return s.q.RetryBackground(ctx, RetryBackgroundParams{CallID: id, NextPollAt: at})
}

// VerifyBackgroundCredentials pages all retained ciphertext snapshots, including
// completed jobs that the follow-up API can still retrieve.
func (s *Store) VerifyBackgroundCredentials(ctx context.Context, keyring *channel.Keyring) error {
	after := ""
	for {
		rows, err := s.pool.Query(ctx, `SELECT call_id::text, metadata FROM gateway_background_jobs WHERE call_id::text>$1 ORDER BY call_id::text LIMIT 128`, after)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var metadata struct{ Candidate gateway.Candidate }
			if err := json.Unmarshal(raw, &metadata); err != nil {
				rows.Close()
				return fmt.Errorf("invalid background metadata %s", id)
			}
			if _, err := keyring.Decrypt(metadata.Candidate.ChannelID, metadata.Candidate.Credential); err != nil {
				rows.Close()
				return fmt.Errorf("background credential %s: %w", id, err)
			}
			after = id
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count < 128 {
			return nil
		}
	}
}
