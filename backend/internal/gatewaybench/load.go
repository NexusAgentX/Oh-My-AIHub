package gatewaybench

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Request is one request a load plan sends.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Plan is a load: what to send, how fast, for how long.
//
// With QPS above zero the load is open: requests arrive at that rate whether
// or not earlier ones have finished, so a slow server shows up as latency and
// not as a lower rate. Concurrency then only caps how many may be in flight;
// an arrival that finds the cap reached is dropped and counted. With QPS zero
// the load is closed: Concurrency workers send one request after the other as
// fast as the server answers.
type Plan struct {
	Name string
	// Next builds the seq-th request; it is called from many goroutines.
	Next        func(seq int64) Request
	QPS         float64
	Concurrency int
	// Warmup runs first and is left out of the result; Duration is the measured
	// window that follows. Requests count when they start inside the window,
	// and the load waits for them to finish before it returns.
	Warmup   time.Duration
	Duration time.Duration
	// Ramp spreads the first requests of a closed load: worker i of n waits
	// i/n of Ramp before it begins. Workers that start together and get answers
	// of one length stay together, and arrive in waves that no real crowd of
	// clients makes; the ramp should be about the length of one answer.
	Ramp time.Duration
	// MaxRequests, when positive, ends the measured window after that many
	// requests have started instead of after Duration.
	MaxRequests int64
	// Timeout bounds one request, answer included. Zero means two minutes.
	Timeout time.Duration
	// Complete, when set, judges the last bytes of a successful answer; false
	// counts the request as incomplete. Nil accepts every answer that ends cleanly.
	Complete func(tail []byte) bool
	// Captured, when set, receives the body of every successful answer (at most
	// CaptureLimit bytes of it), for a load that reads the answer.
	Captured     func(seq int64, status int, body []byte)
	CaptureLimit int
}

// maxOpenInFlight caps the in-flight requests of an open load without Concurrency.
const maxOpenInFlight = 20000

// tailBytes is how much of an answer's end Complete gets to see: enough for
// the last event of an OpenAI Responses stream, which repeats the whole text.
const tailBytes = 16 << 10

type sample struct {
	start, end time.Duration // since the run began
	ttft       time.Duration
	total      time.Duration
	failure    string // empty for a good answer
	message    string // what the client said about the failure
	bytes      int64
	counted    bool // started inside the measured window
}

// Result is what a load saw. Latencies are those of good answers that started
// in the measured window.
type Result struct {
	Name string
	// Window is the length of the measured window.
	Window time.Duration
	// Offered counts the arrivals the schedule produced in the window (open
	// loads), Sent the requests actually started, Dropped the arrivals refused
	// because the in-flight cap was reached.
	Offered, Sent, Dropped int64
	OK                     int64
	// Failures counts the other requests by kind: "status_503", "timeout",
	// "reset", "incomplete", ...; FailureExamples keeps the first message of each kind.
	Failures        map[string]int64
	FailureExamples map[string]string
	// Started counts every request the load began, warm-up included: the
	// divisor for what a whole run cost.
	Started int64
	// Completed counts the good answers that finished inside the window, whatever
	// time they started: the throughput of a load whose requests outlive it.
	Completed    int64
	PeakInFlight int64
	TTFT, Total  Distribution
	BytesRead    int64
}

// Failed is the number of requests that did not end well.
func (r Result) Failed() int64 {
	var n int64
	for _, count := range r.Failures {
		n += count
	}
	return n
}

// ErrorRate is the share of started requests that failed.
func (r Result) ErrorRate() float64 {
	if r.Sent == 0 {
		return 0
	}
	return float64(r.Failed()) / float64(r.Sent)
}

// Throughput is the good answers per second that finished inside the window.
func (r Result) Throughput() float64 {
	if r.Window <= 0 {
		return 0
	}
	return float64(r.Completed) / r.Window.Seconds()
}

// Distribution is a set of durations that answers percentile questions.
type Distribution struct{ sorted []time.Duration }

// NewDistribution returns the distribution of the given durations.
func NewDistribution(values []time.Duration) Distribution {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return Distribution{sorted: sorted}
}

// Count is the number of values.
func (d Distribution) Count() int { return len(d.sorted) }

// Percentile returns the nearest-rank percentile (p in 0..100), zero when empty.
func (d Distribution) Percentile(p float64) time.Duration {
	if len(d.sorted) == 0 {
		return 0
	}
	rank := int(p/100*float64(len(d.sorted)) + 0.999999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(d.sorted) {
		rank = len(d.sorted)
	}
	return d.sorted[rank-1]
}

// Max is the largest value.
func (d Distribution) Max() time.Duration { return d.Percentile(100) }

// Mean is the average value.
func (d Distribution) Mean() time.Duration {
	if len(d.sorted) == 0 {
		return 0
	}
	var sum time.Duration
	for _, value := range d.sorted {
		sum += value
	}
	return sum / time.Duration(len(d.sorted))
}

// ClientConfig configures the HTTP client of a load.
type ClientConfig struct {
	// RootCAs are the roots the client trusts for https URLs.
	RootCAs *x509.CertPool
	// Dial, when set, replaces the dialer: the direct runs use it to reach a
	// fake upstream under a host name that does not resolve.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewClient returns an HTTP/1.1 client that keeps connections alive, as an SDK
// does, with room for as many idle connections as a load can have in flight.
func NewClient(config ClientConfig) *http.Client {
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         config.Dial,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: config.RootCAs},
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: maxOpenInFlight,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var bufferPool = sync.Pool{New: func() any { buffer := make([]byte, 32<<10); return &buffer }}

type runner struct {
	client  *http.Client
	plan    Plan
	callCtx context.Context

	t0          time.Time // the run began
	measureFrom time.Time // the measured window opens
	measureEnd  time.Time // it closes (time-boxed plans)

	measuredStarts atomic.Int64
	seq            atomic.Int64
	inflight       atomic.Int64
	peak           atomic.Int64

	mu      sync.Mutex
	samples []sample
	offered int64
	dropped int64
	wg      sync.WaitGroup
}

// Run executes the plan against the client and returns what it saw.
func Run(ctx context.Context, client *http.Client, plan Plan) Result {
	if plan.Timeout <= 0 {
		plan.Timeout = 2 * time.Minute
	}
	r := &runner{client: client, plan: plan, callCtx: ctx}
	r.t0 = time.Now()
	r.measureFrom = r.t0.Add(plan.Warmup)
	r.measureEnd = r.measureFrom.Add(plan.Duration)
	if plan.QPS > 0 {
		r.openLoop()
	} else {
		r.closedLoop()
	}
	r.wg.Wait()
	return r.result()
}

// windowOpen reports whether the measured window still takes requests (or has
// not begun).
func (r *runner) windowOpen(now time.Time) bool {
	if r.plan.MaxRequests > 0 {
		return r.measuredStarts.Load() < r.plan.MaxRequests
	}
	return now.Before(r.measureEnd)
}

// admit counts a request that starts now against the window and reports
// whether it lies inside it.
func (r *runner) admit(now time.Time) bool {
	if now.Before(r.measureFrom) {
		return false
	}
	r.measuredStarts.Add(1)
	return true
}

func (r *runner) openLoop() {
	limit := int64(r.plan.Concurrency)
	if limit <= 0 {
		limit = maxOpenInFlight
	}
	var issued int64
	for r.callCtx.Err() == nil {
		now := time.Now()
		due := int64(now.Sub(r.t0).Seconds()*r.plan.QPS) + 1
		for ; issued < due; issued++ {
			now = time.Now()
			if !r.windowOpen(now) {
				return
			}
			counted := !now.Before(r.measureFrom)
			if counted {
				r.mu.Lock()
				r.offered++
				r.mu.Unlock()
			}
			if r.inflight.Load() >= limit {
				if counted {
					r.mu.Lock()
					r.dropped++
					r.mu.Unlock()
				}
				continue
			}
			counts := r.admit(now)
			r.begin()
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				defer r.inflight.Add(-1)
				r.do(r.seq.Add(1)-1, counts)
			}()
		}
		next := r.t0.Add(time.Duration(float64(issued) / r.plan.QPS * float64(time.Second)))
		if wait := min(time.Until(next), time.Millisecond); wait > 0 {
			time.Sleep(wait)
		}
	}
}

// begin counts a request as in flight and tracks the peak.
func (r *runner) begin() {
	now := r.inflight.Add(1)
	for {
		peak := r.peak.Load()
		if now <= peak || r.peak.CompareAndSwap(peak, now) {
			return
		}
	}
}

func (r *runner) closedLoop() {
	workers := r.plan.Concurrency
	if workers <= 0 {
		workers = 1
	}
	for index := range workers {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			if delay := time.Duration(int64(r.plan.Ramp) * int64(index) / int64(workers)); delay > 0 {
				time.Sleep(delay)
			}
			for r.callCtx.Err() == nil {
				// Admission and the window check must be one step, or two workers
				// could both take the last place of a MaxRequests plan.
				r.mu.Lock()
				now := time.Now()
				open, counts := r.windowOpen(now), false
				if open {
					counts = r.admit(now)
				}
				r.mu.Unlock()
				if !open {
					return
				}
				r.begin()
				r.do(r.seq.Add(1)-1, counts)
				r.inflight.Add(-1)
			}
		}()
	}
}

func (r *runner) do(seq int64, counted bool) {
	request := r.plan.Next(seq)
	got := r.exchange(request, seq, time.Now())
	got.counted = counted
	r.mu.Lock()
	r.samples = append(r.samples, got)
	r.mu.Unlock()
}

// exchange performs one request and describes how it went.
func (r *runner) exchange(request Request, seq int64, start time.Time) (out sample) {
	out.start = start.Sub(r.t0)
	defer func() {
		out.end = time.Since(r.t0)
		out.total = time.Since(start)
	}()
	ctx, cancel := context.WithTimeout(r.callCtx, r.plan.Timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		out.failure = "bad_request"
		return out
	}
	httpRequest.Header = request.Header.Clone()
	response, err := r.client.Do(httpRequest)
	if err != nil {
		out.failure, out.message = classify(err), err.Error()
		return out
	}
	defer response.Body.Close()
	bufferPointer := bufferPool.Get().(*[]byte)
	defer bufferPool.Put(bufferPointer)
	buffer := *bufferPointer

	var tail, captured []byte
	good := response.StatusCode >= 200 && response.StatusCode < 300
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if out.ttft == 0 {
				out.ttft = time.Since(start)
			}
			out.bytes += int64(n)
			if good {
				if r.plan.Complete != nil {
					tail = append(tail, buffer[:n]...)
					if len(tail) > 2*tailBytes {
						tail = append(tail[:0], tail[len(tail)-tailBytes:]...)
					}
				}
				if r.plan.Captured != nil && len(captured) < r.plan.CaptureLimit {
					captured = append(captured, buffer[:min(n, r.plan.CaptureLimit-len(captured))]...)
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				out.failure, out.message = "body_"+classify(readErr), readErr.Error()
				return out
			}
			break
		}
	}
	if out.ttft == 0 {
		out.ttft = time.Since(start) // an empty body: the headers were the answer
	}
	if !good {
		out.failure, out.message = "status_"+strconv.Itoa(response.StatusCode), response.Status
		return out
	}
	if r.plan.Complete != nil && !r.plan.Complete(tail) {
		out.failure = "incomplete"
		return out
	}
	if r.plan.Captured != nil {
		r.plan.Captured(seq, response.StatusCode, captured)
	}
	return out
}

func truncateMessage(message string) string {
	if len(message) > 200 {
		return message[:200]
	}
	return message
}

func classify(err error) string {
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "transport"
}

func (r *runner) result() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	from := r.measureFrom.Sub(r.t0)
	to := r.measureEnd.Sub(r.t0)
	if r.plan.MaxRequests > 0 {
		// The window of a plan with a quota ends with its last counted request.
		to = from
		for _, s := range r.samples {
			if s.counted && s.end > to {
				to = s.end + 1
			}
		}
	}
	result := Result{
		Name: r.plan.Name, Window: to - from, Offered: r.offered, Dropped: r.dropped,
		Failures: map[string]int64{}, FailureExamples: map[string]string{}, PeakInFlight: r.peak.Load(), Started: r.seq.Load(),
	}
	var ttft, total []time.Duration
	for _, s := range r.samples {
		if s.counted {
			result.Sent++
			result.BytesRead += s.bytes
			if s.failure != "" {
				result.Failures[s.failure]++
				if _, seen := result.FailureExamples[s.failure]; !seen {
					result.FailureExamples[s.failure] = truncateMessage(s.message)
				}
			} else {
				result.OK++
				ttft = append(ttft, s.ttft)
				total = append(total, s.total)
			}
		}
		if s.failure == "" && s.end >= from && s.end < to {
			result.Completed++
		}
	}
	result.TTFT, result.Total = NewDistribution(ttft), NewDistribution(total)
	if r.plan.QPS <= 0 {
		result.Offered = result.Sent // a closed load offers what it sends
	}
	return result
}

// String is a one-line summary.
func (r Result) String() string {
	return fmt.Sprintf("%s: sent %d ok %d failed %d dropped %d, %.1f/s, ttft p50 %s p99 %s, total p50 %s p99 %s",
		r.Name, r.Sent, r.OK, r.Failed(), r.Dropped, r.Throughput(),
		r.TTFT.Percentile(50).Round(time.Microsecond), r.TTFT.Percentile(99).Round(time.Microsecond),
		r.Total.Percentile(50).Round(time.Microsecond), r.Total.Percentile(99).Round(time.Microsecond))
}
