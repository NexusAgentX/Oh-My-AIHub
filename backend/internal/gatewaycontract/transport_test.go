package gatewaycontract_test

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
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
	body, _ := gateway.UpstreamBody(input, scan, "vendor/model:v1", false, false)
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
		{Kind: "response", ID: "resp-a", AccountID: "a", KeyID: "k", Binding: first, CredentialScope: "upstream-key", CreationID: "creation-a"},
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
	var bound *gatewaycontract.Binding
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

func TestSameGroupEntryFallbackKeepsScopeAndDoesNotReplayUnknownWrites(t *testing.T) {
	actor := gatewaycontract.Principal{AccountID: "a", KeyID: "k", Active: true}
	binding := gatewaycontract.Binding{GroupID: "g", ChannelID: "c", CredentialVersion: "v1"}
	var visits []string // handler calls are sequential; each response is drained.
	var visitsMu sync.Mutex
	entry := func(name string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			visitsMu.Lock()
			visits = append(visits, name)
			visitsMu.Unlock()
			if r.Header.Get("Authorization") != "Bearer synthetic-original-key" {
				t.Error("fallback changed credential scope")
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"id":"resp-owned"}`)
		}))
	}
	first, second := entry("cf", 503), entry("direct", 200)
	defer first.Close()
	defer second.Close()
	lookup := func(gatewaycontract.Reference) ([]gatewaycontract.Resource, error) {
		return []gatewaycontract.Resource{{Kind: "response", ID: "resp-owned", AccountID: "a", KeyID: "k", Binding: binding, CredentialScope: "upstream-key", CreationID: "creation-a"}}, nil
	}
	bound, err := gatewaycontract.ResolveReferences(actor, []gatewaycontract.Reference{{Kind: "response", ID: "resp-owned"}}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	candidate := gatewaycontract.Candidate{Binding: binding, Enabled: true, Authorized: true, Callable: true}
	if _, err := gatewaycontract.Candidates(gatewaycontract.Selection{Bound: bound}, []gatewaycontract.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}
	addresses := []string{first.URL, second.URL}
	for i, address := range addresses {
		req, _ := http.NewRequest(http.MethodGet, address+"/v1/responses/resp-owned", nil)
		req.Header.Set("Authorization", "Bearer synthetic-original-key")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode == 200 {
			break
		}
		if i == len(addresses)-1 || !gatewaycontract.MayReplay(gatewaycontract.Endpoint{Replay: "read"}, true, false, false) {
			t.Fatal("same-engine read could not use selected fallback")
		}
	}
	visitsMu.Lock()
	if !slices.Equal(visits, []string{"cf", "direct"}) {
		t.Error("selected address order changed", visits)
	}
	visitsMu.Unlock()
	if gatewaycontract.MayReplay(gatewaycontract.Endpoint{Replay: "write"}, true, false, false) {
		t.Fatal("unknown write result may duplicate on fallback")
	}
	for _, change := range []string{"rotated", "deleted", "no_addresses"} {
		next := candidate
		switch change {
		case "rotated":
			next.CredentialVersion = "v2"
		case "deleted":
			next.Enabled = false
		case "no_addresses":
			next.Callable = false
		}
		if _, err := gatewaycontract.Candidates(gatewaycontract.Selection{Bound: bound}, []gatewaycontract.Candidate{next}); err == nil {
			t.Errorf("%s resource scope silently changed", change)
		}
	}
}

func TestUnregisteredResourceCannotBeClaimedByReadingItFirst(t *testing.T) {
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		_, _ = io.WriteString(w, `{"text":"synthetic-other-owner-context"}`)
	}))
	defer upstream.Close()
	for _, actor := range []gatewaycontract.Principal{{AccountID: "a", KeyID: "ka", Active: true}, {AccountID: "b", KeyID: "kb", Active: true}} {
		bound, err := gatewaycontract.ResolveReferences(actor, []gatewaycontract.Reference{{Kind: "response", ID: "unknown-shared-response"}}, func(gatewaycontract.Reference) ([]gatewaycontract.Resource, error) { return nil, nil })
		if err == nil {
			res, err := http.Get(upstream.URL + "/v1/responses/unknown-shared-response")
			if err == nil {
				_ = res.Body.Close()
			}
			t.Fatal("unregistered shared resource acquired an owner", bound)
		}
	}
	if reached.Load() != 0 {
		t.Fatal("an ownership guess reached the shared upstream resource")
	}
}
