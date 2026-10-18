package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNativeContinuationSharesLengthProbeAndKeepsOriginalContents(t *testing.T) {
	const opaque = "synthetic-opaque-continuation=="
	calls := 0
	var original any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		body, _ := decodeNative(raw)
		text := "1 2 "
		if calls == 1 {
			original = body["contents"]
		} else {
			if !reflect.DeepEqual(original, body["contents"]) || str(body["continuationToken"]) != opaque {
				t.Error("continuation appended output instead of preserving original contents and token")
			}
			limit, _ := fieldInt(object(body["generationConfig"]), "maxOutputTokens")
			if limit <= 16 {
				t.Error("cumulative limit did not increase")
			}
			text = "3 4"
		}
		o := nativeFixtureWithCalls("native-generate-content", text, nil)
		if calls == 1 {
			c := object(list(o["candidates"])[0])
			c["finishReason"], c["continuationToken"] = "MAX_TOKENS", opaque
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(o)
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	s.allowed = map[string]bool{"gemini.generateContent": true}
	n := nativeScene{s: s, protocol: "native-generate-content"}
	body := baseNative(n.protocol, s.Target.Model, nativeLengthPrompt)
	outputLimit(n.protocol, body, 16)
	first := n.infer(context.Background(), body, false, true)
	if first.State != "pass" {
		t.Fatal("initial length proof failed")
	}
	result := service.run(context.Background(), s, nativeItem(t, r, s, "native-generate-content.continuation"), nil)
	if result.State != "pass" || calls != 2 || result.Usage.HTTP != 1 || s.usage.InputTokens != 20 || s.usage.OutputTokens != 8 {
		t.Fatalf("continuation duplicated a probe or usage: %+v", result)
	}
	stored, _ := json.Marshal(result)
	if strings.Contains(string(stored), opaque) || strings.Contains(string(stored), "1 2") {
		t.Fatal("continuation state/output persisted")
	}
}

func TestMissingNativeContinuationTokenIsUnknownNotUnsupported(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nativeFixtureWithCalls("native-generate-content", "1 2", nil))
	}))
	defer up.Close()
	r, s, service := nativeTestSession(t, up.URL)
	result := service.run(context.Background(), s, nativeItem(t, r, s, "native-generate-content.continuation"), nil)
	if result.State != "inconclusive" || s.usage.HTTP != 1 {
		t.Fatalf("missing token invented support/non-support or long run: %+v", result)
	}
}
