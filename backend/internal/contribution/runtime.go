package contribution

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type runtimeView struct {
	directory market.Directory
	blocked   bool
}

// Runtime is a fail-closed publication barrier. F5 installs its candidate
// builder through install, then request reads use Current with no I/O. Only
// approval/boot calls Refresh; read-only background reloads use Reload, which
// cannot clear a publication barrier. Committed mutations use Invalidate.
type Runtime struct {
	mu      sync.Mutex
	store   market.Store
	install func(context.Context, market.Directory) error
	current atomic.Pointer[runtimeView]
	prior   *runtimeView
}

func NewRuntime(store market.Store, install func(context.Context, market.Directory) error) *Runtime {
	return &Runtime{store: store, install: install}
}
func (r *Runtime) Current() (market.Directory, error) {
	v := r.current.Load()
	if v == nil || v.blocked {
		return market.Directory{}, ErrPublication
	}
	return v.directory, nil
}
func (r *Runtime) Block() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prior = r.current.Load()
	v := &runtimeView{blocked: true}
	if r.prior != nil {
		v.directory = r.prior.directory
	}
	r.current.Store(v)
}
func (r *Runtime) Restore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current.Store(r.prior)
	r.prior = nil
}
func (r *Runtime) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refresh(ctx)
}
func (r *Runtime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.current.Load(); v != nil && v.blocked {
		return ErrPublication
	}
	return r.refresh(ctx)
}
func (r *Runtime) Invalidate(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prior = r.current.Load()
	blocked := &runtimeView{blocked: true}
	if r.prior != nil {
		blocked.directory = r.prior.directory
	}
	r.current.Store(blocked)
	return r.refresh(ctx)
}
func (r *Runtime) refresh(ctx context.Context) error {
	if r.store == nil {
		return ErrInvalid
	}
	directory, err := r.store.Snapshot(ctx)
	if err != nil {
		return err
	}
	if r.install != nil {
		if err = r.install(ctx, directory); err != nil {
			return err
		}
	}
	r.current.Store(&runtimeView{directory: directory})
	r.prior = nil
	return nil
}
