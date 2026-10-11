package gatewaybench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func getPlan(url string) func(int64) Request {
	return func(int64) Request { return Request{Method: http.MethodGet, URL: url, Header: http.Header{}} }
}

func slowServer(delay time.Duration, status int, inflight, peak *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inflight != nil {
			now := inflight.Add(1)
			defer inflight.Add(-1)
			for {
				seen := peak.Load()
				if now <= seen || peak.CompareAndSwap(seen, now) {
					break
				}
			}
		}
		time.Sleep(delay)
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ok"))
	}))
}

func TestClosedLoadKeepsExactlyTheWorkersBusy(t *testing.T) {
	var inflight, peak atomic.Int64
	server := slowServer(20*time.Millisecond, http.StatusOK, &inflight, &peak)
	defer server.Close()
	result := Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "closed", Next: getPlan(server.URL), Concurrency: 4, Warmup: 50 * time.Millisecond, Duration: 400 * time.Millisecond,
	})
	if peak.Load() != 4 || result.PeakInFlight != 4 {
		t.Fatalf("server peak %d, load peak %d, want 4", peak.Load(), result.PeakInFlight)
	}
	if result.Failed() != 0 || result.OK != result.Sent || result.OK < 20 {
		t.Fatalf("result = %+v", result)
	}
	if result.TTFT.Percentile(50) < 20*time.Millisecond || result.Total.Percentile(50) < result.TTFT.Percentile(50) {
		t.Fatalf("ttft p50 %s total p50 %s", result.TTFT.Percentile(50), result.Total.Percentile(50))
	}
	// Four workers at 20ms each: about 200 answers a second.
	if rate := result.Throughput(); rate < 50 || rate > 250 {
		t.Fatalf("throughput %.1f/s", rate)
	}
}

func TestOpenLoadSendsAtTheRequestedRate(t *testing.T) {
	server := slowServer(time.Millisecond, http.StatusOK, nil, nil)
	defer server.Close()
	result := Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "open", Next: getPlan(server.URL), QPS: 100, Warmup: 100 * time.Millisecond, Duration: time.Second,
	})
	// A second at 100/s: about 100 starts, with generous room for a busy machine.
	if result.Sent < 70 || result.Sent > 130 || result.Dropped != 0 || result.Failed() != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.Offered != result.Sent {
		t.Fatalf("offered %d sent %d", result.Offered, result.Sent)
	}
}

func TestOpenLoadDropsArrivalsWhenTheServerCannotKeepUp(t *testing.T) {
	var inflight, peak atomic.Int64
	server := slowServer(200*time.Millisecond, http.StatusOK, &inflight, &peak)
	defer server.Close()
	result := Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "drop", Next: getPlan(server.URL), QPS: 100, Concurrency: 2, Duration: 500 * time.Millisecond,
	})
	if result.Dropped == 0 || result.PeakInFlight > 2 || peak.Load() > 2 {
		t.Fatalf("result = %+v server peak %d", result, peak.Load())
	}
	if result.Offered != result.Sent+result.Dropped {
		t.Fatalf("offered %d sent %d dropped %d", result.Offered, result.Sent, result.Dropped)
	}
}

func TestMaxRequestsEndsTheWindowAfterExactlyThatManyStarts(t *testing.T) {
	server := slowServer(time.Millisecond, http.StatusOK, nil, nil)
	defer server.Close()
	for _, plan := range []Plan{
		{Name: "closed", Concurrency: 8, MaxRequests: 40},
		{Name: "open", QPS: 400, MaxRequests: 40},
	} {
		plan.Next = getPlan(server.URL)
		result := Run(context.Background(), NewClient(ClientConfig{}), plan)
		if result.Sent != 40 || result.OK != 40 {
			t.Fatalf("%s: %+v", plan.Name, result)
		}
	}
}

func TestFailuresAreCountedByKind(t *testing.T) {
	failing := slowServer(0, http.StatusServiceUnavailable, nil, nil)
	defer failing.Close()
	result := Run(context.Background(), NewClient(ClientConfig{}), Plan{Name: "503", Next: getPlan(failing.URL), Concurrency: 2, Duration: 100 * time.Millisecond})
	if result.Sent == 0 || result.Failures["status_503"] != result.Sent || result.ErrorRate() != 1 || result.TTFT.Count() != 0 {
		t.Fatalf("result = %+v", result)
	}

	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer cut.Close()
	result = Run(context.Background(), NewClient(ClientConfig{}), Plan{Name: "cut", Next: getPlan(cut.URL), Concurrency: 1, MaxRequests: 3})
	if result.Failures["body_eof"] != 3 {
		t.Fatalf("a body cut short: %+v", result.Failures)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	result = Run(context.Background(), NewClient(ClientConfig{}), Plan{Name: "refused", Next: getPlan(gone.URL), Concurrency: 1, MaxRequests: 2})
	if result.Failures["refused"] != 2 {
		t.Fatalf("a refused connection: %+v", result.Failures)
	}

	ok := slowServer(0, http.StatusOK, nil, nil)
	defer ok.Close()
	result = Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "incomplete", Next: getPlan(ok.URL), Concurrency: 1, MaxRequests: 2,
		Complete: func(tail []byte) bool { return string(tail) == "finished" },
	})
	if result.Failures["incomplete"] != 2 {
		t.Fatalf("an answer judged incomplete: %+v", result.Failures)
	}
}

func TestCapturedGetsTheBodyUpToTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("0123456789")) }))
	defer server.Close()
	var got atomic.Value
	Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "capture", Next: getPlan(server.URL), Concurrency: 1, MaxRequests: 1, CaptureLimit: 4,
		Captured: func(_ int64, status int, body []byte) { got.Store(string(body)) },
	})
	if got.Load() != "0123" {
		t.Fatalf("captured %v", got.Load())
	}
}

func TestPercentilesUseTheNearestRank(t *testing.T) {
	var values []time.Duration
	for i := 100; i >= 1; i-- {
		values = append(values, time.Duration(i)*time.Millisecond)
	}
	d := NewDistribution(values)
	for _, c := range []struct {
		p    float64
		want time.Duration
	}{{0, time.Millisecond}, {50, 50 * time.Millisecond}, {90, 90 * time.Millisecond}, {99, 99 * time.Millisecond}, {99.9, 100 * time.Millisecond}, {100, 100 * time.Millisecond}} {
		if got := d.Percentile(c.p); got != c.want {
			t.Errorf("p%v = %s, want %s", c.p, got, c.want)
		}
	}
	if d.Mean() != 50500*time.Microsecond || d.Count() != 100 || d.Max() != 100*time.Millisecond {
		t.Errorf("mean %s count %d max %s", d.Mean(), d.Count(), d.Max())
	}
	if (Distribution{}).Percentile(99) != 0 {
		t.Error("an empty distribution has no percentiles")
	}
}

func TestRampSpreadsTheFirstRequestsOfAClosedLoad(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		time.Sleep(time.Second) // longer than the ramp: each worker sends once
	}))
	defer server.Close()
	started := time.Now()
	Run(context.Background(), NewClient(ClientConfig{}), Plan{
		Name: "ramp", Next: getPlan(server.URL), Concurrency: 4, Ramp: 400 * time.Millisecond, Duration: 600 * time.Millisecond,
	})
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) < 4 {
		t.Fatalf("%d arrivals", len(arrivals))
	}
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
	// Workers 0..3 wait 0, 100, 200 and 300 ms; allow a busy machine some slack on top.
	if spread := arrivals[3].Sub(arrivals[0]); spread < 250*time.Millisecond {
		t.Fatalf("the first four requests arrived within %s of each other (started %s)", spread, time.Since(started))
	}
}
