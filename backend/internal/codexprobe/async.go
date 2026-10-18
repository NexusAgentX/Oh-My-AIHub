package codexprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func asyncSeed(id string) string {
	return "Remember the exact synthetic code AIHUB-ASYNC-" + id + ". Reply with exactly OK. Synthetic context padding: " + strings.Repeat("violet azure amber seven ", 512)
}

// Waiting for an observed request/event is bounded synchronization, not a TTL
// or elapsed-time capability test. The private observer never calls upstream.
func asyncState(ctx context.Context, c Config, after int, finished bool) (int, error) {
	ca, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), c.CA))
	if err != nil {
		return 0, ErrClient
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return 0, ErrClient
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxConnsPerHost: 1, MaxIdleConns: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Origin+"/probe/async-state?after="+strconv.Itoa(after)+"&finished="+strconv.FormatBool(finished), nil)
	if err != nil {
		return 0, ErrClient
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, ErrClient
	}
	defer resp.Body.Close()
	var state struct{ Started, Finished int }
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&state) != nil || state.Started <= after || state.Started > 8 || finished && state.Finished <= after {
		return 0, ErrClient
	}
	return state.Started, nil
}

func runAsyncCompaction(ctx context.Context, c Config, d *dialogue, first string, params map[string]any, r *Result) error {
	r.Stage = "async_overlap"
	if _, err := asyncState(ctx, c, 0, false); err != nil {
		return err
	}
	d.expected = "OK"
	if d.runTurn(ctx, first, "Also remember the synthetic suffix AIHUB-ASYNC-SUFFIX. Reply with exactly OK.") != nil {
		return ErrClient
	}
	if _, err := asyncState(ctx, c, 0, true); err != nil {
		return err
	}
	// Publication is adopted at a subsequent foreground checkpoint. Waiting
	// for a compaction item before starting that turn would deadlock the client.
	d.expected = "AIHUB-ASYNC-" + first + " AIHUB-ASYNC-SUFFIX"
	r.Stage = "async_reuse"
	if d.runTurn(ctx, first, "Return only the remembered synthetic code and the later synthetic suffix separated by one space, without labels or explanation.") != nil || !d.matched || !d.compacted {
		return ErrClient
	}
	r.ContextVerified = true
	count, err := asyncState(ctx, c, 0, true)
	if err != nil {
		return err
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	r.Stage = "async_shutdown"
	if d.call(ctx, "thread/start", params, &thread) != nil || thread.Thread.ID == "" || thread.Thread.ID == first {
		return ErrClient
	}
	d.thread, d.expected = thread.Thread.ID, "OK"
	if d.runTurn(ctx, thread.Thread.ID, asyncSeed(thread.Thread.ID)+" Controlled client cancellation case AIHUB-ASYNC-CANCEL-"+thread.Thread.ID) != nil || !d.matched {
		return ErrClient
	}
	if _, err = asyncState(ctx, c, count, false); err != nil {
		return err
	}
	if d.call(ctx, "thread/archive", map[string]any{"threadId": thread.Thread.ID}, nil) != nil {
		return ErrClient
	}
	if _, err = asyncState(ctx, c, count, true); err != nil {
		return err
	}
	r.ShutdownVerified = true
	d.matched = r.ContextVerified && r.ShutdownVerified
	return nil
}
