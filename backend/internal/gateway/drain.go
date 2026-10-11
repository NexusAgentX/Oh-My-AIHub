package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// drainWindow is how long the gateway keeps reading an answer after the
// client has left, so that the usage the upstream reports at the very end is
// still read and billed (ADR-0035 rule 6).
const drainWindow = 10 * time.Second

// Why an attempt was cancelled because of its client.
var (
	errClientGone    = errors.New("client left before the answer began")
	errDrainDeadline = errors.New("drain window over")
)

// clientWatch follows the client of one upstream attempt. Until the gateway
// has started writing the answer (commit), a client that leaves cancels the
// attempt at once and nothing is billed. After that the attempt carries on
// without the client for at most the drain window: the gateway stops writing,
// keeps reading and feeds the usage observer, and the attempt ends when the
// answer does, the window is over, or the attempt's own clocks stop it.
type clientWatch struct {
	cancel context.CancelCauseFunc
	window time.Duration
	unhook func() bool

	mutex     sync.Mutex
	committed bool
	gone      bool
	stopped   bool
	drain     *time.Timer
}

// watchClient starts watching the client's request context. cancel stops the
// attempt; it is called with errClientGone or errDrainDeadline.
func watchClient(client context.Context, cancel context.CancelCauseFunc, window time.Duration) *clientWatch {
	w := &clientWatch{cancel: cancel, window: window}
	w.unhook = context.AfterFunc(client, w.left)
	return w
}

// left records that the client is gone: its request was cancelled, or writing
// to it failed. It is safe to call more than once.
func (w *clientWatch) left() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.gone || w.stopped {
		return
	}
	w.gone = true
	if !w.committed {
		w.cancel(errClientGone)
		return
	}
	w.drain = time.AfterFunc(w.window, func() { w.cancel(errDrainDeadline) })
}

// commit marks that the gateway is about to write the answer. It reports
// whether the client is still there; if not, the attempt has been cancelled
// and nothing is to be written.
func (w *clientWatch) commit() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.committed = true
	return !w.gone
}

func (w *clientWatch) isGone() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.gone
}

// stop ends the watch; the attempt is over.
func (w *clientWatch) stop() {
	w.unhook()
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.stopped = true
	if w.drain != nil {
		w.drain.Stop()
	}
}
