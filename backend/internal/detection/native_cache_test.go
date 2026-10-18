package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeImplicitCacheAndEachEnabledTierUseIndependentActualFacts(t *testing.T) {
	for _, protocol := range []string{"openai-chat-completions", "openai-responses", "native-messages"} {
		t.Run(protocol, func(t *testing.T) {
			seen := map[string]int{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body, _ := decodeNative(raw)
				option := str(body["prompt_cache_retention"])
				if protocol == "native-messages" {
					system := list(body["system"])
					if len(system) > 0 {
						option = str(object(object(system[0])["cache_control"])["ttl"])
					}
				}
				text := nativeMarker
				if option != "" {
					text = replayCode
					seen[option]++
				}
				o := nativeFixtureWithCalls(protocol, text, nil)
				usage := object(o["usage"])
				if option != "" {
					read := 0
					if seen[option] > 1 {
						read = 8
					}
					if protocol == "native-messages" {
						usage["cache_creation_input_tokens"] = 0
						if read == 0 {
							usage["cache_creation_input_tokens"] = 8
						}
						usage["cache_read_input_tokens"] = read
					} else {
						key := "prompt_tokens_details"
						if protocol == "openai-responses" {
							key = "input_tokens_details"
						}
						usage[key] = nativeObject{"cached_tokens": read}
					}
				} else {
					tier := str(body["service_tier"])
					if protocol == "native-messages" {
						if tier == "standard_only" {
							usage["service_tier"] = "standard"
						} else if tier == "auto" {
							usage["service_tier"] = "priority"
						} else {
							t.Error("invented Messages tier request")
						}
					} else {
						o["service_tier"] = tier
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(o)
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			extra := "openai:fast"
			if protocol == "native-messages" {
				extra = "anthropic:priority"
			}
			s.Target.Profile.ServiceTiers = []string{extra, "gemini:flex"}
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+".cache_tier"), nil)
			if result.State != "pass" || len(result.Settings) != 4 || s.usage.HTTP != 6 {
				t.Fatalf("cache/tier facts collapsed or extra Cartesian calls: %+v", result)
			}
			for _, e := range result.Settings {
				if e.Kind == "cache" && (e.CacheReadTokens != 8 || !e.UsageReliable) {
					t.Fatal("cache read not independently observed")
				}
				if e.Configured == "anthropic:priority" && (e.Wire != "auto" || e.Actual != "anthropic:priority") {
					t.Fatal("priority inferred from invalid request form")
				}
				if e.Configured == "openai:fast" && (e.Wire != "priority" || e.Actual != "openai:fast") {
					t.Fatal("fast/priority alias lost actual observation")
				}
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), replayCode) || strings.Contains(string(stored), "Ignore this padding") {
				t.Fatal("cache body persisted")
			}
		})
	}
}

func TestTierDowngradeAndUnknownActualValuesNeverAuthorizeRequestedTier(t *testing.T) {
	for _, returned := range []string{"standard", "synthetic-private-response-field"} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := nativeFixtureWithCalls("native-messages", nativeMarker, nil)
			object(o["usage"])["service_tier"] = returned
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(o)
		}))
		_, s, _ := nativeTestSession(t, up.URL)
		s.allowed = map[string]bool{"messages.create": true}
		s.Target.Profile.ServiceTiers = []string{"anthropic:priority"}
		n := nativeScene{s: s, protocol: "native-messages", checks: map[string]Assertion{}}
		n.tiers(context.Background())
		if len(n.result.Settings) != 2 || n.result.Settings[1].State != "inconclusive" || !n.result.Settings[1].Accepted {
			t.Fatal("accepted auto request became priority support")
		}
		if returned == "standard" && n.result.Settings[1].Actual != "anthropic:standard" {
			t.Fatal("actual downgrade hidden")
		}
		stored, _ := json.Marshal(n.result)
		if strings.Contains(string(stored), "synthetic-private-response-field") {
			t.Fatal("arbitrary response string persisted")
		}
		up.Close()
	}
}

func TestImmediateCacheMissRemainsUnknownWithoutWaitingOrClaimingUnsupported(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("openai-chat-completions", replayCode, nil))
	}))
	defer up.Close()
	_, s, _ := nativeTestSession(t, up.URL)
	s.allowed = map[string]bool{"chat.create": true}
	n := nativeScene{s: s, protocol: "openai-chat-completions", checks: map[string]Assertion{}}
	n.implicitCache(context.Background())
	if s.usage.HTTP != 4 || len(n.result.Settings) != 2 {
		t.Fatal("cache miss caused polling/waiting or lost an option")
	}
	for _, e := range n.result.Settings {
		if e.State != "inconclusive" || !e.Accepted || e.CacheReadTokens != 0 {
			t.Fatal("cache miss turned into unsupported or successful write/read")
		}
	}
}

func TestMalformedTierReplyProducesNegativeEvidenceInsteadOfDetectorFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unexpected":"synthetic-private-payload"}`))
	}))
	defer up.Close()
	_, s, _ := nativeTestSession(t, up.URL)
	s.allowed = map[string]bool{"messages.create": true}
	n := nativeScene{s: s, protocol: "native-messages", checks: map[string]Assertion{}}
	n.tiers(context.Background())
	e := n.result.Settings[0]
	attempts, _ := s.store.Attempts(context.Background(), s.jobID, e.AttemptIDs)
	if e.State != "fail" || e.Accepted || !validSettingEvidence(n.result.Settings, map[string]Attempt{attempts[0].ID: attempts[0]}) {
		t.Fatal("malformed native reply could not be reported honestly")
	}
	stored, _ := json.Marshal(n.result)
	if strings.Contains(string(stored), "synthetic-private-payload") {
		t.Fatal("malformed response body persisted")
	}
}

func TestNativeExplicitCacheReadsCountStreamsAndDeletesOnlyItsOwnResource(t *testing.T) {
	deleted := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		if strings.HasPrefix(r.URL.Path, "/v1beta/cachedContents") {
			if r.Method == "DELETE" {
				if r.URL.Path != "/v1beta/cachedContents/cache_owned" {
					t.Error("foreign cache deleted")
				}
				deleted++
				_, _ = w.Write([]byte(`{}`))
				return
			}
			cache := nativeObject{"name": "cachedContents/cache_owned", "model": "models/synthetic-model", "expireTime": "2026-10-19T01:00:00Z", "usageMetadata": nativeObject{"totalTokenCount": 8192}}
			if r.Method == "PATCH" && (str(body["ttl"]) != "3600s" || r.URL.Query().Get("updateMask") != "ttl") {
				t.Error("TTL change altered cached content or omitted mask")
			}
			if r.Method == "GET" && r.URL.Path == "/v1beta/cachedContents" {
				_ = json.NewEncoder(w).Encode(nativeObject{"cachedContents": []any{nativeObject{"name": "cachedContents/foreign", "contents": "another-users-private-cache"}, cache}})
				return
			}
			_ = json.NewEncoder(w).Encode(cache)
			return
		}
		if strings.HasSuffix(r.URL.Path, ":countTokens") {
			if str(object(body["generateContentRequest"])["cachedContent"]) != "cachedContents/cache_owned" {
				t.Error("count omitted actual cached context")
			}
			_, _ = w.Write([]byte(`{"totalTokens":8194}`))
			return
		}
		tier := str(body["serviceTier"])
		text := replayCode
		if tier != "" {
			text = nativeMarker
		}
		o := nativeFixtureWithCalls("native-generate-content", text, nil)
		usage := object(o["usageMetadata"])
		if tier != "" {
			usage["serviceTier"] = tier
		} else {
			if str(body["cachedContent"]) != "cachedContents/cache_owned" {
				t.Error("request lost owned cache reference")
			}
			usage["cachedContentTokenCount"] = 8
		}
		if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, eventJSON("", o))
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	s.Target.Profile.ServiceTiers = []string{"gemini:priority"}
	result := service.run(context.Background(), s, nativeItem(t, r, s, "native-generate-content.cache_tier"), nil)
	if result.State != "pass" || deleted != 1 || len(result.Settings) != 4 || s.usage.Generate != 4 || s.usage.Count != 1 {
		t.Fatalf("explicit cache native flow failed: %+v", result)
	}
	if err := s.Cleanup(context.Background()); err != nil || deleted != 1 {
		t.Fatal("cache cleanup repeated or failed")
	}
	stored, _ := json.Marshal(result)
	if strings.Contains(string(stored), replayCode) || strings.Contains(string(stored), "another-users-private-cache") {
		t.Fatal("cache/foreign body persisted")
	}
}
