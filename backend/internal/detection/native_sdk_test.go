package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOfficialSDKBridgeUsesFixedVersionsAndNativeCredentials(t *testing.T) {
	binary := os.Getenv("AIHUB_TEST_SDK_BINARY")
	if binary == "" {
		t.Skip("build the fixed SDK artifact and set AIHUB_TEST_SDK_BINARY")
	}
	for _, protocol := range []string{"native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), nativeMarker) {
					t.Error("SDK did not send its actual typed basic request")
				}
				header := "x-api-key"
				if protocol == "native-generate-content" {
					header = "x-goog-api-key"
				}
				if r.Header.Get(header) != "synthetic-private-detector-key" || r.Header.Get("X-AIHub-SDK-Bridge") != "" {
					t.Error("SDK placeholder/bridge token replaced or leaked the actual detector credential")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls(protocol, nativeMarker, nil))
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			if err := r.RegisterSDK(context.Background(), binary); err != nil {
				t.Fatal(err)
			}
			item := nativeItem(t, r, s, protocol+".sdk")
			if !item.Implemented || !strings.Contains(item.CodeVersion, "@v1.") {
				t.Fatal("fixed SDK version absent from plan")
			}
			result := service.run(context.Background(), s, item, nil)
			if result.State != "pass" || result.Source != "synthetic" || calls != 1 || s.usage.HTTP != 1 {
				t.Fatalf("SDK native call failed or retried: calls=%d result=%+v", calls, result)
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), "synthetic-private-detector-key") || strings.Contains(string(stored), nativeMarker) {
				t.Fatal("SDK private contents persisted")
			}
		})
	}
}

func TestMissingSDKArtifactRemainsPending(t *testing.T) {
	r, s, _ := nativeTestSession(t, "https://synthetic.invalid")
	if err := r.RegisterSDK(context.Background(), t.TempDir()+"/missing-sdk"); err != nil {
		t.Fatal(err)
	}
	item := nativeItem(t, r, s, "native-messages.sdk")
	if item.Implemented {
		t.Fatal("missing SDK falsely satisfied its creation requirement")
	}
}

func TestOfficialSDKDoesNotRetryAnOrdinaryUpstreamError(t *testing.T) {
	binary := os.Getenv("AIHUB_TEST_SDK_BINARY")
	if binary == "" {
		t.Skip("fixed SDK artifact required")
	}
	for _, protocol := range []string{"native-messages", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":503,"type":"overloaded_error","status":"UNAVAILABLE","message":"synthetic unavailable"}}`))
			}))
			defer up.Close()
			r, s, service := nativeTestSession(t, up.URL)
			if err := r.RegisterSDK(context.Background(), binary); err != nil {
				t.Fatal(err)
			}
			result := service.run(context.Background(), s, nativeItem(t, r, s, protocol+".sdk"), nil)
			if calls != 1 || s.usage.HTTP != 1 || result.State != "inconclusive" {
				t.Fatalf("SDK retried or treated outage as capability support: calls=%d result=%s", calls, result.State)
			}
		})
	}
}
