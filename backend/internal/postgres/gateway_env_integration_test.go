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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/metrics"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
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

func (o testOutbound) GatewayClient(_ context.Context, _ string, total time.Duration) (*http.Client, error) {
	return o.clientWith(total), nil
}

func (o testOutbound) WithExtraBlockedHosts([]string) (channel.Outbound, error) { return o, nil }

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
	t       *testing.T
	pool    *pgxpool.Pool
	store   *storepg.Store
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

const (
	priceInput  = "10"
	priceOutput = "20"
)

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	service, admin, _ := accounts(t, store)

	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	keyring, err := channel.ParseKeyring("test="+base64.StdEncoding.EncodeToString(secret), "test")
	if err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(store.Catalog)
	settingsService := settings.NewService(store.Settings)
	for _, id := range []string{"gpt-test", "claude-test", "gemini-test"} {
		if _, err := catalogService.Create(ctx, admin.ID, catalog.Model{
			ID: id, DisplayName: strings.ToUpper(id), Enabled: true, Provider: "test",
			InputPrice: mustAmount(t, priceInput), OutputPrice: mustAmount(t, priceOutput),
			CacheWritePrice: mustAmount(t, "12.5"), CacheReadPrice: mustAmount(t, "1"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	modelIDs := func(ctx context.Context) ([]string, error) {
		models, err := catalogService.List(ctx, true)
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
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	outbound := testOutbound{client: &http.Client{Transport: testTransport}}
	e := &env{t: t, pool: pool, store: store, logs: logs, service: service, people: map[string]person{}}
	runtime := gateway.NewRuntime(func(channelID, kind, reason string) {
		_ = store.Gateway.RecordChannelEvent(context.Background(), channelID, kind, reason)
	})
	bus := gateway.NewBus()
	e.runtime = runtime
	e.engine = gateway.NewEngine(gateway.Dependencies{
		Store: store.Gateway, Catalog: catalogService, Settings: settingsService, Routing: store.Routes,
		Keyring: keyring, Outbound: outbound, Logger: logger, Runtime: runtime, Events: bus,
	})
	e.observe = observe.NewService(store.Observe)
	e.feed = observe.NewFeed(e.observe, bus, logger)
	e.metrics = metrics.New(func(id string, now time.Time) bool { return !runtime.CooldownUntil(id, now).IsZero() })
	e.metrics.SetModels([]string{"gpt-test", "claude-test", "gemini-test"})
	e.feed.AddObserver(e.metrics)
	feedContext, stopFeed := context.WithCancel(context.Background())
	t.Cleanup(stopFeed)
	go e.feed.Run(feedContext)
	e.handler = api.NewHandler(api.Dependencies{
		Identity: service, Catalog: catalogService, Ledger: ledger.NewService(store.Ledger), Settings: settingsService,
		Audit: audit.NewService(store.Audit), Keys: apikey.NewService(store.Keys, keyring, modelIDs),
		Channels: channel.NewService(channel.Dependencies{
			Store: store.Channels, Keyring: keyring, Outbound: outbound, KnownModels: modelIDs,
			BlockedHosts: func(ctx context.Context) ([]string, error) {
				value, err := settingsService.Get(ctx)
				return value.ExtraBlockedHosts, err
			},
		}),
		Routing: store.Routes, Gateway: e.engine, Browse: store.Gateway, Observe: e.observe, Feed: e.feed, C2C: c2c.NewService(store.C2C, c2cKeyring), CookieSecure: false,
	})
	e.admin = e.login(admin.Username, "Founder-password-2026", admin.ID, false)
	return e
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
	name       string
	upstream   *upstream
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
	body := map[string]any{"name": spec.name, "base_url": spec.upstream.server.URL, "api_key": spec.key, "models": []any{model}}
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
)

// okUpstream answers every inference request with the body fitting its path.
func okUpstream(t *testing.T) *upstream {
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
			_, _ = io.WriteString(w, responsesBody)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = io.WriteString(w, anthropicBody)
		default:
			_, _ = io.WriteString(w, geminiBody)
		}
	})
}
