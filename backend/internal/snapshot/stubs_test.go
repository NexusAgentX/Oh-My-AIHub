package snapshot

import (
	"context"
	"sync/atomic"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// countingStore stands for the database store behind the gateway's cache.
// Everything the snapshots answer counts as a call that should not have
// happened; the Responses lookups and call endings are the ones that reach it
// by design.
type countingStore struct {
	gateway.Store
	calls            atomic.Int64
	lookups          atomic.Int64
	responseChannels map[string]string
	finishResult     gateway.FinishResult
}

func (s *countingStore) LookupKey(context.Context, []byte) (gateway.KeyAuth, error) {
	s.calls.Add(1)
	return gateway.KeyAuth{}, gateway.ErrKeyNotFound
}

func (s *countingStore) Candidates(context.Context, string, channel.Format) ([]gateway.Candidate, error) {
	s.calls.Add(1)
	return nil, nil
}

func (s *countingStore) OnlineModels(context.Context) ([]gateway.OnlineModel, error) {
	s.calls.Add(1)
	return nil, nil
}

func (s *countingStore) ResponseChannel(_ context.Context, accountID, responseID string) (string, error) {
	s.lookups.Add(1)
	if id, found := s.responseChannels[accountID+"/"+responseID]; found {
		return id, nil
	}
	return "", pgx.ErrNoRows
}

func (s *countingStore) FinishCall(context.Context, gateway.CallFinish) (gateway.FinishResult, error) {
	return s.finishResult, nil
}
