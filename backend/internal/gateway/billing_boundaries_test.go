package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type billingRestrictedStore struct{ *serveStore }

func (s billingRestrictedStore) LookupKey(context.Context, []byte) (KeyAuth, error) {
	return KeyAuth{KeyID: "key-1", OwnerID: "account-1", Enabled: true, AccountActive: true, AllowedModels: []string{"gpt-test"}}, nil
}

type unavailableSubmodels struct {
	oneModel
	lists *int
}

func (s unavailableSubmodels) ListModels(context.Context, bool) ([]catalog.Model, error) {
	*s.lists++
	return nil, errors.New("synthetic catalog failure")
}

type billingRoutingStop struct {
	routing.Store
	reached bool
}

func (s *billingRoutingStop) Resolve(context.Context, string, string, string) (routing.Pref, error) {
	s.reached = true
	return routing.Pref{}, errors.New("stop after admission")
}

func TestSubmodelsNeverBecomeAdmissionRules(t *testing.T) {
	for _, tools := range []string{
		`[{"type":"image_generation"}]`,
		`[{"type":"image_generation","model":"other-model"}]`,
		`[{"type":"advisor_20260301","model":"other-model"}]`,
		"[" + strings.Repeat(" ", 1<<20) + `{"type":"image_generation","model":"other-model"}]`,
	} {
		for _, mainModel := range []string{"gpt-test", "forbidden-main"} {
			routes := &billingRoutingStop{}
			f := newServeFixture(t, 2, routes)
			f.engine.Store = billingRestrictedStore{f.store}
			lists := 0
			f.engine.Catalog = catalog.NewService(unavailableSubmodels{lists: &lists})
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":%q,"tools":%s}`, mainModel, tools)))
			r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
			w := httptest.NewRecorder()
			f.engine.Serve(w, r, channel.FormatOpenAIResponses, "", false)
			want := http.StatusInternalServerError // admitted and reached stoppingRouting
			if mainModel == "forbidden-main" {
				want = http.StatusForbidden
			}
			if w.Code != want || strings.Contains(w.Body.String(), "子模型") {
				t.Fatalf("model=%s tools length=%d: status=%d want=%d body=%s", mainModel, len(tools), w.Code, want, w.Body.String())
			}
			if lists != 0 {
				t.Fatal("tool configuration added a directory read on the inference path")
			}
			if routes.reached != (mainModel == "gpt-test") {
				t.Fatal("submodel pricing failure blocked admission, or main model restriction was bypassed")
			}
		}
	}
}

func TestGeminiToolInputsNeedPositiveRequestEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, tools string
		want        int64
	}{
		{"no declaration", `[]`, 100},
		{"code", `[{"codeExecution":{}}]`, 130},
		{"url", `[{"url_context":{}}]`, 130},
		{"file search", `[{"fileSearch":{"fileSearchStoreNames":["synthetic"]}}]`, 130},
		{"search", `[{"googleSearch":{}}]`, 100},
		{"maps", `[{"googleMaps":{}}]`, 100},
		{"mixed", `[{"urlContext":{}},{"googleSearch":{}}]`, 100},
		{"mixed in one definition", `[{"urlContext":{},"googleSearch":{}}]`, 100},
		{"unknown", `[{"futureTool":{}}]`, 100},
		{"null configuration", `[{"codeExecution":null}]`, 100},
		{"oversized", "[" + strings.Repeat(" ", 1<<20) + `{"codeExecution":{}}]`, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"tools":` + tc.tools + `}`)
			_, scan, _ := ScanRequest(body)
			hints := scan.billingHints(body, channel.FormatGemini)
			o := NewObserver(channel.FormatGemini, true, "text/event-stream")
			o.geminiToolInputBillable = hints.GeminiToolInputBillable
			frame := "data: " + `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"toolUsePromptTokenCount":30}}` + "\n\n"
			o.Write([]byte(frame+frame), time.Now())
			got := o.Finish()
			if got.Usage.InputTokens != tc.want || got.Usage.Detail.ToolPromptTokens != 30 {
				t.Fatalf("input=%d want=%d detail=%+v", got.Usage.InputTokens, tc.want, got.Usage.Detail)
			}
			if tc.want == 130 {
				o.Write([]byte("data: "+`{"candidates":[{"groundingMetadata":{"webSearchQueries":["private-search-text"]}}]}`+"\n\n"), time.Now())
				if got := o.Finish(); got.Usage.InputTokens != 100 {
					t.Fatal("later contradictory grounding did not remove extra input")
				}
			}
		})
	}
}

func TestImageSubmodelWithoutGroupQuoteIsUnpriced(t *testing.T) {
	raw := `{"usage":{"input_tokens":100,"output_tokens":20},"output":[{"id":"ig_one","type":"image_generation_call","model":"image-model","usage":{"input_tokens":1000000,"output_tokens":1000000}}]}`
	got := observe(channel.FormatOpenAIResponses, false, "application/json", raw, 7)
	model := catalog.Model{ID: "main", InputPrice: 1_000_000_000, OutputPrice: 5_000_000_000}
	call := &callState{format: channel.FormatOpenAIResponses, billingFacts: quotepricing.Facts{MainReliable: true}}
	priced, snapshot, err := NewEngine(Dependencies{}).priceUsage(call, model, quotedCandidate(t, Candidate{MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}, model), got.Usage, settings.Settings{}, 0)
	if err != nil || priced.Cost.String() != "0.0002" || !strings.Contains(string(snapshot), "missing_group_quote") || got.Usage.Detail.Units["image_generation"] != ledger.FixedPointScale {
		t.Fatalf("cost=%s err=%v snapshot=%s", priced.Cost.String(), err, snapshot)
	}
}

func TestBillingHintsDoNotRetainArbitraryToolContent(t *testing.T) {
	body := []byte(`{"tools":[{"type":"image_generation","model":"private prompt text"},{"type":"advisor_20260301","model":"private advisor text"},{"type":"code_interpreter","container":{"type":"auto","memory_limit":"private container text"}}]}`)
	_, scan, _ := ScanRequest(body)
	hints := scan.billingHints(body, channel.FormatOpenAIResponses)
	raw, err := json.Marshal(hints)
	if err != nil || strings.Contains(string(raw), "private") || hints.ImageModel != "" || hints.ContainerMemory != "" || len(hints.Submodels) != 0 {
		t.Fatalf("unreliable tool configuration must forward without being retained: %s err=%v", raw, err)
	}
}

func TestRegionalEndpointRecognitionAndResponsePriority(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://us.api.openai.com", "us"},
		{"https://eu.api.openai.com/v1", "eu"},
		{"https://US.API.OPENAI.COM", "us"},
		{"https://us.api.openai.com.attacker.example", ""},
		{"https://relay.example/us.api.openai.com", ""},
		{"https://us.api.openai.com@relay.example", ""},
		{"https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google", "us-central1"},
		{"https://aiplatform.googleapis.com/v1/projects/p/locations/us-central1", ""},
		{"https://api.openai.com", ""},
	} {
		if got := endpointGeo(tc.url); got != tc.want {
			t.Errorf("%s: geo=%q want=%q", tc.url, got, tc.want)
		}
	}
	model := catalog.Model{ID: "main", InputPrice: 1_000_000_000}
	for _, tc := range []struct{ geo, endpoint, want string }{
		{"eu", "https://us.api.openai.com", "1"},
		{"", "https://us.api.openai.com", "1"},
		{"unknown", "https://us.api.openai.com", "1"},
		{"", "https://relay.example", "1"},
	} {
		u := ledger.Usage{InputTokens: 1_000_000, Detail: &ledger.UsageDetail{InferenceGeo: tc.geo}}
		priced, _, err := NewEngine(Dependencies{}).priceUsage(&callState{billingFacts: quotepricing.Facts{MainReliable: true}}, model, quotedCandidate(t, Candidate{BaseURL: tc.endpoint, MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}, model), u, settings.Settings{}, 0)
		if err != nil || priced.Cost.String() != tc.want {
			t.Fatalf("geo=%s endpoint=%s cost=%s want=%s err=%v", tc.geo, tc.endpoint, priced.Cost.String(), tc.want, err)
		}
	}
}

func TestObservedToolsAreBoundedDeduplicatedAndContainNoContent(t *testing.T) {
	items := []string{
		`{"id":"search","type":"web_search_call","query":"private-search-text"}`,
		`{"id":"search","type":"web_search_call"}`,
	}
	for i, memory := range []string{"1g", "4gb", "16g", "64gb"} {
		item := fmt.Sprintf(`{"id":"code-%d","type":"code_interpreter_call","container_id":"container-%d","memory_limit":%q,"code":"private-code"}`, i, i, memory)
		items = append(items, item, item, fmt.Sprintf(`{"id":"other-code-%d","type":"code_interpreter_call","container_id":"container-%d","memory_limit":%q}`, i, i, memory))
	}
	for i := 0; i < maxBillingItems+20; i++ {
		items = append(items, fmt.Sprintf(`{"id":"file-%d","type":"file_search_call"}`, i))
	}
	o := NewObserver(channel.FormatOpenAIResponses, false, "application/json")
	o.Write([]byte(`{"usage":{"input_tokens":100,"output_tokens":20},"output":[`+strings.Join(items, ",")+`]}`), time.Now())
	got := o.Finish()
	if len(o.billedItems) != maxBillingItems || got.Usage.Detail.Units["web_search"] != ledger.FixedPointScale {
		t.Fatalf("items=%d units=%v", len(o.billedItems), got.Usage.Detail.Units)
	}
	for _, memory := range []string{"1gb", "4gb", "16gb", "64gb"} {
		if got.Usage.Detail.Units["container_"+memory] != ledger.FixedPointScale {
			t.Fatalf("container counted more than once: %v", got.Usage.Detail.Units)
		}
	}
	raw, _ := json.Marshal(got.Usage.Detail)
	for _, forbidden := range []string{"private-search-text", "private-code", "container-0", "file-0"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("observation retained content or identifiers: %s", raw)
		}
	}
	if !strings.Contains(string(raw), "超过解析上限") || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 20 {
		t.Fatal("observation overflow changed model usage or omitted explanation")
	}
}

func TestLateSettlementCrossesDayButNotMonth(t *testing.T) {
	previous := time.Date(2026, 3, 10, 15, 59, 0, 0, time.UTC)
	now := previous.Add(2 * time.Minute)
	cache := NewSpendCache()
	load := func() (Spend, error) { return Spend{Today: 5, Month: 50, Total: 500}, nil }
	_, _ = cache.Get("key", now, load)
	cache.Add("key", 7, previous)
	got, _ := cache.Get("key", now, load)
	if got != (Spend{Today: 5, Month: 57, Total: 507}) {
		t.Fatalf("late settlement budget=%+v", got)
	}
}
