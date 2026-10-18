package gatewaycontract_test

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// These are isolated HTTP experiments using the real scanner and native wire
// bytes. They do not install a second production proxy or prove supplier support.
func TestOpaqueReplayPreservesNativeBytesWithoutOwnershipLookup(t *testing.T) {
	input := []byte(`{ "model" : "alias", "store":false, "input":[{"type":"reasoning","id":"never-registered","encrypted_content":"synthetic+cipher=="},{"type":"compaction","id":"also-not-registered","encrypted_content":"synthetic-window"}], "unknown_extension":{"nested":[1,2]} }`)
	want := bytes.Replace(input, []byte(`"alias"`), []byte(`"vendor/model:v1"`), 1)
	sse := "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"id\":\"opaque\",\"encrypted_content\":\"synthetic-window\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil || !bytes.Equal(body, want) {
			t.Error("opaque native bytes changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer upstream.Close()
	if bound, err := gatewaycontract.ResolveReferences(gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}, nil,
		func(gatewaycontract.Reference) ([]gatewaycontract.Resource, error) {
			t.Fatal("opaque internal ID caused ownership lookup")
			return nil, nil
		}); err != nil || bound != nil {
		t.Fatal(bound, err)
	}
	_, scan, err := gateway.ScanRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := gateway.UpstreamBody(input, scan, "vendor/model:v1", false)
	res, err := http.Post(upstream.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil || string(got) != sse {
		t.Fatal("SSE bytes changed")
	}
}

func TestImageEditMultipartDoesNotRequireFilesService(t *testing.T) {
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	if err := writer.WriteField("model", "image-model"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("image", "synthetic.png")
	if err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(pngBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/images/edits" {
			t.Error("unexpected Files dependency")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		p, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		model, _ := io.ReadAll(io.LimitReader(p, 256))
		if p.FormName() != "model" || string(model) != "image-model" {
			t.Error("model changed")
		}
		p, err = reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		data, _ := io.ReadAll(io.LimitReader(p, 256))
		if p.FormName() != "image" || !bytes.Equal(data, pngBytes.Bytes()) {
			t.Error("inline image changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"synthetic-image"}]}`)
	}))
	defer upstream.Close()
	res, err := http.Post(upstream.URL+"/v1/images/edits", writer.FormDataContentType(), &input)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || requests.Load() != 1 {
		t.Fatal("single-request multipart experiment failed")
	}
}

func TestUnauthorizedResourceFailsBeforeUpstreamAndContextCanMove(t *testing.T) {
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	first := gatewaycontract.Binding{GroupID: "g", ChannelID: "a", CredentialVersion: "original"}
	rows := []gatewaycontract.Resource{
		{Kind: "response", ID: "resp-a", AccountID: "a", KeyID: "k", Binding: first},
		{Kind: "codex_note", ID: "session/path", AccountID: "a", KeyID: "k"},
	}
	lookup := func(gatewaycontract.Reference) ([]gatewaycontract.Resource, error) { return rows, nil }
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"id":"resp-a"}`)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestActor := actor
		requestActor.KeyID = r.Header.Get("X-Synthetic-Platform-Key")
		bound, err := gatewaycontract.ResolveReferences(requestActor, []gatewaycontract.Reference{{Kind: "response", ID: "resp-a"}}, lookup)
		if err != nil || bound == nil {
			http.NotFound(w, r)
			return
		}
		res, err := http.Get(upstream.URL)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer res.Body.Close()
		_, _ = io.Copy(w, io.LimitReader(res.Body, 1024))
	}))
	defer proxy.Close()
	for _, key := range []string{"other-key", "k"} {
		req, _ := http.NewRequest(http.MethodGet, proxy.URL, nil)
		req.Header.Set("X-Synthetic-Platform-Key", key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if key == "other-key" && (res.StatusCode != 404 || calls.Load() != 0) {
			t.Fatal("unauthorized request reached shared credentials")
		}
		if key == "k" && (res.StatusCode != 200 || calls.Load() != 1) {
			t.Fatal("owned resource inaccessible")
		}
	}
	bound, err := gatewaycontract.ResolveReferences(actor, []gatewaycontract.Reference{{Kind: "codex_note", ID: "session/path"}}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	all := []gatewaycontract.Candidate{
		{Binding: first, Enabled: true, Authorized: true, Callable: true},
		{Binding: gatewaycontract.Binding{GroupID: "other-group", ChannelID: "b", CredentialVersion: "other-key"}, Enabled: true, Authorized: true, Callable: true},
	}
	for _, id := range []string{"a", "b"} {
		got, err := gatewaycontract.Candidates(gatewaycontract.Selection{Bound: bound, Manual: true, Order: []string{id}}, all)
		if err != nil || len(got) != 1 || got[0].ChannelID != id {
			t.Fatal("context source locked routing", got, err)
		}
	}
}
