//go:build perfbaseline

package postgres_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
)

// The gateway performance baseline (Epic #360, G8.0). It is not part of the
// test suite: the build tag keeps it out of `go test ./...` and out of CI.
//
//	TEST_DATABASE_URL=postgres://... go -C backend test -tags perfbaseline \
//	    -run TestGatewayPerfBaseline -count=1 -timeout 60m -v ./internal/postgres \
//	    -args -perf.out=/some/dir
//
// docs/runbooks/gateway-baseline.md explains the flags, the scenarios and how to
// read the output. The numbers depend on the machine; compare runs made on the
// same machine, with nothing else busy, before and after a change.

var (
	perfScenarios = flag.String("perf.scenarios", "nonstream,stream,concurrent,budget,sticky,formats,sweep-nonstream,sweep-stream",
		"comma separated: nonstream, stream, concurrent, budget, sticky, formats, sweep-nonstream, sweep-stream")
	perfDuration   = flag.Duration("perf.duration", 30*time.Second, "measured window of a scenario run")
	perfRepeat     = flag.Int("perf.repeat", 1, "run every scenario this many times (the report shows the median)")
	perfQuiet      = flag.Float64("perf.quiet", 0, "before every run wait (up to 3 minutes) for the 1-minute load average to fall below this; 0 does not wait")
	perfSyncCommit = flag.String("perf.synccommit", "on", "synchronous_commit of the gateway's database connections: on (the default) or off")
	perfWarmup     = flag.Duration("perf.warmup", 3*time.Second, "warm-up before the measured window")
	perfConsumers  = flag.Int("perf.consumers", 100, "consumer accounts, each with its own platform keys")
	perfChannels   = flag.Int("perf.channels", 3, "channels offering the model, all at the same price")
	perfDNS        = flag.Duration("perf.dns", 0, "latency of the fake DNS resolver the outbound policy asks")
	perfOut        = flag.String("perf.out", "", "directory for pprof files, JSON results and the report; empty writes nothing")
	perfProfile    = flag.Duration("perf.profile", 20*time.Second, "length of the profiled run of a profiled scenario (needs -perf.out)")
	perfSweepStep  = flag.Duration("perf.sweepstep", 10*time.Second, "measured window of one step of a sweep")
	perfQPS        = flag.Float64("perf.qps", 0, "replace the reference rate of the named rate-driven scenarios (not the sweeps or formats)")
	perfWorkers    = flag.Int("perf.workers", 0, "replace the worker count of the concurrency scenario")
	perfAudit      = flag.Int64("perf.audit", 400, "requests of the database audit run of a scenario")
)

// ---- scenarios ----

var (
	// A small, quick answer: the whole completion arrives 50ms after the request.
	quickAnswer = gatewaybench.Profile{TTFT: 50 * time.Millisecond, Tokens: 64, InputTokens: 50}
	// 1000 tokens at 5ms: a stream of about 5 seconds.
	longStream = gatewaybench.Profile{TTFT: 200 * time.Millisecond, Interval: 5 * time.Millisecond, Tokens: 1000, InputTokens: 100}
	// 100 tokens at 20ms: a chat reply of about 2 seconds.
	chatStream = gatewaybench.Profile{TTFT: 100 * time.Millisecond, Interval: 20 * time.Millisecond, Tokens: 100, InputTokens: 50}
)

type perfScenario struct {
	name    string
	format  channel.Format
	stream  bool
	profile gatewaybench.Profile
	// qps is the reference arrival rate of an open load; with workers set
	// instead, the load is closed.
	qps     float64
	workers int
	// cap bounds the requests in flight of an open load.
	cap      int
	budget   bool // use the keys that carry budgets
	sticky   bool // continue Responses conversations with previous_response_id
	profiled bool // repeated under the profilers when -perf.out is set
}

// warmup is the warm-up before a measured window: the flag's, or for a stream
// long enough for the first requests to finish, so that the window sees the
// steady number of streams in flight.
func (sc perfScenario) warmup() time.Duration {
	if sc.stream {
		return max(*perfWarmup, sc.ramp()+sc.answerTime()*3/2)
	}
	return *perfWarmup
}

// ramp is how long the workers of a closed load take to start: one answer, so
// that they do not move in waves (see gatewaybench.Plan.Ramp).
func (sc perfScenario) ramp() time.Duration {
	if sc.workers > 0 {
		return sc.answerTime()
	}
	return 0
}

// answerTime is how long one answer takes at the upstream.
func (sc perfScenario) answerTime() time.Duration {
	if !sc.stream {
		return sc.profile.TTFT
	}
	return sc.profile.TTFT + time.Duration(sc.profile.Tokens)*sc.profile.Interval
}

func namedScenarios() map[string]perfScenario {
	scenarios := map[string]perfScenario{
		"nonstream":  {name: "nonstream-small", format: channel.FormatOpenAIChat, profile: quickAnswer, qps: 50, cap: 4000, profiled: true},
		"stream":     {name: "stream-1k", format: channel.FormatOpenAIChat, stream: true, profile: longStream, qps: 20, cap: 4000, profiled: true},
		"concurrent": {name: "concurrent-500", format: channel.FormatOpenAIChat, stream: true, profile: chatStream, workers: 500},
		"budget":     {name: "key-budget", format: channel.FormatOpenAIChat, profile: quickAnswer, qps: 50, cap: 4000, budget: true},
		"sticky":     {name: "responses-sticky", format: channel.FormatOpenAIResponses, profile: quickAnswer, qps: 50, cap: 4000, sticky: true},
	}
	for key, sc := range scenarios {
		if sc.workers == 0 && *perfQPS > 0 {
			sc.qps = *perfQPS
		}
		if sc.workers > 0 && *perfWorkers > 0 {
			sc.workers = *perfWorkers
		}
		scenarios[key] = sc
	}
	return scenarios
}

// formatScenarios covers every format, streamed and not, at a modest rate.
func formatScenarios() []perfScenario {
	var out []perfScenario
	for _, format := range []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini} {
		out = append(out,
			perfScenario{name: "format-" + string(format), format: format, profile: quickAnswer, qps: 40, cap: 4000},
			perfScenario{name: "format-" + string(format) + "-stream", format: format, stream: true,
				profile: gatewaybench.Profile{TTFT: 100 * time.Millisecond, Interval: 5 * time.Millisecond, Tokens: 200, InputTokens: 50}, qps: 10, cap: 4000})
	}
	return out
}

// conversations remembers the last response of each consumer's conversation.
type conversations struct {
	mu   sync.Mutex
	last []string
}

func newConversations(n int) *conversations { return &conversations{last: make([]string, n)} }

func (c *conversations) get(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last[i]
}

func (c *conversations) set(i int, id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.last[i] = id
	c.mu.Unlock()
}

// perfPlan is the load of a scenario: through the gateway, or straight at the
// fake upstream.
func (s *perfStack) perfPlan(sc perfScenario, throughGateway bool, chats *conversations) gatewaybench.Plan {
	base, keys := s.upstreamURL(0), s.keys
	if throughGateway {
		base = s.frontURL
	}
	if sc.budget {
		keys = s.budgetKeys
	}
	plan := gatewaybench.Plan{
		Name: sc.name, QPS: sc.qps, Concurrency: sc.workers, Timeout: time.Minute,
		Warmup: sc.warmup(), Duration: *perfDuration, Ramp: sc.ramp(),
		Next: func(seq int64) gatewaybench.Request {
			consumer := int(seq) % len(keys)
			spec := gatewaybench.RequestSpec{Format: sc.format, Stream: sc.stream}
			if throughGateway {
				spec.APIKey = keys[consumer]
			}
			if sc.sticky {
				spec.PreviousResponseID = chats.get(consumer)
			}
			return spec.Build(base)
		},
		Complete: func(tail []byte) bool { return gatewaybench.Complete(sc.format, sc.stream, tail) },
	}
	if sc.qps > 0 {
		plan.Concurrency = sc.cap
	}
	if sc.sticky {
		plan.CaptureLimit = 8 << 10
		plan.Captured = func(seq int64, _ int, body []byte) { chats.set(int(seq)%len(keys), gatewaybench.ResponseID(body)) }
	}
	return plan
}

// ---- what a run costs ----

type procSample struct {
	cpu            time.Duration
	alloc, mallocs uint64
	gcs            uint32
}

func readProc() procSample {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	tv := func(v syscall.Timeval) time.Duration {
		return time.Duration(v.Sec)*time.Second + time.Duration(v.Usec)*time.Microsecond
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return procSample{cpu: tv(usage.Utime) + tv(usage.Stime), alloc: mem.TotalAlloc, mallocs: mem.Mallocs, gcs: mem.NumGC}
}

// watchPeaks samples the goroutine count and the live heap until stopped.
func watchPeaks() (stop func() (goroutines int, heapMB float64)) {
	done, finished := make(chan struct{}), make(chan struct{})
	var peakGoroutines int
	var peakHeap uint64
	go func() {
		defer close(finished)
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			if n := runtime.NumGoroutine(); n > peakGoroutines {
				peakGoroutines = n
			}
			metrics.Read(sample)
			if sample[0].Value.Kind() == metrics.KindUint64 && sample[0].Value.Uint64() > peakHeap {
				peakHeap = sample[0].Value.Uint64()
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() (int, float64) {
		close(done)
		<-finished
		return peakGoroutines, float64(peakHeap) / (1 << 20)
	}
}

// waitQuiet holds a run back while something else keeps the machine busy.
func (s *perfStack) waitQuiet() {
	if *perfQuiet <= 0 {
		return
	}
	deadline, waited := time.Now().Add(3*time.Minute), false
	for time.Now().Before(deadline) {
		fields := strings.Fields(loadAverage())
		if len(fields) == 0 {
			return
		}
		if load, err := strconv.ParseFloat(fields[0], 64); err != nil || load < *perfQuiet {
			break
		}
		waited = true
		time.Sleep(5 * time.Second)
	}
	if waited {
		s.t.Logf("  waited for the machine to quiet down: load average now %s", loadAverage())
	}
}

func loadAverage() string {
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		return strings.Trim(strings.TrimSpace(string(out)), "{} ")
	}
	if out, err := os.ReadFile("/proc/loadavg"); err == nil {
		return strings.Join(strings.Fields(string(out))[:3], " ")
	}
	return "unknown"
}

// perfRun is one load run and what it cost the process.
type perfRun struct {
	Result         gatewaybench.Result `json:"-"`
	Summary        runSummary          `json:"summary"`
	CPUPerRequest  time.Duration       `json:"cpu_per_request_ns"`
	AllocPerReq    float64             `json:"alloc_bytes_per_request"`
	MallocsPerReq  float64             `json:"mallocs_per_request"`
	GCs            uint32              `json:"gcs"`
	PeakGoroutines int                 `json:"peak_goroutines"`
	PeakHeapMB     float64             `json:"peak_heap_mb"`
	PoolAcquires   int64               `json:"pool_acquires"`
	PoolWaited     int64               `json:"pool_acquires_that_waited"`
	PoolWaitAvg    time.Duration       `json:"pool_wait_avg_ns"`
	Dials          int64               `json:"gateway_dials"`
	Accepted       int64               `json:"upstream_connections"`
	Lookups        int64               `json:"dns_lookups"`
	Upstream       gatewaybench.UpstreamStats
	// StatementMS is the mean duration of each statement, as the client saw it.
	StatementMS map[string]float64 `json:"statement_mean_ms"`
	LoadAverage string             `json:"load_average_before"`
}

// runSummary is the JSON form of a Result.
type runSummary struct {
	Name         string             `json:"name"`
	WindowSec    float64            `json:"window_seconds"`
	Offered      int64              `json:"offered"`
	Sent         int64              `json:"sent"`
	OK           int64              `json:"ok"`
	Dropped      int64              `json:"dropped"`
	Failures     map[string]int64   `json:"failures"`
	Examples     map[string]string  `json:"failure_examples,omitempty"`
	ThroughputPS float64            `json:"throughput_per_second"`
	PeakInFlight int64              `json:"peak_in_flight"`
	TTFTMS       map[string]float64 `json:"ttft_ms"`
	TotalMS      map[string]float64 `json:"total_ms"`
}

func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func summarize(r gatewaybench.Result) runSummary {
	dist := func(d gatewaybench.Distribution) map[string]float64 {
		return map[string]float64{"p50": millis(d.Percentile(50)), "p90": millis(d.Percentile(90)), "p99": millis(d.Percentile(99)), "p999": millis(d.Percentile(99.9)), "max": millis(d.Max()), "mean": millis(d.Mean())}
	}
	return runSummary{Name: r.Name, WindowSec: r.Window.Seconds(), Offered: r.Offered, Sent: r.Sent, OK: r.OK, Dropped: r.Dropped,
		Failures: r.Failures, Examples: r.FailureExamples, ThroughputPS: r.Throughput(), PeakInFlight: r.PeakInFlight,
		TTFTMS: dist(r.TTFT), TotalMS: dist(r.Total)}
}

// run executes a plan and measures what it cost.
func (s *perfStack) run(plan gatewaybench.Plan, client *http.Client) perfRun {
	s.waitChannelsAvailable()
	s.waitQuiet()
	// Gather statistics like autovacuum does on a live database: the tables grow
	// by thousands of rows per run, and plans chosen for the size at the last
	// ANALYZE (a seq scan of a table once empty) would otherwise get slower and
	// slower for no reason that exists in production.
	if _, err := s.pool.Exec(context.Background(), `ANALYZE`); err != nil {
		s.t.Fatalf("analyze: %v", err)
	}
	runtime.GC()
	time.Sleep(time.Second) // let the previous run's stragglers finish
	var out perfRun
	out.LoadAverage = loadAverage()
	timingsBefore := s.counter.Timings()
	before := readProc()
	poolBefore, upstreamBefore := s.pool.Stat(), s.upstream.Stats()
	dialsBefore, acceptedBefore, lookupsBefore := s.dials.Load(), s.server.Accepted(), s.resolver.lookups.Load()
	stop := watchPeaks()
	withLabel("client", func() { out.Result = gatewaybench.Run(context.Background(), client, plan) })
	out.PeakGoroutines, out.PeakHeapMB = stop()
	after := readProc()
	poolAfter, upstreamAfter := s.pool.Stat(), s.upstream.Stats()

	started := float64(max(out.Result.Started, 1))
	out.CPUPerRequest = time.Duration(float64(after.cpu-before.cpu) / started)
	out.AllocPerReq = float64(after.alloc-before.alloc) / started
	out.MallocsPerReq = float64(after.mallocs-before.mallocs) / started
	out.GCs = after.gcs - before.gcs
	out.PoolAcquires = poolAfter.AcquireCount() - poolBefore.AcquireCount()
	out.PoolWaited = poolAfter.EmptyAcquireCount() - poolBefore.EmptyAcquireCount()
	if out.PoolAcquires > 0 {
		out.PoolWaitAvg = (poolAfter.AcquireDuration() - poolBefore.AcquireDuration()) / time.Duration(out.PoolAcquires)
	}
	out.Dials, out.Accepted, out.Lookups = s.dials.Load()-dialsBefore, s.server.Accepted()-acceptedBefore, s.resolver.lookups.Load()-lookupsBefore
	out.Upstream = gatewaybench.UpstreamStats{
		Requests: upstreamAfter.Requests - upstreamBefore.Requests, Streams: upstreamAfter.Streams - upstreamBefore.Streams,
		StickyChecked: upstreamAfter.StickyChecked - upstreamBefore.StickyChecked, StickyMisses: upstreamAfter.StickyMisses - upstreamBefore.StickyMisses,
	}
	out.Summary = summarize(out.Result)
	out.StatementMS = map[string]float64{}
	for name, now := range s.counter.Timings() {
		before := timingsBefore[name]
		if n := now.Count - before.Count; n > 0 {
			out.StatementMS[name] = millis((now.Total - before.Total) / time.Duration(n))
		}
	}
	return out
}

// ---- one scenario ----

type scenarioReport struct {
	Scenario perfScenario `json:"-"`
	Name     string       `json:"name"`
	Repeat   int          `json:"repeat"`
	Direct   perfRun      `json:"direct"`
	Gateway  perfRun      `json:"gateway"`
	// Queries counts the statements per request of the audit run, by sqlc name.
	Queries       map[string]float64 `json:"queries_per_request"`
	QueriesTotal  float64            `json:"queries_per_request_total"`
	QueriesWhole  float64            `json:"queries_per_request_whole_run"`
	Background    map[string]int64   `json:"background_queries_whole_scenario"`
	AuditRequests int64              `json:"audit_requests"`
}

func (s *perfStack) scenario(sc perfScenario, repeat int) scenarioReport {
	t := s.t
	t.Logf("scenario %s #%d: %s", sc.name, repeat, describe(sc))
	s.upstream.SetProfile(sc.profile)
	directChats, gatewayChats := newConversations(len(s.keys)), newConversations(len(s.keys))
	report := scenarioReport{Scenario: sc, Name: sc.name, Repeat: repeat}

	report.Direct = s.run(s.perfPlan(sc, false, directChats), s.direct)
	t.Logf("  direct : %s (load average %s)", report.Direct.Result, report.Direct.LoadAverage)

	requestsBefore, backgroundBefore := s.counter.Counts()
	report.Gateway = s.run(s.perfPlan(sc, true, gatewayChats), s.gateway)
	t.Logf("  gateway: %s (load average %s)", report.Gateway.Result, report.Gateway.LoadAverage)
	requestsAfter, backgroundAfter := s.counter.Counts()
	whole := gatewaybench.Since(requestsAfter, requestsBefore)
	report.Background = gatewaybench.Since(backgroundAfter, backgroundBefore)
	report.QueriesWhole = float64(gatewaybench.Total(whole)) / float64(max(report.Gateway.Result.Started, 1))

	s.auditQueries(sc, gatewayChats, &report)
	s.upstream.SetProfile(sc.profile)
	return report
}

func describe(sc perfScenario) string {
	load := fmt.Sprintf("%.0f requests/s, at most %d in flight", sc.qps, sc.cap)
	if sc.workers > 0 {
		load = fmt.Sprintf("%d workers back to back", sc.workers)
	}
	return fmt.Sprintf("%s stream=%v, upstream ttft %s interval %s tokens %d, %s", sc.format, sc.stream, sc.profile.TTFT, sc.profile.Interval, sc.profile.Tokens, load)
}

// auditQueries counts the statements a request sends. It uses short answers of
// the scenario's shape, so that a stream of five seconds does not make the
// audit take half an hour; the statements do not depend on the answer's length.
func (s *perfStack) auditQueries(sc perfScenario, chats *conversations, report *scenarioReport) {
	short := sc
	short.qps, short.workers = 0, 4
	short.profile.TTFT, short.profile.Interval, short.profile.Tokens = 5*time.Millisecond, 0, min(sc.profile.Tokens, 50)
	s.upstream.SetProfile(short.profile)

	// Warm the caches the engine keeps (key spend, key touch) so that the audit
	// shows a request in steady state.
	warm := s.perfPlan(short, true, chats)
	warm.Warmup, warm.Duration, warm.MaxRequests = 0, 0, int64(2*len(s.keys))
	gatewaybench.Run(context.Background(), s.gateway, warm)

	plan := s.perfPlan(short, true, chats)
	plan.Warmup, plan.Duration, plan.MaxRequests = 0, 0, *perfAudit
	before, _ := s.counter.Counts()
	var result gatewaybench.Result
	withLabel("client", func() { result = gatewaybench.Run(context.Background(), s.gateway, plan) })
	after, _ := s.counter.Counts()
	counts := gatewaybench.Since(after, before)
	report.AuditRequests = result.Sent
	report.Queries = map[string]float64{}
	for name, n := range counts {
		report.Queries[name] = float64(n) / float64(result.Sent)
	}
	report.QueriesTotal = float64(gatewaybench.Total(counts)) / float64(result.Sent)
	if result.Failed() > 0 {
		s.t.Logf("  audit: %d of %d requests failed: %v", result.Failed(), result.Sent, result.Failures)
	}
}

// ---- sweeps ----

type sweepStep struct {
	QPS  float64 `json:"qps"`
	Run  perfRun `json:"run"`
	Pass bool    `json:"pass"`
	Why  string  `json:"why"`
}

// The criteria of a sustainable rate (docs/runbooks/gateway-baseline.md).
const (
	sustainErrorRate  = 0.001
	sustainAchieved   = 0.98
	sustainTTFTMargin = 250 * time.Millisecond
)

func sustained(sc perfScenario, qps float64, r gatewaybench.Result) (bool, string) {
	var why []string
	if r.Dropped > 0 {
		why = append(why, fmt.Sprintf("%d arrivals dropped at the in-flight cap", r.Dropped))
	}
	if r.ErrorRate() > sustainErrorRate {
		why = append(why, fmt.Sprintf("error rate %.2f%%", 100*r.ErrorRate()))
	}
	if got := r.Throughput(); got < sustainAchieved*qps {
		why = append(why, fmt.Sprintf("completed %.0f/s of %.0f/s", got, qps))
	}
	if limit := sc.profile.TTFT + sustainTTFTMargin; r.TTFT.Percentile(99) > limit {
		why = append(why, fmt.Sprintf("ttft p99 %s over %s", r.TTFT.Percentile(99).Round(time.Millisecond), limit))
	}
	return len(why) == 0, strings.Join(why, "; ")
}

// sweep raises the rate step by step until a step fails, then bisects twice
// between the last good step and the first bad one. It returns every step run
// and the best rate that held.
func (s *perfStack) sweep(sc perfScenario, rates []float64) (steps []sweepStep, best float64) {
	s.upstream.SetProfile(sc.profile)
	try := func(qps float64) bool {
		plan := s.perfPlan(sc, true, newConversations(len(s.keys)))
		plan.QPS, plan.Concurrency, plan.Duration, plan.Timeout = qps, sc.cap, *perfSweepStep, 30*time.Second
		plan.Warmup = max(sc.warmup(), 3*time.Second)
		run := s.run(plan, s.gateway)
		pass, why := sustained(sc, qps, run.Result)
		steps = append(steps, sweepStep{QPS: qps, Run: run, Pass: pass, Why: why})
		s.t.Logf("  sweep %s %.0f/s: pass=%v %s | %s", sc.name, qps, pass, why, run.Result)
		time.Sleep(3 * time.Second) // let a struggling step drain
		return pass
	}
	var good, bad float64
	for _, qps := range rates {
		if try(qps) {
			good = qps
			continue
		}
		bad = qps
		break
	}
	for range 2 {
		if good == 0 || bad == 0 || bad-good < 2 {
			break
		}
		mid := float64(int((good+bad)/2/5) * 5)
		if mid <= good || mid >= bad {
			break
		}
		if try(mid) {
			good = mid
		} else {
			bad = mid
		}
	}
	return steps, good
}

// ---- profiles ----

// profiled repeats a scenario under the CPU and allocation profilers and
// writes the files into dir. The allocation profile is cumulative; the
// "before" file lets `go tool pprof -base` cut out the run.
func (s *perfStack) profiled(sc perfScenario, dir string) {
	t := s.t
	s.upstream.SetProfile(sc.profile)
	plan := s.perfPlan(sc, true, newConversations(len(s.keys)))
	plan.Warmup, plan.Duration = max(sc.warmup(), 3*time.Second), *perfProfile

	runtime.MemProfileRate = 4096
	defer func() { runtime.MemProfileRate = 512 * 1024 }()
	runtime.GC()
	writeProfile(t, filepath.Join(dir, sc.name+".allocs.before.pprof"), "allocs")
	cpu, err := os.Create(filepath.Join(dir, sc.name+".cpu.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		t.Fatal(err)
	}
	run := s.run(plan, s.gateway)
	pprof.StopCPUProfile()
	_ = cpu.Close()
	runtime.GC()
	writeProfile(t, filepath.Join(dir, sc.name+".allocs.after.pprof"), "allocs")
	t.Logf("  profiled %s: %s", sc.name, run.Result)
}

func writeProfile(t *testing.T, path, name string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := pprof.Lookup(name).WriteTo(file, 0); err != nil {
		t.Fatal(err)
	}
}

// ---- the test ----

func TestGatewayPerfBaseline(t *testing.T) {
	selected := map[string]bool{}
	for _, name := range strings.Split(*perfScenarios, ",") {
		selected[strings.TrimSpace(name)] = true
	}
	if *perfOut != "" {
		if err := os.MkdirAll(*perfOut, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	s := newPerfStack(t, *perfChannels, *perfConsumers, *perfDNS)
	s.smoke()
	s.prime()
	probeStart := s.probeDatabase()
	t.Logf("environment: %s, GOMAXPROCS %d, load average at start %s; database round trip %s, commit %s", runtime.Version(), runtime.GOMAXPROCS(0), loadAverage(),
		probeStart.RoundTripMean.Round(10*time.Microsecond), probeStart.CommitMean.Round(10*time.Microsecond))

	var reports []scenarioReport
	named := namedScenarios()
	var chosen []perfScenario
	for _, key := range []string{"nonstream", "stream", "concurrent", "budget", "sticky"} {
		if selected[key] {
			chosen = append(chosen, named[key])
		}
	}
	if selected["formats"] {
		chosen = append(chosen, formatScenarios()...)
	}
	for _, sc := range chosen {
		for repeat := 1; repeat <= *perfRepeat; repeat++ {
			reports = append(reports, s.scenario(sc, repeat))
		}
	}
	type sweepResult struct {
		Scenario string      `json:"scenario"`
		Best     float64     `json:"best_sustainable_qps"`
		Steps    []sweepStep `json:"steps"`
	}
	var sweeps []sweepResult
	if selected["sweep-nonstream"] {
		sc := named["nonstream"]
		steps, best := s.sweep(sc, []float64{25, 50, 75, 100, 125, 150, 200, 250, 300, 400, 500, 650, 800, 1000, 1250, 1500, 2000, 2500, 3000})
		sweeps = append(sweeps, sweepResult{sc.name, best, steps})
	}
	if selected["sweep-stream"] {
		sc := named["stream"]
		steps, best := s.sweep(sc, []float64{10, 20, 30, 40, 55, 70, 90, 120, 160, 200, 260})
		sweeps = append(sweeps, sweepResult{sc.name, best, steps})
	}
	if *perfOut != "" {
		for _, key := range []string{"nonstream", "stream"} {
			if selected[key] && named[key].profiled {
				s.profiled(named[key], *perfOut)
			}
		}
	}

	probeEnd := s.probeDatabase()
	var report strings.Builder
	fmt.Fprintf(&report, "\n# Gateway performance baseline\n\n%s\n", s.environment(started, probeStart, probeEnd))
	writeScenarioTables(&report, reports)
	for _, sweep := range sweeps {
		writeSweepTable(&report, sweep.Scenario, sweep.Best, sweep.Steps)
	}
	s.writeDatabaseNotes(&report)
	fmt.Print(report.String())
	if *perfOut != "" {
		_ = os.WriteFile(filepath.Join(*perfOut, "report.md"), []byte(report.String()), 0o644)
		encoded, _ := json.MarshalIndent(map[string]any{"scenarios": reports, "sweeps": sweeps}, "", "  ")
		_ = os.WriteFile(filepath.Join(*perfOut, "results.json"), encoded, 0o644)
	}
}

// smoke sends one request in every format through the gateway and checks that
// it went through the production outbound policy to the fake upstream.
func (s *perfStack) smoke() {
	t := s.t
	s.upstream.SetProfile(gatewaybench.Profile{TTFT: time.Millisecond, Tokens: 4, InputTokens: 10})
	for _, format := range []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini} {
		for _, stream := range []bool{false, true} {
			request := gatewaybench.RequestSpec{Format: format, Stream: stream, APIKey: s.keys[0]}.Build(s.frontURL)
			before := s.dials.Load()
			result := gatewaybench.Run(context.Background(), s.gateway, gatewaybench.Plan{
				Name: "smoke", Next: func(int64) gatewaybench.Request { return request }, Concurrency: 1, MaxRequests: 1,
				Complete: func(tail []byte) bool { return gatewaybench.Complete(format, stream, tail) },
			})
			if result.OK != 1 {
				t.Fatalf("smoke %s stream=%v: %v %v", format, stream, result.Failures, result.FailureExamples)
			}
			if s.dials.Load() == before {
				t.Fatalf("smoke %s stream=%v: the request did not reach the fake upstream through the outbound policy", format, stream)
			}
		}
	}
	if s.resolver.lookups.Load() == 0 {
		t.Fatal("the outbound policy never asked the resolver")
	}
}

// prime puts a few thousand calls into the tables before the first run. The
// planner decides by the table sizes it last saw; tables that are still empty
// get sequential scans in the ledger's triggers, which a live database, whose
// tables are never empty, does not. This is warm-up of the database, not part
// of any measurement.
func (s *perfStack) prime() {
	s.upstream.SetProfile(gatewaybench.Profile{TTFT: 2 * time.Millisecond, Tokens: 8, InputTokens: 20})
	sc := perfScenario{name: "prime", format: channel.FormatOpenAIChat, workers: 4}
	plan := s.perfPlan(sc, true, nil)
	plan.Warmup, plan.Duration, plan.MaxRequests = 0, 0, 2000
	result := gatewaybench.Run(context.Background(), s.gateway, plan)
	if result.Failed() > 0 {
		s.t.Fatalf("priming the database failed: %v %v", result.Failures, result.FailureExamples)
	}
	if _, err := s.pool.Exec(context.Background(), `ANALYZE`); err != nil {
		s.t.Fatalf("analyze: %v", err)
	}
}

func (s *perfStack) environment(started time.Time, before, after databaseProbe) string {
	cpu := "unknown"
	if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
		cpu = strings.TrimSpace(string(out))
	}
	probe := func(p databaseProbe) string {
		return fmt.Sprintf("round trip mean %s p99 %s, commit mean %s p99 %s",
			p.RoundTripMean.Round(10*time.Microsecond), p.RoundTripP99.Round(10*time.Microsecond), p.CommitMean.Round(10*time.Microsecond), p.CommitP99.Round(10*time.Microsecond))
	}
	return fmt.Sprintf("- Go %s %s/%s, GOMAXPROCS %d, %s\n- %d channels at the same price, %d consumers (2 keys each); database pool max %d connections; synchronous_commit=%s\n"+
		"- fake DNS latency %s; scenario window %s after %s warm-up, %d run(s) per scenario\n"+
		"- database as this process sees it, before the runs: %s\n- and after them: %s\n- load average at the start %s, at the end %s\n- started %s, took %s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), cpu,
		s.channelCount, len(s.keys), s.pool.Config().MaxConns, *perfSyncCommit, *perfDNS, *perfDuration, *perfWarmup, *perfRepeat,
		probe(before), probe(after), s.startLoad, loadAverage(),
		started.Format(time.RFC3339), time.Since(started).Round(time.Second))
}

// writeDatabaseNotes reports how big the calls table grew, and how long the
// minute-by-minute channel stats query takes on it.
func (s *perfStack) writeDatabaseNotes(report *strings.Builder) {
	var calls int64
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls`).Scan(&calls); err != nil {
		return
	}
	started := time.Now()
	_, err := s.e.store.Gateway.ChannelStats(context.Background())
	elapsed := time.Since(started)
	fmt.Fprintf(report, "\n## Database\n\n- `calls` holds %d rows at the end of the run.\n", calls)
	if err == nil {
		fmt.Fprintf(report, "- `ChannelStats24h` (run once a minute by the engine) takes %s over them.\n", elapsed.Round(time.Millisecond))
	}
	rows, err := s.pool.Query(context.Background(), `
		SELECT relname, seq_scan, seq_tup_read, coalesce(idx_scan, 0), n_live_tup
		FROM pg_stat_user_tables WHERE schemaname = current_schema() ORDER BY seq_tup_read DESC LIMIT 6`)
	if err != nil {
		return
	}
	defer rows.Close()
	fmt.Fprint(report, "\nTables read by sequential scans the most:\n\n| table | seq scans | rows read by them | index scans | live rows |\n|---|---|---|---|---|\n")
	for rows.Next() {
		var name string
		var seqScans, seqRows, indexScans, live int64
		if rows.Scan(&name, &seqScans, &seqRows, &indexScans, &live) == nil {
			fmt.Fprintf(report, "| %s | %d | %d | %d | %d |\n", name, seqScans, seqRows, indexScans, live)
		}
	}
}

// ---- tables ----

func ms(d time.Duration) string { return fmt.Sprintf("%.1f", millis(d)) }

// rowName labels a run in a table; the repeat number only when there are several.
func rowName(r scenarioReport) string {
	if *perfRepeat > 1 {
		return fmt.Sprintf("%s #%d", r.Name, r.Repeat)
	}
	return r.Name
}

func addedP50(r scenarioReport) float64 {
	return millis(r.Gateway.Result.TTFT.Percentile(50) - r.Direct.Result.TTFT.Percentile(50))
}

func addedP99(r scenarioReport) float64 {
	return millis(r.Gateway.Result.TTFT.Percentile(99) - r.Direct.Result.TTFT.Percentile(99))
}

// spread formats the median of the values with their range.
func spread(values []float64, format string) string {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	if len(sorted) == 1 {
		return fmt.Sprintf(format, median)
	}
	return fmt.Sprintf(format+" ["+format+" – "+format+"]", median, sorted[0], sorted[len(sorted)-1])
}

func writeScenarioTables(w *strings.Builder, reports []scenarioReport) {
	if len(reports) == 0 {
		return
	}
	if *perfRepeat > 1 {
		fmt.Fprintf(w, "\n## Added latency over %d runs: median [min – max]\n\n| scenario | added ttft p50 (ms) | added ttft p99 (ms) | gateway throughput (/s) | CPU ms/req net |\n|---|---|---|---|---|\n", *perfRepeat)
		var order []string
		groups := map[string][]scenarioReport{}
		for _, r := range reports {
			if _, seen := groups[r.Name]; !seen {
				order = append(order, r.Name)
			}
			groups[r.Name] = append(groups[r.Name], r)
		}
		for _, name := range order {
			var p50, p99, rate, cpu []float64
			for _, r := range groups[name] {
				p50, p99, rate = append(p50, addedP50(r)), append(p99, addedP99(r)), append(rate, r.Gateway.Result.Throughput())
				cpu = append(cpu, millis(r.Gateway.CPUPerRequest-r.Direct.CPUPerRequest))
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", name, spread(p50, "%.1f"), spread(p99, "%.1f"), spread(rate, "%.0f"), spread(cpu, "%.2f"))
		}
	}
	fmt.Fprint(w, "\n## Latency through the gateway against the fake upstream\n\n")
	fmt.Fprint(w, "TTFT is the time from sending the request to the first byte of the body, as the client sees it. Added = gateway minus direct.\n\n")
	fmt.Fprint(w, "| scenario | load | ok / sent | failed | direct p50 | direct p99 | gateway p50 | gateway p99 | **added p50** | **added p99** | gateway total p50 | machine load (1 min) |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range reports {
		d, g := r.Direct.Result, r.Gateway.Result
		load := fmt.Sprintf("%.0f/s", r.Scenario.qps)
		if r.Scenario.workers > 0 {
			load = fmt.Sprintf("%d workers", r.Scenario.workers)
		}
		fmt.Fprintf(w, "| %s | %s | %d / %d | %d | %s | %s | %s | %s | **%s** | **%s** | %s | %s |\n", rowName(r), load, g.OK, g.Sent, g.Failed(),
			ms(d.TTFT.Percentile(50)), ms(d.TTFT.Percentile(99)), ms(g.TTFT.Percentile(50)), ms(g.TTFT.Percentile(99)),
			ms(g.TTFT.Percentile(50)-d.TTFT.Percentile(50)), ms(g.TTFT.Percentile(99)-d.TTFT.Percentile(99)), ms(g.Total.Percentile(50)),
			strings.Fields(r.Direct.LoadAverage + " ")[0]+" / "+strings.Fields(r.Gateway.LoadAverage + " ")[0])
	}

	fmt.Fprint(w, "\n## Cost per request (whole process, warm-up included)\n\n")
	fmt.Fprint(w, "Net = gateway run minus direct run: what the gateway and its database add. The process holds the gateway, the fake upstreams and the load generator; the database server is not in the CPU figure.\n\n")
	fmt.Fprint(w, "| scenario | throughput | CPU ms/req gateway | CPU ms/req direct | **CPU ms/req net** | KB/req net | allocs/req net | peak goroutines | peak heap MB | DB acquires/req | DB waited | upstream conns/req | DNS lookups/req |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range reports {
		g, d := r.Gateway, r.Direct
		started := float64(max(g.Result.Started, 1))
		waited := 0.0
		if g.PoolAcquires > 0 {
			waited = 100 * float64(g.PoolWaited) / float64(g.PoolAcquires)
		}
		fmt.Fprintf(w, "| %s | %.0f/s | %.2f | %.2f | **%.2f** | %.1f | %.0f | %d | %.0f | %.1f | %.1f%% (avg %s ms) | %.2f | %.2f |\n", rowName(r), g.Result.Throughput(),
			millis(g.CPUPerRequest), millis(d.CPUPerRequest), millis(g.CPUPerRequest-d.CPUPerRequest),
			(g.AllocPerReq-d.AllocPerReq)/1024, g.MallocsPerReq-d.MallocsPerReq, g.PeakGoroutines, g.PeakHeapMB,
			float64(g.PoolAcquires)/started, waited, ms(g.PoolWaitAvg), float64(g.Accepted)/started, float64(g.Lookups)/started)
	}

	fmt.Fprint(w, "\n## Database statements per request\n\n")
	fmt.Fprint(w, "Counted at the pool by sqlc query name over an audit run of short answers in steady state (caches warm); begin and commit are statements too. Background work (settings refresh, stale call sweep) is not included.\n\n")
	for _, r := range reports {
		if r.Repeat > 1 {
			continue // the statements do not change from one run to the next
		}
		fmt.Fprintf(w, "**%s** — %.2f statements per request (%d audit requests; %.2f over the whole gateway run)\n\n", r.Name, r.QueriesTotal, r.AuditRequests, r.QueriesWhole)
		names := make([]string, 0, len(r.Queries))
		for name := range r.Queries {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			if r.Queries[names[i]] != r.Queries[names[j]] {
				return r.Queries[names[i]] > r.Queries[names[j]]
			}
			return names[i] < names[j]
		})
		fmt.Fprint(w, "| statement | per request | mean ms (gateway run) |\n|---|---|---|\n")
		for _, name := range names {
			fmt.Fprintf(w, "| %s | %.2f | %.2f |\n", name, r.Queries[name], r.Gateway.StatementMS[name])
		}
		fmt.Fprint(w, "\n")
	}
	for _, r := range reports {
		if r.Scenario.sticky && r.Repeat == 1 {
			u := r.Gateway.Upstream
			fmt.Fprintf(w, "Sticky routing check (%s): the upstream judged %d follow-up requests, %d reached a channel other than the one that answered first.\n\n", r.Name, u.StickyChecked, u.StickyMisses)
		}
	}
}

func writeSweepTable(w *strings.Builder, name string, best float64, steps []sweepStep) {
	fmt.Fprintf(w, "\n## Sweep: %s\n\n", name)
	fmt.Fprintf(w, "Highest rate that held: **%.0f requests/s**. A step holds when no arrival is dropped, the error rate is at most %.1f%%, at least %.0f%% of the rate completes, and ttft p99 stays within %s of the upstream's own.\n\n",
		best, 100*sustainErrorRate, 100*sustainAchieved, sustainTTFTMargin)
	fmt.Fprint(w, "| rate | completed/s | failed | dropped | ttft p50 | ttft p99 | peak in flight | CPU ms/req | DB waited | LockLedgerAccounts ms | commit ms | held | why not |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, step := range steps {
		r := step.Run
		waited := 0.0
		if r.PoolAcquires > 0 {
			waited = 100 * float64(r.PoolWaited) / float64(r.PoolAcquires)
		}
		fmt.Fprintf(w, "| %.0f | %.0f | %d | %d | %s | %s | %d | %.2f | %.0f%% | %.1f | %.1f | %v | %s |\n", step.QPS, r.Result.Throughput(), r.Result.Failed(), r.Result.Dropped,
			ms(r.Result.TTFT.Percentile(50)), ms(r.Result.TTFT.Percentile(99)), r.Result.PeakInFlight, millis(r.CPUPerRequest), waited,
			r.StatementMS["LockLedgerAccounts"], r.StatementMS["(commit)"], step.Pass, step.Why)
	}
}
