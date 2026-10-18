package detection

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
)

// Private worker synchronization only. This path is never registered on the
// platform router and supplies no supplier assertion or response body.
type codexProbeActivity struct {
	started, received, finished, compact, foreground int
	changed                                          chan struct{}
}

type codexClientCancellationKey struct{}

func (a *codexProbeActivity) notify() {
	close(a.changed)
	a.changed = make(chan struct{})
}

func (b *codexBridge) startActivity(compact bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := &b.activity
	if compact {
		a.started++
		a.compact++
		if a.foreground > 0 {
			b.wire.BackgroundOverlaps++
		}
	} else {
		a.foreground++
		if a.compact > 0 {
			b.wire.BackgroundOverlaps++
		}
	}
	a.notify()
}

func (b *codexBridge) finishActivity(compact, cancelled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := &b.activity
	if compact {
		a.compact--
		a.finished++
		if cancelled {
			b.wire.CancelledCompactions++
		}
	} else {
		a.foreground--
	}
	a.notify()
}

func (b *codexBridge) receivedActivity() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activity.received++
	b.activity.notify()
}

func (b *codexBridge) serveAsyncState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	after, err := strconv.Atoi(r.URL.Query().Get("after"))
	if err != nil || after < 0 || after > 8 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	finished := r.URL.Query().Get("finished") == "true"
	received := r.URL.Query().Get("received") == "true"
	for {
		b.mu.Lock()
		a, closed := b.activity, b.closed
		b.mu.Unlock()
		if closed {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if a.started > after && (!received || a.received > after) && (!finished || a.finished > after) {
			_ = json.NewEncoder(w).Encode(map[string]int{"started": a.started, "received": a.received, "finished": a.finished})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-a.changed:
		}
	}
}
