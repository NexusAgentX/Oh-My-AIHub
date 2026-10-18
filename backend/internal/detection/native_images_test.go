package detection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func syntheticEditedImage(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := range 256 {
			c := color.NRGBA{G: 255, A: 255}
			if x >= 128 {
				c = color.NRGBA{B: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestImageInspectionRequiresRealProductsAndMaskSemantics(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))
	p, pixels, ok := inspectImagePixels(encoded)
	if !ok || p.Width != 256 || p.Height != 256 || p.Format != "png" || p.Transparent || maskPreserved(pixels) {
		t.Fatalf("original image mistaken for an edit: %+v", p)
	}
	_, edited, ok := inspectImagePixels(syntheticEditedImage(t))
	if !ok || !maskPreserved(edited) {
		t.Fatal("edited region or protected region not recognized")
	}
	for _, bad := range []string{"not an image", base64.StdEncoding.EncodeToString([]byte("PNG")), encoded[:len(encoded)/2]} {
		if _, ok := inspectImage(bad); ok {
			t.Fatal("invalid product counted")
		}
	}
	if imageSizeMatches(p, "1024x1024") || imageSizeMatches(p, "256x-1") || !imageSizeMatches(p, "256x256") {
		t.Fatal("requested size not verified")
	}
}

func TestImageSSERequiresOneFinalProductAndValidEventOrder(t *testing.T) {
	complete := `data: {"type":"image_generation.completed","b64_json":"AA==","created_at":1}` + "\n\n"
	partial := `data: {"type":"image_generation.partial_image","partial_image_index":0,"b64_json":"AA=="}` + "\n\n"
	if _, ok := parseImageProduct([]byte(partial+complete), true, "images.generations", 1024); !ok {
		t.Fatal("native stream rejected")
	}
	for _, bad := range []string{partial, strings.TrimSuffix(complete, "\n\n"), complete + complete, partial + partial + complete, strings.Replace(complete, "image_generation", "image_edit", 1)} {
		if _, ok := parseImageProduct([]byte(bad), true, "images.generations", 1024); ok {
			t.Fatal("invalid/truncated stream counted as final product")
		}
	}
}

func TestNativeImageProfilesUseActualModelsAndShareIdenticalUnits(t *testing.T) {
	encoded := syntheticEditedImage(t)
	for _, endpoint := range []string{"images.generations", "images.edits", "images.variations"} {
		t.Run(endpoint, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/images/"+strings.TrimPrefix(endpoint, "images.") {
					t.Error("image endpoint converted")
				}
				var body nativeObject
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					if r.FormValue("model") != "synthetic-image-model" {
						t.Error("used text model for images")
					}
					field, count := "image", 1
					if endpoint == "images.edits" {
						field, count = "image[]", 2
						if len(r.MultipartForm.File["mask"]) != 1 {
							t.Error("missing mask")
						}
					}
					if len(r.MultipartForm.File[field]) != count || r.MultipartForm.File[field][0].Header.Get("Content-Type") != "image/png" {
						t.Error("incorrect native multipart")
					}
				} else {
					raw, _ := io.ReadAll(r.Body)
					body, _ = decodeNative(raw)
					if str(body["model"]) != "synthetic-image-model" {
						t.Error("used another image model")
					}
					if endpoint == "images.edits" && (len(list(body["images"])) != 2 || object(body["mask"]) == nil) {
						t.Error("missing native multi-image/mask input")
					}
					if body["response_format"] != nil {
						t.Error("added removed legacy parameter to modern profile")
					}
				}
				response := nativeObject{"created": 1, "data": []any{nativeObject{"b64_json": encoded, "revised_prompt": "private-upstream-prompt"}}, "quality": "high", "size": "256x256", "usage": nativeObject{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}
				if body["stream"] == true {
					prefix := "image_generation"
					if endpoint == "images.edits" {
						prefix = "image_edit"
					}
					response["type"], response["b64_json"], response["created_at"] = prefix+".completed", encoded, 1
					delete(response, "created")
					delete(response, "data")
					raw, _ := json.Marshal(response)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "event: %s.completed\ndata: %s\n\n", prefix, raw)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(response)
				}
			}))
			defer up.Close()
			registry, session, service := nativeTestSession(t, up.URL)
			session.Target.ModelType, session.Target.Model = "image", "synthetic-image-model"
			session.Target.Profile.Images = []ImageVariant{{Unit: "request", Size: "256x256", Quality: "high"}, {Unit: "successful_image", Size: "256x256", Quality: "high"}}
			plan, err := registry.Plan(session.Target)
			if err != nil {
				t.Fatal(err)
			}
			var item Planned
			for _, candidate := range plan.Items {
				if slices.Contains(candidate.Requirements, endpoint) {
					item = candidate
					break
				}
			}
			result := service.run(context.Background(), session, item, nil)
			if result.State != "pass" {
				t.Fatalf("image native profile failed: %+v", result)
			}
			want := map[string]int{"images.generations": 2, "images.edits": 4, "images.variations": 1}[endpoint]
			if calls != want || session.usage.Images != int64(want) || session.usage.InputTokens != int64(want*2) {
				t.Fatalf("unit aliases caused repeat billing/calls: %d %+v", calls, session.usage)
			}
			stored, _ := json.Marshal(result)
			if strings.Contains(string(stored), encoded) || strings.Contains(string(stored), "private-upstream-prompt") {
				t.Fatal("image or revised prompt persisted")
			}
		})
	}
}

func TestImageURLOutputIsNotFetchedAndIgnoredOptionsAreNotConfirmed(t *testing.T) {
	for _, urlOnly := range []bool{true, false} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/images/generations" {
				t.Error("fetched returned URL")
			}
			product := nativeObject{"url": "http://127.0.0.1/private-product"}
			if !urlOnly {
				product = nativeObject{"b64_json": base64.StdEncoding.EncodeToString(nativeFixture("image-a.png"))}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nativeObject{"created": 1, "data": []any{product}})
		}))
		_, s, _ := nativeTestSession(t, up.URL)
		s.allowed = map[string]bool{"images.generations": true}
		n := &nativeScene{s: s, protocol: "openai-images", checks: map[string]Assertion{}}
		out, e := n.imageRequest(context.Background(), "images.generations", "json", ImageVariant{Size: "256x256", Quality: "high"})
		if out.State != "inconclusive" || !e.UnpricedTokens || e.UsageReliable || e.OptionsConfirmed && !urlOnly {
			t.Fatalf("unconfirmed image data/option guessed: %+v %+v", out, e)
		}
		if urlOnly && e.Verified != 0 || !urlOnly && e.Verified != 1 {
			t.Fatal("URL counted as decoded product")
		}
		if s.usage.HTTP != 1 {
			t.Fatal("extra product URL fetch")
		}
		up.Close()
	}
}

func TestImageEventsUseBoundedResponseLimitInsteadOfTextLimit(t *testing.T) {
	s := &Session{limits: DefaultLimits()}
	if s.eventLimit("images.generations") != int(s.limits.ResponseBytes) || s.eventLimit("codex.images") != int(s.limits.ResponseBytes) || s.eventLimit("responses.create") != s.limits.EventBytes {
		t.Fatal("image/text event bounds conflated")
	}
}
