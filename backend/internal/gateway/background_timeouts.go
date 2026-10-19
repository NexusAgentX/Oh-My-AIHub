package gateway

import (
	"context"
	"io"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// The creating attempt already recorded fully resolved channel/group/model/
// platform timeout stages. Older metadata falls back to its frozen defaults.
func frozenBackgroundTimeouts(metadata backgroundMetadata) attemptTimeouts {
	if len(metadata.Attempts) > 0 {
		if saved := metadata.Attempts[len(metadata.Attempts)-1].Timeouts; saved != nil {
			return attemptTimeouts{time.Duration(saved.FirstByteMS) * time.Millisecond, time.Duration(saved.IdleMS) * time.Millisecond, time.Duration(saved.TotalMS) * time.Millisecond}
		}
	}
	return resolveTimeouts(metadata.Settings, routing.Pref{}).withChannel(metadata.Candidate.Advanced)
}

type backgroundReadBody struct {
	io.ReadCloser
	ctx  context.Context
	read func()
}

func (r backgroundReadBody) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.ReadCloser.Read(p)
	if expired := r.ctx.Err(); expired != nil {
		return 0, expired
	}
	if n > 0 {
		r.read()
	}
	return n, err
}
