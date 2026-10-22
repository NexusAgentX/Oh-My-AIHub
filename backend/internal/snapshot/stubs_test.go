package snapshot

import (
	"context"
	"sync/atomic"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// countingStore stands for the database store behind the gateway's cache.
// Everything the snapshots answer counts as a call that should not have
// happened.
type countingStore struct {
	gateway.Store
	calls atomic.Int64
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
