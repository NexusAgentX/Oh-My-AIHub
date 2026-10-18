package detection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestNativeMeterRejectsOverflowWithoutPartialAccounting(t *testing.T) {
	s := &Session{usage: Usage{InputTokens: 3, OutputTokens: math.MaxInt64 - 1}}
	before := s.usage
	if s.Meter(2, 2, 1, 1) != ErrInvalid || s.usage != before {
		t.Fatal("overflow changed another usage counter")
	}
}

func TestAudioContainersRejectTruncationAndSilence(t *testing.T) {
	data := nativeFixture("audio-a.wav")
	encoded := base64.StdEncoding.EncodeToString(data)
	if !validWave(data) || !validNativeAudio(encoded, "audio/wav") {
		t.Fatal("valid synthetic WAV rejected")
	}
	for _, bad := range [][]byte{data[:20], data[:len(data)-1], append(bytes.Clone(data), 0), make([]byte, len(data))} {
		if validWave(bad) {
			t.Fatal("invalid/truncated wave accepted")
		}
	}
	silent := bytes.Clone(data)
	clear(silent[44:])
	if validWave(silent) {
		t.Fatal("silent audio counted as actual content")
	}
	if validNativeAudio(strings.Repeat("!", 400), "audio/wav") || validNativeAudio(encoded, "audio/unknown") {
		t.Fatal("invalid native audio accepted")
	}
	if !validNativeAudio(base64.StdEncoding.EncodeToString(data[44:]), "audio/L16;codec=pcm;rate=16000") || validNativeAudio(base64.StdEncoding.EncodeToString(data[44:]), "audio/L16;rate=1") {
		t.Fatal("raw PCM validation mismatch")
	}
}
func TestNativeAudioAndVideoKeepNativeRequestShapes(t *testing.T) {
	for _, protocol := range []string{"openai-chat-completions", "native-generate-content"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				text := "2"
				if strings.Contains(body, "video/mp4") || strings.Contains(body, "video-a.mp4") {
					text = "red blue"
				}
				response := nativeFixtureWithCalls(protocol, text, nil)
				if strings.Contains(body, "responseModalities") || strings.Contains(body, `"modalities":["text","audio"]`) {
					encoded := base64.StdEncoding.EncodeToString(nativeFixture("audio-a.wav"))
					if protocol == "openai-chat-completions" {
						message := object(object(list(response["choices"])[0])["message"])
						message["content"] = nil
						message["audio"] = nativeObject{"id": "synthetic-audio", "data": encoded, "transcript": "Synthetic audio output", "expires_at": 2000000000}
					} else {
						object(list(response["candidates"])[0])["content"] = nativeObject{"role": "model", "parts": []any{nativeObject{"inlineData": nativeObject{"mimeType": "audio/wav", "data": encoded}}}}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer up.Close()
			r, session, service := nativeTestSession(t, up.URL)
			plan, err := r.Plan(session.Target)
			if err != nil {
				t.Fatal(err)
			}
			families := []string{"audio"}
			if protocol == "native-generate-content" {
				families = append(families, "video")
			}
			for _, family := range families {
				var item Planned
				for _, p := range plan.Items {
					if slices.Contains(p.Requirements, protocol+"."+family) {
						item = p
						break
					}
				}
				result := service.run(context.Background(), session, item, nil)
				if result.State != "pass" {
					t.Fatalf("%s: %+v", family, result)
				}
				stored, _ := json.Marshal(result)
				if strings.Contains(string(stored), base64.StdEncoding.EncodeToString(nativeFixture("audio-a.wav"))) || strings.Contains(string(stored), "synthetic-audio") {
					t.Fatal("audio body/ephemeral native state persisted")
				}
			}
			want := 2
			if protocol == "native-generate-content" {
				want = 4
			}
			if calls != want {
				t.Fatalf("unexpected media attempts %d != %d", calls, want)
			}
		})
	}
}
func TestResponsesAudioIsAnExplicitExtensionAndDoesNotBorrowChatProof(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Error("audio extension converted to another endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid value: input_audio. Supported values are input_text, input_image, input_file.","type":"invalid_request_error","code":"invalid_value","param":"input[0].content[1].type"}}`))
	}))
	defer up.Close()
	r, session, service := nativeTestSession(t, up.URL)
	plan, err := r.Plan(session.Target)
	if err != nil {
		t.Fatal(err)
	}
	var item Planned
	for _, p := range plan.Items {
		if slices.Contains(p.Requirements, "openai-responses.audio") {
			item = p
			break
		}
	}
	result := service.run(context.Background(), session, item, nil)
	if result.State != "unsupported" || len(result.Assertions) != 2 || session.usage.HTTP != 1 {
		t.Fatalf("native extension misrepresented: %+v %+v", result, session.usage)
	}
}
