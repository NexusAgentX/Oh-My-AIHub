package detection

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
)

// ImageEvidence stores facts about products, never pixels, returned URLs,
// prompts, revised prompts or base64. The pricing selector is user configuration.
type ImageEvidence struct {
	Variant          ImageVariant      `json:"variant"`
	Transport        string            `json:"transport"`
	State            string            `json:"state"`
	Reason           string            `json:"reason"`
	Requested        int               `json:"requested"`
	Returned         int               `json:"returned"`
	Verified         int               `json:"verified"`
	Products         []ImageProperties `json:"products"`
	OptionsConfirmed bool              `json:"options_confirmed"`
	MaskPreserved    bool              `json:"mask_preserved"`
	UsageReliable    bool              `json:"usage_reliable"`
	UnpricedTokens   bool              `json:"unpriced_tokens"`
	AttemptIDs       []string          `json:"attempt_ids"`
}
type ImageProperties struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Format      string `json:"format"`
	Transparent bool   `json:"transparent"`
}

func inspectImage(data string) (ImageProperties, bool) {
	p, _, ok := inspectImagePixels(data)
	return p, ok
}

func inspectImagePixels(data string) (ImageProperties, image.Image, bool) {
	if len(data) == 0 || base64.StdEncoding.DecodedLen(len(data)) > 24<<20 {
		return ImageProperties{}, nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return ImageProperties{}, nil, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	// Check before allocating decoded pixels. Inspection runs only on bounded
	// background detection workers; a compressed image cannot allocate arbitrarily.
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return ImageProperties{}, nil, false
	}
	decoded, actual, err := image.Decode(bytes.NewReader(raw))
	if err != nil || actual != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return ImageProperties{}, nil, false
	}
	p := ImageProperties{Width: config.Width, Height: config.Height, Format: format}
	// A bounded 64x64 grid suffices for alpha observation; absent sampled alpha
	// is not a proof of an opaque image. Transparent options require positive proof.
	for y := 0; y < config.Height; y += max(1, config.Height/64) {
		for x := 0; x < config.Width; x += max(1, config.Width/64) {
			_, _, _, alpha := decoded.At(x+decoded.Bounds().Min.X, y+decoded.Bounds().Min.Y).RGBA()
			p.Transparent = p.Transparent || alpha < 65535
		}
	}
	return p, decoded, true
}

func maskPreserved(decoded image.Image) bool {
	b := decoded.Bounds()
	green, blue, left, right := 0, 0, 0, 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			r, g, bl, a := decoded.At(b.Min.X+(2*x+1)*b.Dx()/64, b.Min.Y+(2*y+1)*b.Dy()/64).RGBA()
			if x < 16 {
				left++
				if a > 50000 && g > 30000 && g > r*2 && g > bl*2 {
					green++
				}
			} else {
				right++
				if a > 50000 && bl > 30000 && bl > r*2 && bl > g*2 {
					blue++
				}
			}
		}
	}
	return green*10 >= left*7 && blue*10 >= right*8
}

func imageSizeMatches(p ImageProperties, size string) bool {
	if size == "" || size == "auto" {
		return true
	}
	w, h, ok := strings.Cut(size, "x")
	width, ew := strconv.Atoi(w)
	height, eh := strconv.Atoi(h)
	return ok && ew == nil && eh == nil && p.Width == width && p.Height == height
}
func nativeRequestedTier(configured string) string {
	_, tier, ok := strings.Cut(configured, ":")
	if !ok {
		tier = configured
	}
	if tier == "fast" {
		return "priority"
	}
	return tier
}

func parseImageProduct(body []byte, streaming bool, endpoint string, eventLimit int) (nativeObject, bool) {
	if !streaming {
		o, err := decodeNative(body)
		return o, err == nil && o["error"] == nil
	}
	events, err := nativeEvents(body, eventLimit)
	if err != nil || len(events) == 0 {
		return nil, false
	}
	prefix := "image_generation"
	if endpoint == "images.edits" {
		prefix = "image_edit"
	}
	var completed nativeObject
	lastPartial := int64(-1)
	for _, event := range events {
		o, err := decodeNative(event.Data)
		kind := str(o["type"])
		if err != nil || completed != nil || event.Name != "" && event.Name != kind {
			return nil, false
		}
		switch kind {
		case prefix + ".partial_image":
			index, ok := fieldInt(o, "partial_image_index")
			if !ok || index <= lastPartial || str(o["b64_json"]) == "" {
				return nil, false
			}
			lastPartial = index
		case prefix + ".completed":
			if str(o["b64_json"]) == "" {
				return nil, false
			}
			completed = o
		default:
			return nil, false
		}
	}
	if completed == nil {
		return nil, false
	}
	// The SSE completion contains one final product directly, not data[].
	out := nativeObject{}
	for k, v := range completed {
		out[k] = v
	}
	out["data"] = []any{nativeObject{"b64_json": completed["b64_json"]}}
	return out, true
}

func imageUsage(o nativeObject) nativeUsage {
	u := object(o["usage"])
	if u == nil {
		return nativeUsage{}
	}
	// Images use the Responses-style token counters. Do not infer token usage
	// from product count, requested size or a missing total.
	return nativeUsageFrom("openai-responses", nativeObject{"usage": u, "service_tier": o["service_tier"]})
}

func (n *nativeScene) imageRequest(ctx context.Context, endpoint, transport string, variant ImageVariant) (nativeOutcome, ImageEvidence) {
	endpointID := endpoint
	codex := endpoint == "codex.images" || endpoint == "codex.images.edit"
	if codex {
		endpoint = "images.generations"
		if endpointID == "codex.images.edit" {
			endpoint = "images.edits"
		}
	}
	request := nativeObject{"model": n.s.Target.Model, "n": 1}
	if endpoint != "images.variations" {
		request["prompt"] = "Create a simple blue circle centered on a plain white background. Do not add text."
		request["output_format"] = "png"
		if variant.Background == "transparent" {
			request["prompt"] = "Create a simple blue circle centered on a transparent background. Do not add text."
		}
	} else {
		request["response_format"] = "b64_json"
	}
	for key, value := range map[string]string{"size": variant.Size, "quality": variant.Quality, "background": variant.Background, "service_tier": nativeRequestedTier(variant.ServiceTier)} {
		if value != "" {
			request[key] = value
		}
	}
	if endpoint == "images.edits" {
		request["prompt"] = "Fill the entire transparent masked left half of the first image with solid green. Preserve the unmasked right blue half exactly. Use the second image only as a green color reference."
		request["images"] = []any{nativeObject{"image_url": nativeDataURL("image-a.png", "image/png")}, nativeObject{"image_url": nativeDataURL("image-b.png", "image/png")}}
		request["mask"] = nativeObject{"image_url": nativeDataURL("mask-a.png", "image/png")}
		if transport == "json_url" {
			request["images"] = []any{nativeObject{"image_url": nativeFixtureBase + "image-a.png"}, nativeObject{"image_url": nativeFixtureBase + "image-b.png"}}
			request["mask"] = nativeObject{"image_url": nativeFixtureBase + "mask-a.png"}
		}
	}
	if transport == "sse" {
		request["stream"] = true
		request["partial_images"] = 1
	}
	if codex {
		// The pinned native image API is JSON-only. It has inline images for
		// edits, with no standalone Files dependency and no mask parameter.
		delete(request, "output_format")
		delete(request, "mask")
		if endpoint == "images.edits" {
			request["prompt"] = "Replace the left half of the first image with green. Use the second image as a green color reference."
		}
	}
	var encoded []byte
	header := http.Header{"Content-Type": {"application/json"}}
	if transport == "multipart" {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for key, value := range request {
			if key == "images" || key == "mask" {
				continue
			}
			_ = writer.WriteField(key, fmt.Sprint(value))
		}
		field := "image[]"
		if endpoint == "images.variations" {
			field = "image"
		}
		writeImagePart := func(field, filename string) {
			header := textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename)}, "Content-Type": {"image/png"}}
			part, _ := writer.CreatePart(header)
			_, _ = part.Write(nativeFixture(filename))
		}
		writeImagePart(field, "image-a.png")
		if endpoint == "images.edits" {
			writeImagePart("image[]", "image-b.png")
			writeImagePart("mask", "mask-a.png")
		}
		_ = writer.Close()
		encoded = buf.Bytes()
		header.Set("Content-Type", writer.FormDataContentType())
	} else {
		encoded, _ = json.Marshal(request)
	}
	path := "/v1/images/" + strings.TrimPrefix(endpoint, "images.")
	if codex {
		path = "/backend-api/codex/images/" + strings.TrimPrefix(endpoint, "images.")
	}
	out := n.call(ctx, Request{EndpointID: endpointID, Path: path, Headers: header, Body: encoded, Category: "generate"}, false)
	e := ImageEvidence{Variant: variant, Transport: transport, State: out.State, Reason: out.Reason, Requested: 1, Products: []ImageProperties{}, UnpricedTokens: true, AttemptIDs: []string{out.Response.AttemptID}}
	if out.Response.Status < 200 || out.Response.Status >= 300 || out.Response.AttemptID == "" || (out.State == "inconclusive" && out.Reason != "invalid_response") {
		return out, e
	}
	streaming := strings.Contains(strings.ToLower(out.Response.Headers.Get("Content-Type")), "text/event-stream")
	o, valid := parseImageProduct(out.Response.Body, streaming, endpoint, int(n.s.limits.ResponseBytes))
	out.State, out.Reason = "fail", "invalid_response"
	if !valid || streaming != (transport == "sse") {
		e.State, e.Reason = out.State, out.Reason
		return out, e
	}
	_, created := fieldInt(o, "created")
	if streaming {
		_, created = fieldInt(o, "created_at")
	}
	products := list(o["data"])
	e.Returned = len(products)
	e.OptionsConfirmed = true
	urlOnly := false
	for _, raw := range products[:min(len(products), 32)] {
		product := object(raw)
		p, pixels, ok := inspectImagePixels(str(product["b64_json"]))
		if !ok {
			urlOnly = urlOnly || str(product["url"]) != ""
			continue
		}
		e.Products = append(e.Products, p)
		e.Verified++
		if endpoint == "images.edits" && e.Verified == 1 {
			e.MaskPreserved = maskPreserved(pixels)
		}
		e.OptionsConfirmed = e.OptionsConfirmed && imageSizeMatches(p, variant.Size)
		if variant.Background == "transparent" {
			e.OptionsConfirmed = e.OptionsConfirmed && p.Transparent
		}
	}
	// Quality, opaque background and actual service tier cannot be proven from
	// accepting a parameter. Require a native actual-return field, or explicit
	// alpha evidence for transparent output. A missing echo stays inconclusive.
	for key, requested := range map[string]string{"quality": variant.Quality, "background": variant.Background, "service_tier": nativeRequestedTier(variant.ServiceTier)} {
		if requested != "" && requested != "auto" && !(key == "background" && requested == "transparent") {
			e.OptionsConfirmed = e.OptionsConfirmed && str(o[key]) == requested
		}
	}
	usage := imageUsage(o)
	e.UsageReliable, e.UnpricedTokens = usage.Reliable, !usage.Reliable
	if usage.Reliable {
		if n.s.Meter(usage.Input, usage.Output, 0, 0) != nil {
			e.UsageReliable, e.UnpricedTokens = false, true
		}
	}
	if e.Verified > 0 {
		_ = n.s.Meter(0, 0, int64(e.Verified), 0)
	}
	if created && e.Returned == e.Requested && e.Verified == e.Requested {
		out.State, out.Reason = "pass", "ok"
		if !e.OptionsConfirmed {
			out.State, out.Reason = "inconclusive", "not_observed"
		}
	} else if urlOnly {
		// No arbitrary returned URL is fetched or treated as a valid image.
		out.State, out.Reason = "inconclusive", "not_observed"
	}
	e.State, e.Reason = out.State, out.Reason
	return out, e
}

func (n *nativeScene) images(ctx context.Context) {
	endpoint := n.requirement.ID
	variants := slices.Clone(n.s.Target.Profile.Images)
	if len(variants) == 0 {
		variants = []ImageVariant{{}}
	}
	type sharedImage struct {
		out      nativeOutcome
		evidence ImageEvidence
	}
	shared := map[string]sharedImage{}
	for i, variant := range variants {
		transports := []string{"json"}
		if endpoint == "images.variations" {
			transports = []string{"multipart"}
		} else if i == 0 {
			if endpoint == "images.edits" {
				transports = append(transports, "json_url", "multipart")
			}
			transports = append(transports, "sse")
		}
		for _, transport := range transports {
			if ctx.Err() != nil {
				return
			}
			keyVariant := variant
			keyVariant.Unit = ""
			keyBody, _ := json.Marshal(keyVariant)
			key := transport + string(keyBody)
			prior, cached := shared[key]
			if !cached {
				prior.out, prior.evidence = n.imageRequest(ctx, endpoint, transport, variant)
				shared[key] = prior
			}
			out, evidence := prior.out, prior.evidence
			evidence.Variant = variant
			n.result.Images = append(n.result.Images, evidence)
			productOut := out
			if evidence.Verified == evidence.Requested && evidence.Returned == evidence.Requested && out.Reason == "not_observed" {
				productOut.State, productOut.Reason = "pass", "ok"
			}
			for _, label := range []string{"actual_image_model", "native_json_or_multipart", "actual_product_count", "reliable_usage_or_unpriced"} {
				n.check(label, productOut, evidence.Verified == evidence.Requested && evidence.Returned == evidence.Requested)
			}
			n.check("each_enabled_pricing_unit", out, evidence.OptionsConfirmed && evidence.Verified == evidence.Requested)
			if transport == "sse" {
				n.check("native_sse_lifecycle", productOut, evidence.Verified == evidence.Requested)
			}
			if transport == "multipart" {
				n.check("native_multipart", productOut, evidence.Verified == evidence.Requested)
			}
			if endpoint == "images.edits" && transport != "sse" {
				n.check("inline_url_mask_multiple_images", productOut, evidence.Verified == evidence.Requested)
				n.check("masked_edit_preserves_unmasked_region", productOut, evidence.MaskPreserved)
			}
		}
	}
}

func validImageEvidence(evidence []ImageEvidence, witnesses map[string]Attempt) bool {
	if len(evidence) > 256 {
		return false
	}
	for _, e := range evidence {
		if !terminal(e.State) || !reasonValid(e.Reason) || !slices.Contains([]string{"json", "json_url", "multipart", "sse"}, e.Transport) || e.Requested != 1 || e.Returned < 0 || e.Returned > 100000 || e.Verified < 0 || e.Verified > min(32, e.Returned) || len(e.Products) != e.Verified || len(e.AttemptIDs) != 1 || len(e.Variant.Size) > 64 || len(e.Variant.Quality) > 32 || len(e.Variant.Background) > 32 || len(e.Variant.ServiceTier) > 64 || !slices.Contains([]string{"", "request", "successful_image"}, e.Variant.Unit) || e.UsageReliable == e.UnpricedTokens {
			return false
		}
		attempt, ok := witnesses[e.AttemptIDs[0]]
		imageEndpoint := strings.HasPrefix(attempt.EndpointID, "images.") || attempt.EndpointID == "codex.images" || attempt.EndpointID == "codex.images.edit"
		if !ok || !imageEndpoint || (e.State == "pass" && (!attempt.ResponseReceived || attempt.Status < 200 || attempt.Status >= 300 || e.Verified != e.Requested || e.Returned != e.Requested || !e.OptionsConfirmed)) {
			return false
		}
		for _, p := range e.Products {
			if p.Width < 1 || p.Height < 1 || p.Width > 16384 || p.Height > 16384 || int64(p.Width)*int64(p.Height) > 16<<20 || !slices.Contains([]string{"png", "jpeg"}, p.Format) {
				return false
			}
		}
	}
	return true
}
