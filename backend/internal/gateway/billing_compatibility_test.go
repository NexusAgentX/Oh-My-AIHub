package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestBillingMultilineSSEAndCumulativeUsage(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		body := strings.Join([]string{": comment", "data: {\"usage\":", "data: {\"prompt_tokens\":100,\"completion_tokens\":20}}", "", "data: {\"usage\":{\"completion_tokens\":30}}", "", ""}, newline)
		for _, chunk := range []int{1, 7, 4096} {
			got := observe(channel.FormatOpenAIChat, true, "text/event-stream", body, chunk)
			if !got.Found || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 30 {
				t.Fatalf("newline=%q chunk=%d observation=%+v", newline, chunk, got)
			}
		}
	}
}

func TestBillingGeminiTrafficAndGrounding(t *testing.T) {
	for _, tc := range []struct {
		name, raw, tier string
		input           int64
		unit            string
	}{
		{"flex", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"trafficType":"ON_DEMAND_FLEX"}}`, "gemini:flex", 100, ""},
		{"actual service wins", `{"usageMetadata":{"promptTokenCount":100,"serviceTier":"STANDARD","trafficType":"ON_DEMAND_FLEX"}}`, "gemini:STANDARD", 100, ""},
		{"code", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"toolUsePromptTokenCount":30,"totalTokenCount":150}}`, "", 130, ""},
		{"search", `{"candidates":[{"groundingMetadata":{"webSearchQueries":["q"]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"toolUsePromptTokenCount":30}}`, "", 100, "google_search_query"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observe(channel.FormatGemini, false, "application/json", tc.raw, 7)
			if got.Usage.InputTokens != tc.input {
				t.Fatal(got.Usage)
			}
			if tc.tier != "" && (got.Usage.Detail == nil || got.Usage.Detail.ServiceTier != tc.tier) {
				t.Fatal(got)
			}
			if tc.unit != "" && got.Usage.Detail.Units[tc.unit] != ledger.FixedPointScale {
				t.Fatal(got)
			}
		})
	}
}

func TestBillingAdvisorAndPerIterationThreshold(t *testing.T) {
	model := catalog.Model{ID: "main", InputPrice: 1_000_000_000, OutputPrice: 5_000_000_000, CacheReadPrice: 100_000_000}
	child := catalog.Model{ID: "advisor", InputPrice: 5_000_000_000, OutputPrice: 25_000_000_000}
	engine := NewEngine(Dependencies{})
	call := &callState{format: channel.FormatAnthropic, submodels: []catalog.Model{child}}
	candidate := Candidate{UpstreamModel: "main-up", MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}
	raw := `{"usage":{"input_tokens":1760,"cache_read_input_tokens":412,"output_tokens":531,"iterations":[{"type":"message","input_tokens":412,"output_tokens":89},{"type":"advisor_message","model":"advisor","input_tokens":823,"output_tokens":1612},{"type":"message","input_tokens":1348,"cache_read_input_tokens":412,"output_tokens":442}]}}`
	got := observe(channel.FormatAnthropic, false, "application/json", raw, 7)
	priced, snapshot, err := engine.priceUsage(call, model, candidate, got.Usage, settings.Settings{}, 0)
	if err != nil || priced.Cost.String() != "0.0488712" || !strings.Contains(string(snapshot), `"model":"advisor"`) {
		t.Fatalf("cost=%+v err=%v snapshot=%s", priced, err, snapshot)
	}
	threshold := int64(100001)
	model.PriceTiers = []ledger.PriceTier{{MinPromptTokens: &threshold, InputPrice: 2_000_000_000, OutputPrice: 7_500_000_000}}
	got = observe(channel.FormatAnthropic, false, "application/json", `{"usage":{"iterations":[{"type":"compaction","input_tokens":80000,"output_tokens":3500},{"type":"message","input_tokens":23000,"output_tokens":1000}]}}`, 7)
	priced, _, err = engine.priceUsage(call, model, candidate, got.Usage, settings.Settings{}, 0)
	if err != nil || priced.Cost.String() != "0.1255" {
		t.Fatalf("incorrect aggregate threshold: %+v %v", priced, err)
	}
}

func TestBillingObservedToolsDoNotAddFees(t *testing.T) {
	usage := ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{InferenceGeo: "us", Units: map[string]int64{"web_search": 2 * ledger.FixedPointScale}}}
	prices := ledger.Prices{InputPerMillion: 1_000_000_000, GeoMultipliers: map[string]int64{"us": 1_100_000_000}}
	priced, err := ledger.CalculatePriceAt(usage, prices, nil, time.Time{}, ledger.FixedPointScale, ledger.UnitRate, 1_000_000)
	if err != nil || priced.Cost.String() != "1.1" || priced.Fee.String() != "0.0011" {
		t.Fatalf("unexpected tool surcharge: %+v %v", priced, err)
	}
}

// In-memory durable metadata store; the fake ledger closes calls once, like
// PostgreSQL's in_progress predicate and unique call idempotency key.
type backgroundFixture struct {
	saveErr error
	Store
	mu       sync.Mutex
	jobs     map[string]BackgroundJob
	finished map[string]CallFinish
	key      KeyAuth
}

func (s *backgroundFixture) SaveBackground(_ context.Context, j BackgroundJob) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.CallID] = j
	return nil
}
func (s *backgroundFixture) LookupBackground(_ context.Context, a, k, id string) (BackgroundJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.AccountID == a && j.KeyID == k && j.ResponseID == id {
			return j, nil
		}
	}
	return BackgroundJob{}, ErrBackgroundNotFound
}
func (s *backgroundFixture) PendingBackground(context.Context) ([]BackgroundJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jobs []BackgroundJob
	for _, j := range s.jobs {
		if !j.Settled {
			jobs = append(jobs, j)
		}
	}
	return jobs, nil
}
func (s *backgroundFixture) ClaimBackground(ctx context.Context, _ time.Time) ([]BackgroundJob, error) {
	return s.PendingBackground(ctx)
}
func (*backgroundFixture) RetryBackground(context.Context, string, time.Time) error { return nil }
func (s *backgroundFixture) LookupKey(context.Context, []byte) (KeyAuth, error)     { return s.key, nil }
func (s *backgroundFixture) FinishCall(_ context.Context, f CallFinish) (FinishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.finished[f.ID]; ok {
		return FinishResult{}, nil
	}
	s.finished[f.ID] = f
	j := s.jobs[f.ID]
	j.Settled = true
	s.jobs[f.ID] = j
	return FinishResult{Finished: true, TxID: "tx"}, nil
}

type backgroundOutbound struct {
	channel.Outbound
	client *http.Client
}

func (b backgroundOutbound) GatewayClient(context.Context, string) (*http.Client, error) {
	return b.client, nil
}

func TestBackgroundAckPersistsBeforeDeliveryAndBindsFollowups(t *testing.T) {
	fixture := &backgroundFixture{jobs: map[string]BackgroundJob{}, finished: map[string]CallFinish{}, key: KeyAuth{KeyID: "key", OwnerID: "consumer", Enabled: true, AccountActive: true}}
	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keyring.Encrypt("channel", 1, "original-secret")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original-secret" {
			t.Error("credential not pinned")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_job","object":"response","status":"completed","usage":{"input_tokens":100,"output_tokens":20}}`)
	}))
	defer upstream.Close()
	engine := NewEngine(Dependencies{Store: fixture, Jobs: fixture, Keyring: keyring})
	engine.settingsSnap.Store(&settingsSnapshot{outbound: backgroundOutbound{client: upstream.Client()}})
	now := time.Now()
	call := &callState{id: "call", started: now, key: fixture.key, format: channel.FormatOpenAIResponses, modelID: "main", hints: billingHints{Background: true}, attempts: []Attempt{{}}}
	engine.reserveBackground(call.id)
	release, _ := engine.inflight.Acquire(call.key.OwnerID, 20)
	call.releaseAccount = release
	candidate := Candidate{ChannelID: "channel", OwnerID: "seller", BaseURL: upstream.URL, Credential: sealed, MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}
	model := catalog.Model{ID: "main", InputPrice: 1_000_000_000, OutputPrice: 5_000_000_000}
	ack := `{"id":"resp_job","object":"response","background":true,"status":"queued","usage":null}`
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(""))}
	recorder := httptest.NewRecorder()
	engine.streamBack(recorder, call, model, candidate, attemptResult{response: response, first: []byte(ack), firstErr: io.EOF, upstream: context.Background(), cancel: func(error) {}, watch: watchClient(context.Background(), func(error) {}, drainWindow), started: now}, settings.Settings{})
	if recorder.Body.String() != ack || len(fixture.finished) != 0 || len(fixture.jobs) != 1 || engine.inflight.Count("consumer") != 1 {
		t.Fatalf("ack prematurely settled: calls=%v jobs=%v count=%d", fixture.finished, fixture.jobs, engine.inflight.Count("consumer"))
	}
	var job BackgroundJob
	for _, j := range fixture.jobs {
		job = j
	}
	if strings.Contains(string(job.Metadata), "original-secret") || strings.Contains(string(job.Metadata), "queued") {
		t.Fatal("plaintext or response body persisted")
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_job", nil)
	request.SetPathValue("responseID", "resp_job")
	request.Header.Set("Authorization", "Bearer sk-aih-test")
	denied := httptest.NewRecorder()
	fixture.key.KeyID = "other"
	engine.ServeResponse(denied, request, false)
	if denied.Code != 404 {
		t.Fatalf("cross-Key response access=%d", denied.Code)
	}
	fixture.key.KeyID = "key"
	restarted := NewEngine(Dependencies{Store: fixture, Jobs: fixture, Keyring: keyring})
	restarted.settingsSnap.Store(&settingsSnapshot{outbound: backgroundOutbound{client: upstream.Client()}})
	if err := restarted.RestoreBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarted.inflight.Count("consumer") != 1 {
		t.Fatal("pending reservation not restored")
	}
	restarted.pollBackgroundJob(context.Background(), job)
	restarted.pollBackgroundJob(context.Background(), job)
	engine.releaseBackground(call.id)
	if len(fixture.finished) != 1 || engine.inflight.Count("consumer") != 0 {
		t.Fatalf("not settled exactly once: %+v", fixture.finished)
	}
	finished := fixture.finished["call"]
	if finished.Cost.String() != "0.0002" || finished.Bill == nil {
		t.Fatalf("wrong final billing: %+v", finished)
	}
	restored := NewEngine(Dependencies{Jobs: fixture, Keyring: keyring})
	if err := restored.RestoreBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBillingImageToolRetainsModalityDetailsAndDeduplicatesItems(t *testing.T) {
	raw := `{"id":"resp_image","usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"ig_one","type":"image_generation_call","model":"image-model","usage":{"input_tokens":10,"output_tokens":30,"input_tokens_details":{"text_tokens":2,"image_tokens":8},"output_tokens_details":{"image_tokens":30}}},{"id":"ig_one","type":"image_generation_call","model":"image-model","usage":{"input_tokens":10,"output_tokens":30}}]}`
	got := observe(channel.FormatOpenAIResponses, false, "application/json", raw, 7)
	if got.Usage.Detail == nil || len(got.Usage.Detail.Iterations) != 1 {
		t.Fatalf("%+v", got.Usage)
	}
	child := got.Usage.Detail.Iterations[0].Usage
	if child.Detail == nil || child.Detail.Tokens["input_image"] != 8 || child.Detail.Tokens["output_image"] != 30 {
		t.Fatalf("child=%+v", child)
	}
}

func TestBillingReliableToolsWithoutTokenSummary(t *testing.T) {
	got := observe(channel.FormatOpenAIResponses, false, "application/json", `{"id":"resp_tools","output":[{"id":"ws_one","type":"web_search_call"}]}`, 7)
	if got.Found || got.Usage.Detail == nil || got.Usage.Detail.Units["web_search"] != ledger.FixedPointScale {
		t.Fatalf("%+v", got)
	}
}

func TestBackgroundDeadlineSettlesCancellationUsage(t *testing.T) {
	fixture := &backgroundFixture{jobs: map[string]BackgroundJob{}, finished: map[string]CallFinish{}}
	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := keyring.Encrypt("channel", 1, "frozen-secret")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses/resp_deadline/cancel" {
			t.Errorf("unexpected exchange %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_deadline","status":"cancelled","usage":{"input_tokens":100,"output_tokens":20}}`)
	}))
	defer upstream.Close()
	engine := NewEngine(Dependencies{Store: fixture, Jobs: fixture, Keyring: keyring})
	engine.settingsSnap.Store(&settingsSnapshot{outbound: backgroundOutbound{client: upstream.Client()}})
	metadata := backgroundMetadata{
		Started: time.Now().Add(-time.Hour), Key: KeyAuth{OwnerID: "consumer", KeyID: "key"},
		Candidate: Candidate{ChannelID: "channel", OwnerID: "seller", BaseURL: upstream.URL, Credential: credential, MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate},
		Model:     catalog.Model{ID: "main", InputPrice: 1_000_000_000, OutputPrice: 5_000_000_000}, ModelID: "main", Attempts: []Attempt{{}},
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	job := BackgroundJob{CallID: "deadline", ResponseID: "resp_deadline", AccountID: "consumer", ChannelID: "channel", Metadata: raw, Deadline: time.Now().Add(-time.Minute)}
	fixture.jobs[job.CallID] = job
	engine.pollBackgroundJob(context.Background(), job)
	got, exists := fixture.finished[job.CallID]
	if !exists || got.Cost.String() != "0.0002" || got.Bill == nil {
		t.Fatalf("cancel usage lost: %+v", got)
	}
}

func TestBackgroundRegistrationFailureCancelsProviderWork(t *testing.T) {
	fixture := &backgroundFixture{jobs: map[string]BackgroundJob{}, saveErr: errors.New("database unavailable")}
	keyring, err := channel.ParseKeyring("v1="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := keyring.Encrypt("channel", 1, "synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	var cancelled atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelled.Store(r.Method == http.MethodPost && r.URL.Path == "/v1/responses/resp_unregistered/cancel")
		io.WriteString(w, `{"status":"cancelled"}`)
	}))
	defer upstream.Close()
	engine := NewEngine(Dependencies{Jobs: fixture, Keyring: keyring})
	engine.settingsSnap.Store(&settingsSnapshot{outbound: backgroundOutbound{client: upstream.Client()}})
	call := &callState{id: "failed", started: time.Now()}
	engine.reserveBackground(call.id)
	err = engine.keepBackground(call, catalog.Model{}, Candidate{ChannelID: "channel", BaseURL: upstream.URL, Credential: credential}, settings.Settings{}, "resp_unregistered")
	if err == nil || !cancelled.Load() || call.pending || len(engine.jobs) != 0 {
		t.Fatalf("registration failure leaked work: %v cancelled=%v pending=%v", err, cancelled.Load(), call.pending)
	}
}
