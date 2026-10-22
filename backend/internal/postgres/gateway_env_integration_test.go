package postgres_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/metrics"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/groupchannelpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// ---- a loopback stand-in for the pinned egress policy ----

// testOutbound lets the gateway reach httptest TLS servers on loopback, which
// the real policy refuses by design. Everything else (normalization shape,
// per-attempt clients without redirects) mirrors the production boundary.
type testOutbound struct{ client *http.Client }

// testRoots trusts every httptest upstream started by the tests.
var testRoots = x509.NewCertPool()

var testTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: testRoots}, DisableKeepAlives: true}

func (o testOutbound) NormalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", channel.ErrInvalidInput
	}
	return strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/"), nil
}

func (o testOutbound) ValidateBaseURL(_ context.Context, raw string) (string, error) {
	return o.NormalizeBaseURL(raw)
}

func (o testOutbound) clientWith(total time.Duration) *http.Client {
	copied := *o.client
	copied.Timeout = total
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copied
}

func (o testOutbound) Client(_ context.Context, _ string, total time.Duration) (*http.Client, error) {
	return o.clientWith(total), nil
}

// GatewayClient has no deadline of its own, like the production client: the
// gateway bounds every exchange through the request context.
func (o testOutbound) GatewayClient(_ context.Context, _ string) (*http.Client, error) {
	return o.clientWith(0), nil
}

func (o testOutbound) FreshClient(_ context.Context, _ string) (*http.Client, error) {
	return o.clientWith(0), nil
}

func (o testOutbound) WithExtraBlockedHosts([]string) (channel.Outbound, error) { return o, nil }

// slowCloseConn closes slowly. The gateway's transport cancels an attempt by
// closing its connection, and closing a TLS connection first sends the peer
// close_notify and only then closes the socket. A peer that is waiting for
// that (a stalled upstream handler waits for its request to be cancelled)
// finishes its chunked body at once, and the transport's reader, still blocked
// on the open socket, gets those last bytes and a clean io.EOF instead of an
// error. On a loaded machine that window opens by itself now and then; this
// connection holds it open on purpose, so the tests that cancel an attempt can
// rely on it being there.
type slowCloseConn struct{ net.Conn }

func (c slowCloseConn) Close() error {
	time.Sleep(100 * time.Millisecond)
	return c.Conn.Close()
}

// slowCloseOutbound makes the gateway reach its upstreams over slowCloseConn.
func slowCloseOutbound(d *gateway.Dependencies) {
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: testRoots},
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return slowCloseConn{conn}, nil
		},
	}
	d.Outbound = testOutbound{client: &http.Client{Transport: transport}}
}

// withDependencies applies several adjustments to the gateway's dependencies.
func withDependencies(adjust ...func(*gateway.Dependencies)) func(*gateway.Dependencies) {
	return func(d *gateway.Dependencies) {
		for _, apply := range adjust {
			apply(d)
		}
	}
}

// ---- fake upstream relays ----

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

type upstream struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	records []recordedRequest
	handler func(w http.ResponseWriter, r *http.Request, body []byte)
}

func newUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte)) *upstream {
	t.Helper()
	u := &upstream{t: t, handler: handler}
	u.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.records = append(u.records, recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
		u.mu.Unlock()
		u.handler(w, r, body)
	}))
	testRoots.AddCert(u.server.Certificate())
	t.Cleanup(u.server.Close)
	return u
}

func (u *upstream) requests() []recordedRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]recordedRequest(nil), u.records...)
}

func (u *upstream) inference() []recordedRequest {
	var result []recordedRequest
	for _, record := range u.requests() {
		if record.Method == http.MethodPost {
			result = append(result, record)
		}
	}
	return result
}

// ---- the environment ----

type person struct {
	id       string
	username string
	token    string
}

type env struct {
	groups     *groupchannel.Runtime
	groupStore *groupchannelpg.Store
	groupPorts *groupLoopbackPorts
	t          *testing.T
	pool       *pgxpool.Pool
	store      *storepg.Store
	// cached is what the gateway and the services run on (ADR-0040); a test
	// that changes cached data goes through the env's services, or through
	// cached.Cache.Invalidate after writing SQL itself.
	cached *storepg.Cached

	handler http.Handler
	engine  *gateway.Engine
	admin   person
	people  map[string]person
	logs    *syncBuffer
	service *identity.Service
	observe *observe.Service
	feed    *observe.Feed
	metrics *metrics.Metrics
	runtime *gateway.Runtime
	keyring *channel.Keyring

	// engineDeps and newHandler are what restartGateway rebuilds the engine
	// and the HTTP handler from.
	engineDeps gateway.Dependencies
	newHandler func(*gateway.Engine) http.Handler
}

type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func newGroupEnvOn(t *testing.T, pool *pgxpool.Pool, store *storepg.Store, configure func(*gateway.Dependencies)) *env {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	// Like the server, run the gateway on the cached stores and build every
	// service that writes the cached data on them (ADR-0040).
	var groups *groupchannel.Runtime
	var groupedPrices *groupIntegrationPriceSource
	var repo *groupchannelpg.Store
	var options []func(*snapshot.Config)

	repo = groupchannelpg.NewStore(pool)
	groupedPrices = &groupIntegrationPriceSource{}
	groups = &groupchannel.Runtime{Repository: repo}
	options = append(options, func(c *snapshot.Config) {
		c.Loader = groupIntegrationEvidence{Loader: groups.Wrap(c.Loader), Prices: groupedPrices}
	})

	cached := store.Cached(logger, options...)

	groups.Rebuild = func(ctx context.Context) error {
		return cached.Cache.InvalidateChecked(ctx, snapshot.DomainModels|snapshot.DomainFleet)
	}

	service, admin, _ := accountsOn(t, cached.Identity)

	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	keyring, err := channel.ParseKeyring("test="+base64.StdEncoding.EncodeToString(secret), "test")
	if err != nil {
		t.Fatal(err)
	}

	settingsService := settings.NewService(store.Settings)

	modelIDs := func(ctx context.Context) ([]string, error) {
		models, err := cached.Models.List(ctx, true)
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids, err
	}

	c2cSecret := make([]byte, 32)
	_, _ = rand.Read(c2cSecret)
	c2cKeyring, err := c2c.ParseKeyring("k1="+base64.StdEncoding.EncodeToString(c2cSecret), "k1")
	if err != nil {
		t.Fatal(err)
	}
	outbound := testOutbound{client: &http.Client{Transport: testTransport}}
	e := &env{t: t, pool: pool, store: store, cached: cached, logs: logs, service: service, people: map[string]person{}, keyring: keyring}
	e.groups, e.groupStore = groups, repo

	groups.Removed = keyring.ForgetCredential

	runtime := gateway.NewRuntime(func(channelID, kind, reason string) {
		_ = store.Gateway.RecordChannelEvent(context.Background(), channelID, kind, reason)
	})
	bus := gateway.NewBus()
	e.runtime = runtime
	e.metrics = metrics.New(func(id string, now time.Time) bool { return !runtime.CooldownUntil(id, now).IsZero() })
	e.metrics.SetModels([]string{"gpt-test", "claude-test", "gemini-test"})
	dependencies := gateway.Dependencies{
		Store: cached.Gateway, Catalog: cached.Models, Settings: settingsService, Routing: cached.Routing,
		Keyring: keyring, Outbound: outbound, Logger: logger, Runtime: runtime, Events: gateway.Publishers{bus, e.metrics}, Telemetry: e.metrics,
	}

	dependencies.TypicalPrices = groupedPrices.View
	if configure != nil {
		configure(&dependencies)
	}

	dependencies.Outbound, e.groupPorts = groupFixtureOutbound(dependencies.Outbound)

	dependencies.Ready = groups.Check

	e.engineDeps = dependencies
	e.engine = gateway.NewEngine(dependencies)

	groups.Edited = e.engine.ChannelEdited

	e.observe = observe.NewService(store.Observe)
	// Rows are stamped by the database's clock and the observation windows end
	// at the service's "now". Keep both on the database's clock: with the host's,
	// a PostgreSQL clock that runs a few milliseconds ahead (a container or VM
	// on a laptop) puts the newest fixture rows after the end of the window.
	e.observe.SetClock(e.databaseNow)
	e.feed = observe.NewFeed(e.observe, bus, logger)
	feedContext, stopFeed := context.WithCancel(context.Background())
	t.Cleanup(stopFeed)
	go e.feed.Run(feedContext)
	diagnosticContext, stopDiagnostics := context.WithCancel(gatewaybench.Background(context.Background()))
	diagnosticDone := make(chan struct{})
	go func() { defer close(diagnosticDone); store.Gateway.RunDiagnostics(diagnosticContext) }()
	t.Cleanup(func() { stopDiagnostics(); <-diagnosticDone })
	e.newHandler = func(engine *gateway.Engine) http.Handler {
		d := api.Dependencies{
			Identity: service, Ledger: ledger.NewService(store.Ledger), Settings: settingsService,
			Audit: audit.NewService(store.Audit), Keys: apikey.NewService(cached.Keys, keyring, modelIDs),

			Routing: cached.Routing, Gateway: engine, Browse: store.Gateway, Observe: e.observe, Feed: e.feed, C2C: c2c.NewService(store.C2C, c2cKeyring), CookieSecure: false,
		}

		d.GroupChannels = &groupchannel.Service{Runtime: groups, Repository: repo, Keyring: keyring}
		return api.NewHandler(d)
	}
	e.handler = e.newHandler(e.engine)
	e.admin = e.login(admin.Username, "Founder-password-2026", admin.ID, false)

	e.seedGroupModelIDs()

	return e
}

// databaseNow is the current time on the PostgreSQL server, the clock that
// stamps created_at and completed_at. It stands in for the host's time.Now
// wherever a test compares fixture rows with "now". Without a reachable
// database it falls back to the host's clock: every other query fails too.
func (e *env) databaseNow() time.Time {
	var now time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Now()
	}
	return now
}

// restartGateway replaces the engine and the handler with new ones over the same
// database, the way a restart of the server would: the channel statistics start
// empty and are loaded from the calls table (LoadStats). Cooldowns and other
// runtime state are kept, which these tests do not depend on.
func (e *env) restartGateway() {
	e.t.Helper()
	dependencies := e.engineDeps
	dependencies.StatsBook = nil
	engine := gateway.NewEngine(dependencies)
	if e.groups != nil {
		e.groups.Edited = engine.ChannelEdited
	}
	if err := engine.LoadStats(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	e.engine, e.handler = engine, e.newHandler(engine)
}

func (e *env) login(username, password, id string, changePassword bool) person {
	e.t.Helper()
	result, err := e.service.Login(context.Background(), username, password)
	if err != nil {
		e.t.Fatalf("login %s: %v", username, err)
	}
	token := result.SessionToken
	if changePassword {
		changed, err := e.service.ChangePassword(context.Background(), id, password, "New-password-2026-"+username)
		if err != nil {
			e.t.Fatalf("change password %s: %v", username, err)
		}
		token = changed.SessionToken
	}
	return person{id: id, username: username, token: token}
}

// member creates a ready account with the given credit limit.
func (e *env) member(name, credit string) person {
	e.t.Helper()
	limit := mustAmount(e.t, credit)
	adminAccount, err := e.service.GetAccount(context.Background(), identity.Account{IsAdmin: true}, e.admin.id)
	if err != nil {
		e.t.Fatal(err)
	}
	created, err := e.service.CreateInvitedAccount(context.Background(), adminAccount.Account, name, name, &limit, false)
	if err != nil {
		e.t.Fatal(err)
	}
	p := e.login(name, created.InitialPassword, created.Account.ID, true)
	e.people[name] = p
	return p
}

// api performs a session-authenticated request and returns the recorder.
func (e *env) api(p person, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(&http.Cookie{Name: "oma_session", Value: p.token})
	recorder := httptest.NewRecorder()
	e.handler.ServeHTTP(recorder, request)
	assertContract(e.t, method, path, recorder)
	return recorder
}

func (e *env) apiJSON(p person, status int, method, path string, body any) map[string]any {
	e.t.Helper()
	recorder := e.api(p, method, path, body)
	if recorder.Code != status {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, recorder.Code, status, recorder.Body.String())
	}
	if recorder.Body.Len() == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		e.t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return decoded
}

// defaultKey returns the full platform key of a person (creating it lazily).
func (e *env) defaultKey(p person) (id, secret string) {
	e.t.Helper()
	home := e.apiJSON(p, http.StatusOK, http.MethodGet, "/api/home", nil)
	key := home["default_key"].(map[string]any)
	id = key["id"].(string)
	secret = e.apiJSON(p, http.StatusOK, http.MethodGet, "/api/keys/"+id+"/secret", nil)["secret"].(string)
	return id, secret
}

type channelSpec struct {
	name     string
	upstream *upstream
	// baseURL, when set, is used in place of the fake upstream's address; the
	// performance baseline points channels at host names of its own.
	supplierID string
	baseURL    string
	key        string
	model      string
	upstreamAs string
	multiplier string
	formats    []string
	advanced   map[string]any
	status     string
}

func (e *env) createChannel(owner person, spec channelSpec) string {
	e.t.Helper()
	if e.groups != nil {
		return e.createGroupedChannel(owner, spec)
	}
	if spec.model == "" {
		spec.model = "gpt-test"
	}
	if spec.multiplier == "" {
		spec.multiplier = "2"
	}
	if spec.formats == nil {
		spec.formats = []string{"openai_chat", "openai_responses", "anthropic", "gemini"}
	}
	if spec.key == "" {
		spec.key = "upstream-key-" + spec.name
	}
	model := map[string]any{"model_id": spec.model, "multiplier": spec.multiplier, "formats": spec.formats}
	if spec.upstreamAs != "" {
		model["upstream_model"] = spec.upstreamAs
	}
	baseURL := spec.baseURL
	if baseURL == "" {
		baseURL = spec.upstream.server.URL
	}
	body := map[string]any{"name": spec.name, "base_url": baseURL, "api_key": spec.key, "models": []any{model}}
	if spec.advanced != nil {
		body["advanced"] = spec.advanced
	}
	if spec.status != "" {
		body["status"] = spec.status
	}
	created := e.apiJSON(owner, http.StatusCreated, http.MethodPost, "/api/channels", body)
	return created["channel"].(map[string]any)["id"].(string)
}

// call sends a gateway request and returns the recorder.
func (e *env) call(secret, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	e.handler.ServeHTTP(recorder, request)
	return recorder
}

type callRow struct {
	Outcome       string
	ModelID       string
	Input, Output int64
	CostNano      int64
	FeeNano       int64
	HasLedgerTx   bool
	Attempts      int
	FinalChannel  string
	ResponseBytes int64
}

func (e *env) callRow(id string) callRow {
	e.t.Helper()
	var row callRow
	var modelID, channelID *string
	var bytesRead *int64
	err := e.pool.QueryRow(context.Background(), `
		SELECT outcome, model_id, input_tokens, output_tokens, cost_nano, fee_nano, ledger_tx_id IS NOT NULL,
		       jsonb_array_length(attempts), final_channel_id::text, response_bytes
		FROM calls WHERE id = $1`, id).Scan(&row.Outcome, &modelID, &row.Input, &row.Output, &row.CostNano, &row.FeeNano, &row.HasLedgerTx, &row.Attempts, &channelID, &bytesRead)
	if err != nil {
		e.t.Fatalf("call %s: %v", id, err)
	}
	if modelID != nil {
		row.ModelID = *modelID
	}
	if channelID != nil {
		row.FinalChannel = *channelID
	}
	if bytesRead != nil {
		row.ResponseBytes = *bytesRead
	}
	return row
}

func (e *env) balance(p person) string {
	e.t.Helper()
	points, err := e.store.Ledger.Points(context.Background(), p.id)
	if err != nil {
		e.t.Fatal(err)
	}
	return points.Balance.String()
}

func (e *env) systemBalance(code string) string {
	e.t.Helper()
	var nano int64
	if err := e.pool.QueryRow(context.Background(), `SELECT balance_nano FROM ledger_accounts WHERE system_code = $1`, code).Scan(&nano); err != nil {
		e.t.Fatal(err)
	}
	return fmt.Sprintf("%d", nano)
}

func (e *env) assertZeroSum() {
	e.t.Helper()
	var total int64
	if err := e.pool.QueryRow(context.Background(), `SELECT coalesce(sum(balance_nano), 0) FROM ledger_accounts`).Scan(&total); err != nil {
		e.t.Fatal(err)
	}
	if total != 0 {
		e.t.Fatalf("ledger does not sum to zero: %d", total)
	}
}

func (e *env) channelEvents(channelID string) []string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT kind FROM channel_events WHERE channel_id = $1 ORDER BY id`, channelID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		_ = rows.Scan(&kind)
		kinds = append(kinds, kind)
	}
	return kinds
}

// ---- canned upstream answers ----

const (
	chatBody       = `{"id":"c1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500}}`
	responsesBody  = `{"id":"resp_1","object":"response","usage":{"input_tokens":1000,"output_tokens":500}}`
	anthropicBody  = `{"id":"m1","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1000,"output_tokens":500}}`
	geminiBody     = `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":500}}`
	noUsageBody    = `{"id":"c9","choices":[{"message":{"content":"hi"}}]}`
	chatStreamBody = "data: {\"choices\":[{\"delta\":{\"content\":\"h\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\ndata: [DONE]\n\n"
	// What a client that did not ask for usage receives of chatStreamBody: the
	// gateway turned include_usage on for it and keeps the usage-only frame back.
	chatStreamBodyWithoutUsageFrame = "data: {\"choices\":[{\"delta\":{\"content\":\"h\"}}],\"usage\":null}\n\ndata: [DONE]\n\n"
)

// okUpstream answers every inference request with the body fitting its path.
func okUpstream(t *testing.T) *upstream {
	var responseSerial atomic.Uint64
	return newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			if bytes.Contains(body, []byte(`"stream":true`)) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, chatStreamBody)
				return
			}
			_, _ = io.WriteString(w, chatBody)
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = io.WriteString(w, strings.Replace(responsesBody, "resp_1", fmt.Sprintf("resp_%d", responseSerial.Add(1)), 1))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = io.WriteString(w, anthropicBody)
		default:
			_, _ = io.WriteString(w, geminiBody)
		}
	})
}
