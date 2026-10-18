package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func nativeTestSession(t *testing.T, base string) (*Registry, *Session, *Service) {
	t.Helper()
	manifest, err := gatewaycontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterNative(); err != nil {
		t.Fatal(err)
	}
	m := newMemoryStore()
	session := &Session{Target: Target{GroupID: "synthetic-group", Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: "synthetic-model"}, Model: "synthetic-model", ModelType: "text"}, Parameters: testParameters(), resolved: Resolved{Addresses: []string{base}}, secret: []byte("synthetic-private-detector-key"), outbound: fixtureOutbound{client: &http.Client{}}, endpoints: map[string]gatewaycontract.Endpoint{}, store: m, jobID: "synthetic-task", limits: DefaultLimits(), source: "synthetic"}
	for _, e := range manifest.Endpoints {
		session.endpoints[e.ID] = e
	}
	t.Cleanup(session.clear)
	return r, session, &Service{registry: r, store: m, source: "synthetic"}
}
func TestNativeScenarioSharingAndIndependentEvidence(t *testing.T) {
	for protocol, fixture := range nativeJSONFixtures {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				o, _ := decodeNative([]byte(fixture))
				expected := nativeMarker
				if strings.Contains(string(body), "SILVER-829") {
					expected = "SILVER-829"
				}
				switch protocol {
				case "openai-chat-completions":
					c := object(list(o["choices"])[0])
					object(c["message"])["content"] = expected
					if strings.Contains(string(body), "10000") {
						c["finish_reason"] = "length"
					}
				case "openai-responses":
					object(list(object(list(o["output"])[0])["content"])[0])["text"] = expected
					if strings.Contains(string(body), "10000") {
						o["status"] = "incomplete"
						o["incomplete_details"] = nativeObject{"reason": "max_output_tokens"}
					}
				case "native-messages":
					object(list(o["content"])[0])["text"] = expected
					if strings.Contains(string(body), "10000") {
						o["stop_reason"] = "max_tokens"
					}
				case "native-generate-content":
					c := object(list(o["candidates"])[0])
					object(list(object(c["content"])["parts"])[0])["text"] = expected
					if strings.Contains(string(body), "10000") {
						c["finishReason"] = "MAX_TOKENS"
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(o)
			}))
			defer up.Close()
			r, session, service := nativeTestSession(t, up.URL)
			plan, err := r.Plan(session.Target)
			if err != nil {
				t.Fatal(err)
			}
			if !HasPending(plan) {
				t.Fatal("unimplemented scenarios disappeared from full-test scope")
			}
			for _, family := range []string{"base", "json"} {
				var item Planned
				for _, p := range plan.Items {
					if slices.Contains(p.Requirements, protocol+"."+family) {
						item = p
						break
					}
				}
				result := service.run(context.Background(), session, item, nil)
				if result.State != "pass" || result.Source != "synthetic" {
					t.Fatalf("native fixture %s: %+v", family, result)
				}
				encoded, _ := json.Marshal(result)
				for _, secret := range []string{nativeMarker, "SILVER-829", "synthetic-private-detector-key"} {
					if strings.Contains(string(encoded), secret) {
						t.Fatal("scenario persisted body or credential")
					}
				}
			}
			want := 5
			if protocol == "openai-chat-completions" {
				want = 6
			}
			if protocol == "native-generate-content" {
				want = 4
			}
			if calls != want || session.usage.HTTP != int64(want) {
				t.Fatalf("exact basic request not reused: calls=%d usage=%+v", calls, session.usage)
			}
		})
	}
}

func TestNegativeAuthenticationCannotReuseConfiguredCredentials(t *testing.T) {
	for _, protocol := range []string{"openai-chat-completions", "native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			seen := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
				header := "Authorization"
				if protocol == "native-messages" {
					header = "x-api-key"
				} else if protocol == "native-generate-content" {
					header = "x-goog-api-key"
				}
				got := r.Header.Get(header)
				if seen == 1 && !strings.Contains(got, "aihub-invalid-") {
					t.Error("invalid auth probe did not replace secret")
				}
				if seen == 2 && got != "" {
					t.Error("absent auth probe restored secret")
				}
				for _, name := range []string{"key", "api_key", "access_token", "token"} {
					if r.URL.Query().Get(name) != "" {
						t.Error("query credential survived")
					}
				}
				if protocol == "native-messages" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("negative auth removed protocol version")
				}
				w.WriteHeader(401)
			}))
			defer up.Close()
			_, s, _ := nativeTestSession(t, up.URL)
			id, path, query := nativeEndpoint(protocol, "synthetic-model", false)
			s.allowed = map[string]bool{id: true}
			s.resolved.Headers.Set = []market.HeaderSet{{Name: "Authorization", Value: "Bearer synthetic-configured-secret"}, {Name: "x-api-key", Value: "synthetic-configured-secret"}, {Name: "x-goog-api-key", Value: "synthetic-configured-secret"}}
			if query == nil {
				query = map[string][]string{}
			}
			for _, name := range []string{"key", "api_key", "access_token", "token"} {
				query.Set(name, "synthetic-query-secret")
			}
			for _, auth := range []Authentication{AuthenticationInvalid, AuthenticationAbsent} {
				res, err := s.Do(context.Background(), Request{EndpointID: id, Path: path, Query: query, Category: "generate", Authentication: auth})
				if err != nil || res.Status != 401 {
					t.Fatalf("negative probe failed: %v", err)
				}
			}
		})
	}
}

func TestNativeOversizedInputRecordsProtectionWithoutAllocatingOrSending(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("oversized scene reached upstream") }))
	defer up.Close()
	_, s, _ := nativeTestSession(t, up.URL)
	s.allowed = map[string]bool{"responses.create": true}
	response, err := s.Do(context.Background(), Request{EndpointID: "responses.create", Path: "/v1/responses", Category: "generate", Oversized: true})
	if err != ErrBusy || response.AttemptID == "" {
		t.Fatalf("missing local protection witness: %+v %v", response, err)
	}
	attempts, err := s.store.Attempts(context.Background(), s.jobID, []string{response.AttemptID})
	if err != nil || len(attempts) != 1 || attempts[0].Sent || attempts[0].ResponseReceived || attempts[0].Reason != "body_limit" {
		t.Fatalf("invalid protection metadata: %+v %v", attempts, err)
	}
}

func TestProfilesIgnorePricesAndOrderButRequireEveryEnabledSelector(t *testing.T) {
	q := market.Quote{Tiers: []market.Tier{{Conditions: market.Conditions{ServiceTier: "priority"}}, {Conditions: market.Conditions{ServiceTier: "flex"}}}, UnitPrices: []market.UnitPrice{{Unit: "successful_image", ImageSize: "1024x1024", ImageQuality: "high", Price: 1}}}
	before := profileFor(q)
	q.Tiers[0], q.Tiers[1] = q.Tiers[1], q.Tiers[0]
	q.Tiers[0].Prices.Input = 99
	q.UnitPrices[0].Price = 0
	if !CompatibleProfile(before, profileFor(q)) {
		t.Fatal("price or order invalidated selector evidence")
	}
	q.UnitPrices = append(q.UnitPrices, market.UnitPrice{Unit: "successful_image", ImageSize: "1536x1024", ImageQuality: "high"})
	if CompatibleProfile(before, profileFor(q)) {
		t.Fatal("new image selector borrowed old report")
	}
	q.UnitPrices = q.UnitPrices[:1]
	q.Tiers = append(q.Tiers, market.Tier{Conditions: market.Conditions{ServiceTier: "new-tier"}})
	if CompatibleProfile(before, profileFor(q)) {
		t.Fatal("new service tier borrowed old report")
	}
}
