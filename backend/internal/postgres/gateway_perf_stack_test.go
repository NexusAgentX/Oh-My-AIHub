//go:build perfbaseline

package postgres_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"reflect"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

// The stack of the gateway performance baseline (Epic #360, G8.0): the real
// gateway engine and API handler on a real PostgreSQL database, an HTTP server
// in front of it, and fake upstreams behind it, all in this one process. See
// docs/runbooks/gateway-baseline.md.
//
// The gateway reaches its upstreams through the production OutboundPolicy,
// not through a stand-in: the same host name checks, the same DNS validation
// and cache, the same pooled transport. Two things have to differ from production
// for it to reach a server on this machine, and both are the seams the policy
// keeps for its own tests: the resolver (NewOutboundPolicyWithResolver) answers
// with public-looking addresses, and the policy's dialer and trusted roots
// (the unexported dialContext and testRootCAs, which only tests in package
// channel can set) are pointed at the fake upstream and the baseline's test CA.
// That happens here, in a test file with a build tag, never in a build of the
// server; no switch, setting or environment variable of the product can do it.

// perfResolver stands in for DNS. Every channel host resolves to its own
// public-looking address, so the policy's address checks have something to
// accept; an optional delay stands for a slow resolver.
type perfResolver struct {
	ips     map[string]net.IP
	delay   time.Duration
	lookups atomic.Int64
}

func (r *perfResolver) LookupNetIP(ctx context.Context, _, host string) ([]net.IP, error) {
	r.lookups.Add(1)
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ip, ok := r.ips[host]
	if !ok {
		return nil, fmt.Errorf("no such host %q", host)
	}
	return []net.IP{ip}, nil
}

// setUnexported assigns value to an unexported field of a struct.
func setUnexported(t *testing.T, target reflect.Value, name string, value any) {
	t.Helper()
	field := target.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("channel.OutboundPolicy has no field %q any more: update the baseline's way of pointing the policy at its fake upstreams (gateway_perf_stack_test.go)", name)
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

// pointAtLoopback gives a real policy the test CA and a dialer that connects
// every pinned address to the fake upstream.
func pointAtLoopback(t *testing.T, policy *channel.OutboundPolicy, roots *x509.CertPool, dial func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	target := reflect.ValueOf(policy).Elem()
	setUnexported(t, target, "testRootCAs", roots)
	setUnexported(t, target, "dialContext", dial)
}

// perfStack is everything a baseline run needs.
type perfStack struct {
	t       *testing.T
	e       *env
	pool    *pgxpool.Pool
	counter *gatewaybench.QueryCounter
	pki     *gatewaybench.PKI

	upstream *gatewaybench.Upstream
	server   *gatewaybench.Server
	hosts    []string
	resolver *perfResolver
	// dials counts the connections the gateway opened to the upstream.
	dials atomic.Int64

	frontURL     string
	gateway      *http.Client // for the gateway
	direct       *http.Client // for the fake upstream, straight
	keys         []string     // platform keys of the consumers
	budgetKeys   []string     // a second key per consumer, with budgets
	channelCount int
	channelIDs   []string
	startLoad    string // load average when the stack was built
}

// perfDatabase is isolatedDatabase for the baseline: the same schema per run,
// but on a pool whose statements a counter sees, and without the parallelism
// and slot bookkeeping of the integration tests.
func perfDatabase(t *testing.T, counter *gatewaybench.QueryCounter) (*pgxpool.Pool, *storepg.Store) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(basePool.Close)
	schema := "perf_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	schemaURL := withSearchPath(t, databaseURL, schema)
	migrateMu.Lock()
	err = database.Migrate(ctx, schemaURL)
	migrateMu.Unlock()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The pool settings are the server's: open a pool the way the server does,
	// take its configuration, and open the pool under test with the counter added.
	probe, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	config := probe.Config()
	probe.Close()
	config.ConnConfig.Tracer = counter
	switch *perfSyncCommit {
	case "on":
	case "off":
		// Commits no longer wait for the disk. A way to see how much of a result
		// is the storage under the database and how much the gateway.
		config.ConnConfig.RuntimeParams["synchronous_commit"] = "off"
	default:
		t.Fatalf("-perf.synccommit must be on or off, not %q", *perfSyncCommit)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, storepg.New(pool)
}

// withLabel runs f with a pprof label on its goroutine and on every goroutine
// it starts, so a CPU profile of the one process can be split into the
// gateway, the fake upstreams and the load generator.
func withLabel(component string, f func()) {
	pprof.Do(context.Background(), pprof.Labels("component", component), func(context.Context) { f() })
}

// newPerfStack builds the stack and fills it: channels owned by sharers, and
// consumers with keys.
func newPerfStack(t *testing.T, channels, consumers int, dnsDelay time.Duration) *perfStack {
	t.Helper()
	s := &perfStack{t: t, counter: gatewaybench.NewQueryCounter(), channelCount: channels, startLoad: loadAverage()}
	// The settlement's row locks: how long they stay taken is what limits the
	// rate at which requests can be billed (Epic #360, G8.7).
	s.counter.WatchHold("LockLedgerAccounts")
	s.resolver = &perfResolver{ips: map[string]net.IP{}, delay: dnsDelay}
	for i := range channels {
		host := fmt.Sprintf("up-%d.perf.test", i)
		s.hosts = append(s.hosts, host)
		s.resolver.ips[host] = net.IPv4(93, 184, 216, byte(34+i))
	}
	var err error
	if s.pki, err = gatewaybench.NewPKI(s.hosts...); err != nil {
		t.Fatal(err)
	}
	s.upstream = gatewaybench.NewUpstream(gatewaybench.DefaultProfile())
	withLabel("upstream", func() { s.server, err = gatewaybench.Serve(s.upstream, s.pki.ServerConfig()) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.server.Close() })

	policy, err := channel.NewOutboundPolicyWithResolver([]string{s.server.Port()}, nil, s.resolver)
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]bool{}
	for _, ip := range s.resolver.ips {
		pinned[ip.String()] = true
	}
	pointAtLoopback(t, policy, s.pki.Roots, func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !pinned[host] || port != s.server.Port() {
			return nil, fmt.Errorf("the policy pinned %q, which is not one of the baseline's upstream addresses", address)
		}
		s.dials.Add(1)
		return (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}).DialContext(ctx, network, s.server.Address())
	})

	var store *storepg.Store
	s.pool, store = perfDatabase(t, s.counter)
	withLabel("gateway", func() {
		quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
		s.e = newGroupEnvOn(t, s.pool, store, func(d *gateway.Dependencies) {
			d.Outbound = policy
			d.Logger = quiet
		})
		// What cmd/server runs next to the engine. The refresh is marked so
		// that its queries are told apart from those of requests.
		background, stop := context.WithCancel(gatewaybench.Background(context.Background()))
		t.Cleanup(stop)
		go s.e.engine.RunRefresh(background)
		go s.e.engine.RunReaper(background, time.Minute)
		go s.e.cached.Cache.Run(background)

		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatal(listenErr)
		}
		// The server's http.Server settings (cmd/server/main.go).
		front := &http.Server{
			Handler: s.e.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second,
			WriteTimeout: 0, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
		}
		go func() { _ = front.Serve(listener) }()
		t.Cleanup(func() { _ = front.Close() })
		s.frontURL = "http://" + listener.Addr().String()
	})
	s.gateway = gatewaybench.NewClient(gatewaybench.ClientConfig{})
	s.direct = gatewaybench.NewClient(gatewaybench.ClientConfig{
		RootCAs: s.pki.Roots,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, s.server.Address())
		},
	})
	t.Cleanup(func() {
		s.gateway.CloseIdleConnections()
		s.direct.CloseIdleConnections()
	})
	s.populate(channels, consumers)
	return s
}

// upstreamURL is the base URL of the i-th channel's upstream.
func (s *perfStack) upstreamURL(i int) string {
	return "https://" + s.hosts[i] + ":" + s.server.Port()
}

func (s *perfStack) populate(channels, consumers int) {
	e := s.e
	started := time.Now()
	for i := range channels {
		sharer := e.member(fmt.Sprintf("sharer%d", i), "0")
		s.channelIDs = append(s.channelIDs, e.createChannel(sharer, channelSpec{name: fmt.Sprintf("channel%d", i), baseURL: s.upstreamURL(i), multiplier: "1"}))
		// The synthetic upstream supports response continuation. Explicitly
		// declare that fixture capability, as the ordinary integration test does;
		// production routing must keep rejecting unproven stateful requests.
		e.declareGroupedCapabilities(s.channelIDs[i], "upstream_protocol/openai-responses.state/previous_response_id_three_turns")
	}
	for i := range consumers {
		consumer := e.member(fmt.Sprintf("consumer%d", i), "1000000")
		_, secret := e.defaultKey(consumer)
		budget := e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{
			"name": "budget", "budget_daily": "100000", "budget_monthly": "100000", "budget_total": "100000",
		})
		s.keys = append(s.keys, secret)
		s.budgetKeys = append(s.budgetKeys, budget["secret"].(string))
	}
	// A production database has had its statistics gathered; a schema created a
	// moment ago has not, and the planner would pick plans for empty tables.
	if _, err := s.pool.Exec(context.Background(), `ANALYZE`); err != nil {
		s.t.Fatalf("analyze: %v", err)
	}
	s.t.Logf("populated %d channels and %d consumers in %s", channels, consumers, time.Since(started).Round(time.Millisecond))
}

// databaseProbe is how fast the database answers from this process: the round
// trip of the smallest statement, and the commit of a one-row insert. The
// gateway sends about twenty statements per request, a few of them commits, so
// these two numbers explain much of what a run shows and differ a lot between
// machines.
type databaseProbe struct {
	RoundTripMean, RoundTripP99 time.Duration
	CommitMean, CommitP99       time.Duration
}

func (s *perfStack) probeDatabase() databaseProbe {
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS perf_probe (id int)`); err != nil {
		s.t.Fatal(err)
	}
	measure := func(n int, statement string) gatewaybench.Distribution {
		samples := make([]time.Duration, 0, n)
		for range n {
			started := time.Now()
			if _, err := s.pool.Exec(ctx, statement); err != nil {
				s.t.Fatal(err)
			}
			samples = append(samples, time.Since(started))
		}
		return gatewaybench.NewDistribution(samples)
	}
	roundTrip, commit := measure(1000, `SELECT 1`), measure(300, `INSERT INTO perf_probe VALUES (1)`)
	return databaseProbe{
		RoundTripMean: roundTrip.Mean(), RoundTripP99: roundTrip.Percentile(99),
		CommitMean: commit.Mean(), CommitP99: commit.Percentile(99),
	}
}

// waitChannelsAvailable holds a run back until no channel is cooling down. A
// gateway that is overloaded times out on answers that are merely late, counts
// them as failures of the channels, and cools them down; the next run would
// then meet a gateway with no channel to offer, and measure that.
func (s *perfStack) waitChannelsAvailable() {
	deadline, waited := time.Now().Add(5*time.Minute), false
	for time.Now().Before(deadline) {
		cooling := false
		for _, id := range s.channelIDs {
			if !s.e.runtime.CooldownUntil(id, time.Now()).IsZero() {
				cooling = true
			}
		}
		if !cooling {
			break
		}
		waited = true
		time.Sleep(2 * time.Second)
	}
	if waited {
		s.t.Logf("  waited for the channels to come out of their cooldown")
	}
}
