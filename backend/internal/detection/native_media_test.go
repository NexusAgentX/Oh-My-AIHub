package detection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestNativeMediaRecognitionUsesInlineBytesAndPinnedOpaqueURLs(t *testing.T) {
	for protocol := range nativeJSONFixtures {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				text := "red blue"
				document := strings.Contains(body, "application/pdf") || strings.Contains(body, "document-a.pdf")
				if document {
					text = "SAPPHIRE-731 ORCHID-264"
				} else if strings.Contains(body, base64.StdEncoding.EncodeToString(nativeFixture("image-b.png"))) {
					text = "red blue green yellow"
				}
				response := nativeFixtureWithCalls(protocol, text, nil)
				if document && protocol == "native-messages" {
					object(list(response["content"])[0])["citations"] = []any{nativeObject{"type": "page_location", "document_index": 0, "start_page_number": 1, "end_page_number": 2, "cited_text": "The notebook code is SAPPHIRE-731."}, nativeObject{"type": "page_location", "document_index": 0, "start_page_number": 2, "end_page_number": 3, "cited_text": "The delivery code is ORCHID-264."}}
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
			for _, family := range []string{"vision", "document"} {
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
				for _, secret := range []string{"SAPPHIRE-731", "ORCHID-264", "red blue", base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))} {
					if strings.Contains(string(stored), secret) {
						t.Fatal("media content persisted")
					}
				}
			}
			want := 5
			if protocol == "openai-chat-completions" {
				want = 4
			}
			if calls != want {
				t.Fatalf("unexpected media request count %d != %d", calls, want)
			}
		})
	}
}
func TestSyntheticFixturesAreValidAndDoNotAdvertiseAnswersInURLs(t *testing.T) {
	files, version, err := nativeFixtures()
	if err != nil || len(files) != 6 || len(version) != 64 {
		t.Fatalf("invalid fixture set: %v", err)
	}
	for _, name := range []string{"image-a.png", "image-b.png", "mask-a.png"} {
		config, format, err := image.DecodeConfig(bytes.NewReader(files[name]))
		if err != nil || format != "png" || config.Width != 256 || config.Height != 256 {
			t.Fatalf("invalid native image fixture %s: %+v %v", name, config, err)
		}
	}
	if !bytes.HasPrefix(files["audio-a.wav"], []byte("RIFF")) || !bytes.Contains(files["audio-a.wav"][:16], []byte("WAVE")) || !bytes.Contains(files["video-a.mp4"][:32], []byte("ftyp")) {
		t.Fatal("invalid audio/video containers")
	}
	if !bytes.HasPrefix(files["document-a.pdf"], []byte("%PDF-1.4")) || !bytes.Contains(files["document-a.pdf"], []byte("/Count 2")) {
		t.Fatal("invalid PDF fixture")
	}
	for _, name := range []string{"image-a.png", "image-b.png", "audio-a.wav", "video-a.mp4", "document-a.pdf"} {
		uri := nativeFixtureBase + name
		for _, answer := range []string{"red", "blue", "green", "yellow", "beep", "SAPPHIRE", "ORCHID"} {
			if strings.Contains(uri, answer) {
				t.Fatalf("URI reveals a semantic answer: %s", uri)
			}
		}
	}
}
func TestDocumentCitationMustMatchPageAndSource(t *testing.T) {
	raw, _ := decodeNative([]byte(nativeJSONFixtures["native-messages"]))
	block := object(list(raw["content"])[0])
	block["text"] = "The notebook code is SAPPHIRE-731."
	citation := nativeObject{"type": "page_location", "document_index": json.Number("0"), "start_page_number": json.Number("1"), "end_page_number": json.Number("2"), "cited_text": "The notebook code is SAPPHIRE-731."}
	block["citations"] = []any{citation}
	if !validDocumentCitations("native-messages", parseNative("native-messages", raw)) {
		t.Fatal("valid PDF citation rejected")
	}
	for _, test := range []struct {
		Field string
		Value any
	}{{"document_index", json.Number("1")}, {"end_page_number", json.Number("1")}, {"end_page_number", json.Number("4")}, {"cited_text", "The notebook code is FAKE-000."}, {"start_page_number", json.Number("2")}} {
		copy := cloneNative(citation)
		copy[test.Field] = test.Value
		block["citations"] = []any{copy}
		if validDocumentCitations("native-messages", parseNative("native-messages", raw)) {
			t.Fatalf("incorrect source/page accepted: %+v", test)
		}
	}
}
