package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type resourceFixture struct {
	serveStore
	candidate   Candidate
	rows        []Resource
	key         KeyAuth
	saveErr     error
	itemBatches int
	formats     []channel.Format
}

func (s *resourceFixture) LookupKey(context.Context, []byte) (KeyAuth, error) { return s.key, nil }
func (s *resourceFixture) Candidates(_ context.Context, _ string, format channel.Format) ([]Candidate, error) {
	if s.formats != nil && !slices.Contains(s.formats, format) {
		return nil, nil
	}
	return []Candidate{s.candidate}, nil
}
func (s *resourceFixture) SaveResource(_ context.Context, row Resource) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	for _, v := range s.rows {
		if v.ID == row.ID && v.KeyID == row.KeyID && v.Kind == row.Kind {
			return errors.New("duplicate")
		}
	}
	row.CreatedAt = time.Now()
	s.rows = append(s.rows, row)
	return nil
}
func (s *resourceFixture) LookupResource(_ context.Context, a, k, kind, id string) (Resource, error) {
	for _, row := range s.rows {
		if row.AccountID == a && row.KeyID == k && row.Kind == kind && row.ID == id {
			return row, nil
		}
	}
	return Resource{}, ErrResourceNotFound
}
func (s *resourceFixture) ListResources(_ context.Context, a, k, kind, after string, desc bool, limit int) ([]Resource, error) {
	out := []Resource{}
	for _, row := range s.rows {
		if row.AccountID == a && row.KeyID == k && row.Kind == kind {
			out = append(out, row)
		}
	}
	if desc {
		slices.Reverse(out)
	}
	if after != "" {
		for i, row := range out {
			if row.ID == after {
				out = out[i+1:]
				break
			}
		}
	}
	return out[:min(len(out), limit)], nil
}
func (s *resourceFixture) DeleteResource(_ context.Context, a, k, kind, id string) error {
	s.rows = slices.DeleteFunc(s.rows, func(v Resource) bool { return v.AccountID == a && v.KeyID == k && v.Kind == kind && v.ID == id })
	return nil
}

type resourceModels struct{}

func (resourceModels) Get(_ context.Context, id string) (catalog.Model, error) {
	return catalog.Model{ID: id, Enabled: true, InputPrice: 1_000_000_000, OutputPrice: 2_000_000_000}, nil
}
func (resourceModels) List(context.Context, bool) ([]catalog.Model, error) {
	m, _ := resourceModels{}.Get(context.Background(), "gpt-test")
	return []catalog.Model{m}, nil
}
func (s *resourceFixture) OnlineModels(context.Context) ([]OnlineModel, error) {
	formats := s.formats
	if formats == nil {
		formats = channel.Formats
	}
	return []OnlineModel{{ID: "gpt-test", Formats: formats}}, nil
}
func newResourceFixture(t *testing.T, h http.HandlerFunc) (*Engine, *resourceFixture) {
	t.Helper()
	run := newCredentialRun(t, 200)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	s := &resourceFixture{key: KeyAuth{KeyID: "key", OwnerID: "consumer", Enabled: true, AccountActive: true}, candidate: Candidate{ChannelID: "upstream", OwnerID: "provider", BaseURL: server.URL, UpstreamModel: "upstream-model", MultiplierNano: ledger.UnitRate, Credential: run.seal("upstream", "original-secret")}}
	e := newServeFixture(t, 10, defaultRouting{}).engine
	e.Store, e.Resources, e.Keyring, e.Catalog = s, s, run.keyring, resourceModels{}
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 1}, outbound: fakeOutbound{transport: http.DefaultTransport}})
	return e, s
}
func resourceRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+apikey.Prefix+"test")
	return r
}
func TestStoredResponseOwnershipAndCredentialPinning(t *testing.T) {
	var paths []string
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer original-secret" {
			t.Error("credential changed")
		}
		if r.Header.Get("OpenAI-Project") != "original-project" {
			t.Error("project changed")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_owned","object":"response","status":"completed","usage":{"input_tokens":10,"output_tokens":3}}`)
	})
	r := resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","store":true,"input":"private"}`)
	r.Header.Set("OpenAI-Project", "original-project")
	w := httptest.NewRecorder()
	e.Serve(w, r, channel.FormatOpenAIResponses, "", false)
	if w.Code != 200 || len(s.rows) != 1 || strings.Contains(string(s.rows[0].Metadata), "private") {
		t.Fatalf("%d %s rows=%+v", w.Code, w.Body.String(), s.rows)
	}
	s.candidate.Credential = channel.EncryptedCredential{}
	for _, change := range []string{"other-key", "other-account", "forbidden-model", "disabled", "valid"} {
		s.key = KeyAuth{KeyID: "key", OwnerID: "consumer", Enabled: true, AccountActive: true}
		switch change {
		case "other-key":
			s.key.KeyID = "other"
		case "other-account":
			s.key.OwnerID = "other"
		case "forbidden-model":
			s.key.AllowedModels = []string{"other"}
		case "disabled":
			s.key.Enabled = false
		}
		get := resourceRequest("GET", "/v1/responses/resp_owned", "")
		get.SetPathValue("responseID", "resp_owned")
		get.Header.Set("OpenAI-Project", "attack-project")
		out := httptest.NewRecorder()
		e.ServeOpenAIResponse(out, get)
		if change == "valid" && out.Code != 200 || change != "valid" && out.Code == 200 {
			t.Fatalf("%s: %d %s", change, out.Code, out.Body.String())
		}
	}
	if len(paths) != 2 || len(s.finished) != 1 {
		t.Fatalf("requests=%v bills=%d", paths, len(s.finished))
	}
}
func TestStoredChatListNeverExposesAccountWideUpstreamList(t *testing.T) {
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/chat/completions" {
			t.Fatal("account-wide list was requested")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chat_owned","object":"chat.completion","metadata":{"topic":"test"},"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
	})
	e.Serve(httptest.NewRecorder(), resourceRequest("POST", "/v1/chat/completions", `{"model":"gpt-test","store":true}`), channel.FormatOpenAIChat, "", false)
	foreign := s.rows[0]
	foreign.ID = "chat_foreign"
	foreign.KeyID = "other-key"
	s.rows = append(s.rows, foreign)
	out := httptest.NewRecorder()
	e.ListStoredChats(out, resourceRequest("GET", "/v1/chat/completions?metadata[topic]=test", ""))
	if out.Code != 200 || strings.Contains(out.Body.String(), "chat_foreign") || !strings.Contains(out.Body.String(), "chat_owned") {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
}
func TestMultipartImageSplicesOnlyModelAndRejectsAmbiguity(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "gpt-test")
	_ = writer.WriteField("prompt", "change background")
	part, _ := writer.CreateFormFile("image", "source.png")
	_, _ = part.Write([]byte{0, 1, 2, 255, 0})
	_ = writer.Close()
	raw := body.Bytes()
	facts, span, err := multipartFacts(raw, writer.FormDataContentType())
	if err != nil || facts.Model != "gpt-test" {
		t.Fatal(facts, err)
	}
	replacement := applySplices(raw, []splice{{start: span.start, end: span.end, value: []byte("upstream-model")}})
	if !bytes.Equal(replacement, bytes.Replace(raw, []byte("gpt-test"), []byte("upstream-model"), 1)) {
		t.Fatal("image bytes changed")
	}
	duplicate := bytes.Replace(raw, []byte("name=\"prompt\""), []byte("name=\"model\""), 1)
	if _, _, err := multipartFacts(duplicate, writer.FormDataContentType()); err == nil {
		t.Fatal("duplicate model accepted")
	}
}
func TestOpenAIImageOperationsShareOneProtocolAndKeepTheirPaths(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/images/variations"} {
		t.Run(path, func(t *testing.T) {
			e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("path = %s", r.URL.Path)
				}
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), `"model":"upstream-model"`) {
					t.Error(string(raw))
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"data":[],"usage":{"input_tokens":10,"output_tokens":20}}`)
			})
			s.formats = []channel.Format{channel.FormatOpenAIImages}
			out := httptest.NewRecorder()
			e.Serve(out, resourceRequest("POST", path, `{"model":"gpt-test","prompt":"test"}`), channel.FormatOpenAIImages, "", false)
			if out.Code != 200 || len(s.finished) != 1 || s.finished[0].Cost == 0 {
				t.Fatalf("%d %s", out.Code, out.Body.String())
			}
		})
	}
}
func TestLargeImagesKeepUsageWithoutBufferingImageStrings(t *testing.T) {
	for _, stream := range []bool{false, true} {
		raw := `{"data":[{"b64_json":"` + strings.Repeat("A", maxJSONBuffer+100) + `"}],"usage":{"input_tokens":30,"input_tokens_details":{"text_tokens":10,"image_tokens":20},"output_tokens":40}}`
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
			raw = "data: " + raw + "\n\n"
		}
		o := NewObserver(channel.FormatOpenAIImages, stream, contentType)
		for off := 0; off < len(raw); off += 19 {
			o.Write([]byte(raw[off:min(off+19, len(raw))]), time.Now())
		}
		got := o.Finish()
		if !got.Found || got.Usage.InputTokens != 30 || got.Usage.OutputTokens != 40 || got.Usage.Detail.Tokens["input_image"] != 20 || got.Usage.Detail.Tokens["output_image"] != 40 || o.overflow {
			t.Fatalf("stream=%v %+v", stream, got)
		}
		if len(o.buffer) > 4096 || len(o.media.output) > 4096 {
			t.Fatal("image retained")
		}
	}
}
func TestCompactAndCountReuseProtocolWithoutInventingCharges(t *testing.T) {
	for _, path := range []string{"/v1/responses/compact", "/v1/responses/input_tokens"} {
		e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"model":"upstream-model"`) {
				t.Error(string(raw))
			}
			w.Header().Set("Content-Type", "application/json")
			if path == "/v1/responses/input_tokens" {
				io.WriteString(w, `{"object":"response.input_tokens","input_tokens":123}`)
			} else {
				io.WriteString(w, `{"id":"resp_compact","object":"response.compaction","output":[{"id":"cmp_owned","type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":100,"output_tokens":10}}`)
			}
		})
		s.formats = []channel.Format{channel.FormatOpenAIResponses}
		w := httptest.NewRecorder()
		e.Serve(w, resourceRequest("POST", path, `{"model":"gpt-test","input":[]}`), channel.FormatOpenAIResponses, "", false)
		if w.Code != 200 || len(s.finished) != 1 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		finish := s.finished[0]
		if path == "/v1/responses/input_tokens" && (finish.Cost != 0 || finish.Outcome != OutcomeSucceeded) {
			t.Fatal(finish)
		}
		if path == "/v1/responses/compact" && finish.Cost == 0 {
			t.Fatal("compact usage not charged")
		}
	}
}
func TestResourceRegistrationFailureDoesNotExposeIdentifier(t *testing.T) {
	for _, stream := range []bool{false, true} {
		e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_hidden\",\"status\":\"in_progress\"}}\n\n")
			} else {
				io.WriteString(w, `{"id":"resp_hidden","status":"completed"}`)
			}
		})
		s.saveErr = errors.New("db unavailable")
		snapshot := e.settingsSnap.Load()
		snapshot.value.DefaultCooldownFailures = 1
		snapshot.value.DefaultCooldownSeconds = 60
		body := `{"model":"gpt-test","store":true}`
		if stream {
			body = `{"model":"gpt-test","store":true,"stream":true}`
		}
		w := httptest.NewRecorder()
		e.Serve(w, resourceRequest("POST", "/v1/responses", body), channel.FormatOpenAIResponses, "", false)
		if !e.Runtime.CooldownUntil("upstream", e.Now()).IsZero() {
			t.Fatal("local registry failure cooled down upstream")
		}
		if strings.Contains(w.Body.String(), "resp_hidden") || w.Code < 500 {
			t.Fatalf("stream=%v: %d %s", stream, w.Code, w.Body.String())
		}
	}
}
func TestGetModelUsesSameListItemAndAliasVisibility(t *testing.T) {
	e, s := newResourceFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected upstream request") })
	s.key.Aliases = map[string]string{"alias": "gpt-test"}
	r := resourceRequest("GET", "/v1/models/alias", "")
	r.SetPathValue("model", "alias")
	w := httptest.NewRecorder()
	e.GetModel(w, r)
	var model map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &model)
	if w.Code != 200 || model["id"] != "alias" || model["object"] != "model" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	s.key.AllowedModels = []string{"other"}
	w = httptest.NewRecorder()
	e.GetModel(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func (s *resourceFixture) SaveResourceItems(ctx context.Context, parent Resource, ids []string) error {
	s.itemBatches++
	if s.saveErr != nil {
		return s.saveErr
	}
	for _, id := range ids {
		if _, err := s.LookupResource(ctx, parent.AccountID, parent.KeyID, "item", id); err == nil {
			continue
		}
		row := parent
		row.Kind = "item"
		row.ID = id
		if err := s.SaveResource(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func TestConversationItemsAndOpaqueReferencesStayWithOriginalScope(t *testing.T) {
	var lastPath string
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/conversations":
			io.WriteString(w, `{"id":"conv_owned","object":"conversation","items":[]}`)
		case "/v1/conversations/conv_owned/items":
			io.WriteString(w, `{"object":"list","data":[{"id":"msg_owned","type":"message","role":"user","content":[]}]}`)
		default:
			io.WriteString(w, `{"id":"resp_conversation","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":1}}`)
		}
	})
	s.formats = []channel.Format{channel.FormatOpenAIResponses}
	create := httptest.NewRecorder()
	e.Serve(create, resourceRequest("POST", "/v1/conversations", `{"items":[]}`), channel.FormatOpenAIResponses, "", false)
	if create.Code != 200 || len(s.rows) != 1 || s.finished[0].Cost != 0 {
		t.Fatalf("%d %s", create.Code, create.Body.String())
	}
	add := resourceRequest("POST", "/v1/conversations/conv_owned/items", `{"items":[{"type":"message","role":"user","content":"hello"}]}`)
	add.SetPathValue("resourceID", "conv_owned")
	out := httptest.NewRecorder()
	e.ServeResource(out, add, "conversation")
	if out.Code != 200 || len(s.rows) != 2 {
		t.Fatalf("%d %s rows=%d", out.Code, out.Body.String(), len(s.rows))
	}
	for _, input := range []string{`"conversation":"conv_owned","input":"continue"`, `"input":[{"type":"item_reference","id":"msg_owned"}]`} {
		out = httptest.NewRecorder()
		e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test",`+input+`,"store":false}`), channel.FormatOpenAIResponses, "", false)
		if out.Code != 200 || lastPath != "/v1/responses" {
			t.Fatalf("%d %s", out.Code, out.Body.String())
		}
	}
	previous := len(s.finished)
	out = httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","input":[{"type":"item_reference","id":"msg_foreign"}]}`), channel.FormatOpenAIResponses, "", false)
	if out.Code != 404 || len(s.finished) != previous+1 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
}
func TestStoredResponseItemReferencesAreRegisteredWithoutContent(t *testing.T) {
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_items","status":"completed","output":[{"id":"msg_items","type":"message","content":[{"type":"output_text","text":"sensitive answer"}]}],"usage":{"input_tokens":10,"output_tokens":2}}`)
	})
	out := httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","input":"private request"}`), channel.FormatOpenAIResponses, "", false)
	if out.Code != 200 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
	item, err := e.ownedResource(context.Background(), s.key, "item", "msg_items")
	if err != nil || bytes.Contains(item.Metadata, []byte("sensitive answer")) || bytes.Contains(item.Metadata, []byte("private request")) {
		t.Fatal(item, err)
	}
}

func TestCompactionStateRetainsItsUpstreamScope(t *testing.T) {
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses/compact" {
			io.WriteString(w, `{"id":"resp_compact","object":"response.compaction","output":[{"id":"cmp_owned","type":"compaction","encrypted_content":"opaque-state"}],"usage":{"input_tokens":20,"output_tokens":5}}`)
		} else {
			io.WriteString(w, `{"id":"resp_next","status":"completed","usage":{"input_tokens":10,"output_tokens":1}}`)
		}
	})
	out := httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses/compact", `{"model":"gpt-test","input":[]}`), channel.FormatOpenAIResponses, "", false)
	if out.Code != 200 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
	for _, row := range s.rows {
		if bytes.Contains(row.Metadata, []byte("opaque-state")) {
			t.Fatal("compaction content persisted")
		}
	}
	input := `{"model":"gpt-test","store":false,"input":[{"id":"cmp_owned","type":"compaction","encrypted_content":"opaque-state"}]}`
	out = httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses", input), channel.FormatOpenAIResponses, "", false)
	if out.Code != 200 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
	s.key.KeyID = "foreign-key"
	out = httptest.NewRecorder()
	e.Serve(out, resourceRequest("POST", "/v1/responses", input), channel.FormatOpenAIResponses, "", false)
	if out.Code != 404 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
}

type oneByteResourceBody struct{ io.Reader }

func (b oneByteResourceBody) Read(p []byte) (int, error) { return b.Reader.Read(p[:min(len(p), 1)]) }
func (oneByteResourceBody) Close() error                 { return nil }

type resourceTransport func(*http.Request) (*http.Response, error)

func (f resourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type registeredDelivery struct {
	*httptest.ResponseRecorder
	check func()
}

func (w registeredDelivery) Write(p []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(p)
}
func TestStoredChatPersistsBeforeAnyFragmentedStreamDelivery(t *testing.T) {
	e, s := newResourceFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected HTTP upstream") })
	payload := "data: {\"id\":\"chat_fragmented\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"chat_fragmented\",\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n" + "data: [DONE]\n\n"
	e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 1}, outbound: fakeOutbound{transport: resourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: oneByteResourceBody{strings.NewReader(payload)}, Request: r}, nil
	})}})
	recorder := httptest.NewRecorder()
	writer := registeredDelivery{recorder, func() {
		if len(s.rows) != 1 || s.rows[0].ID != "chat_fragmented" {
			t.Fatal("identifier delivered before registration")
		}
	}}
	e.Serve(writer, resourceRequest("POST", "/v1/chat/completions", `{"model":"gpt-test","store":true,"stream":true,"stream_options":{"include_usage":true}}`), channel.FormatOpenAIChat, "", false)
	if recorder.Code != 200 || recorder.Body.String() != payload || len(s.finished) != 1 || s.finished[0].Usage.OutputTokens != 2 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestBackgroundOutputItemsCanBeReferencedAfterReadOrPoll(t *testing.T) {
	for _, mode := range []string{"get", "stream", "poll"} {
		t.Run(mode, func(t *testing.T) {
			completed := `{"id":"resp_job","status":"completed","output":[{"id":"msg_background","type":"message","content":[{"type":"output_text","text":"private output"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`
			e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/responses/resp_job" {
					if r.Header.Get("OpenAI-Project") != "original-project" {
						t.Error("read changed project")
					}
					if r.URL.Query().Get("stream") == "true" {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+completed+"}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, completed)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"resp_next","status":"completed","usage":{"input_tokens":5,"output_tokens":1}}`)
			})
			model, _ := resourceModels{}.Get(context.Background(), "gpt-test")
			metadata := backgroundMetadata{Started: time.Now(), Key: s.key, Candidate: s.candidate, Model: model, ModelID: "gpt-test", Hints: billingHints{Background: true}, Attempts: []Attempt{{}}, Headers: http.Header{"Openai-Project": []string{"original-project"}}}
			metadata.Candidate.RateNano = ledger.UnitRate
			raw, _ := json.Marshal(metadata)
			job := BackgroundJob{CallID: "background-call", AccountID: s.key.OwnerID, KeyID: s.key.KeyID, ChannelID: s.candidate.ChannelID, ResponseID: "resp_job", Metadata: raw, Deadline: time.Now().Add(time.Hour), Settled: mode != "poll"}
			e.Jobs = &backgroundFixture{jobs: map[string]BackgroundJob{job.CallID: job}}
			if mode == "stream" {
				e.settingsSnap.Store(&settingsSnapshot{value: settings.Settings{DefaultMaxAttempts: 1}, outbound: fakeOutbound{transport: resourceTransport(func(r *http.Request) (*http.Response, error) {
					response, err := http.DefaultTransport.RoundTrip(r)
					if err == nil {
						response.Body = oneByteResourceBody{response.Body}
					}
					return response, err
				})}})
			}
			if mode == "poll" {
				s.saveErr = errors.New("registry unavailable")
				e.pollBackgroundJob(context.Background(), job)
				if len(s.finished) != 0 {
					t.Fatal("poll settled despite registration failure")
				}
				s.saveErr = nil
				e.pollBackgroundJob(context.Background(), job)
			} else {
				path := "/v1/responses/resp_job"
				if mode == "stream" {
					path += "?stream=true"
				}
				r := resourceRequest("GET", path, "")
				r.SetPathValue("responseID", job.ResponseID)
				r.Header.Set("OpenAI-Project", "other-project")
				out := httptest.NewRecorder()
				e.ServeOpenAIResponse(out, r)
				expected := completed
				if mode == "stream" {
					expected = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + completed + "}\n\n"
				}
				if out.Code != 200 || out.Body.String() != expected {
					t.Fatalf("read: %d %s", out.Code, out.Body.String())
				}
				if s.itemBatches != 1 {
					t.Fatalf("read issued %d item batches", s.itemBatches)
				}
				again := httptest.NewRecorder()
				e.ServeOpenAIResponse(again, r.Clone(context.Background()))
				if again.Code != 200 || again.Body.String() != expected {
					t.Fatalf("repeat: %d %s", again.Code, again.Body.String())
				}
			}
			if mode != "poll" && len(s.finished) != 0 {
				t.Fatal("read charged again")
			}
			if mode == "poll" && (len(s.finished) != 1 || s.finished[0].Cost == 0) {
				t.Fatal("poll did not settle reliable usage once")
			}
			row, err := s.LookupResource(context.Background(), s.key.OwnerID, s.key.KeyID, "item", "msg_background")
			if err != nil {
				t.Fatalf("output item not registered after %s: %v", mode, err)
			}
			if bytes.Contains(row.Metadata, []byte("private output")) {
				t.Fatal("output content persisted")
			}
			var route resourceRoute
			if json.Unmarshal(row.Metadata, &route) != nil || route.Headers.Get("OpenAI-Project") != "original-project" {
				t.Fatal("original project lost")
			}
			for _, foreign := range []bool{false, true} {
				if foreign {
					s.key.KeyID = "other-key"
				}
				out := httptest.NewRecorder()
				e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","store":false,"input":[{"type":"item_reference","id":"msg_background"}]}`), channel.FormatOpenAIResponses, "", false)
				want := 200
				if foreign {
					want = 404
				}
				if out.Code != want {
					t.Fatalf("resume foreign=%v: %d %s", foreign, out.Code, out.Body.String())
				}
			}
		})
	}
}

func TestRetrievedSingleConversationItemCanBeReferenced(t *testing.T) {
	e, s := newResourceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/conversations":
			io.WriteString(w, `{"id":"conv_owned","object":"conversation"}`)
		case "/v1/conversations/conv_owned/items/msg_single":
			io.WriteString(w, `{"id":"msg_single","type":"message","role":"user","content":[{"type":"input_text","text":"private item"}]}`)
		default:
			io.WriteString(w, `{"id":"resp_next","status":"completed","usage":{"input_tokens":5,"output_tokens":1}}`)
		}
	})
	e.Serve(httptest.NewRecorder(), resourceRequest("POST", "/v1/conversations", `{"items":[]}`), channel.FormatOpenAIResponses, "", false)
	r := resourceRequest("GET", "/v1/conversations/conv_owned/items/msg_single", "")
	r.SetPathValue("resourceID", "conv_owned")
	r.SetPathValue("itemID", "msg_single")
	s.saveErr = errors.New("registry unavailable")
	denied := httptest.NewRecorder()
	e.ServeResource(denied, r.Clone(context.Background()), "conversation")
	if denied.Code != 503 || strings.Contains(denied.Body.String(), "msg_single") {
		t.Fatalf("registration failure exposed item: %d %s", denied.Code, denied.Body.String())
	}
	s.saveErr = nil
	out := httptest.NewRecorder()
	e.ServeResource(out, r, "conversation")
	if out.Code != 200 {
		t.Fatalf("read: %d %s", out.Code, out.Body.String())
	}
	again := httptest.NewRecorder()
	e.ServeResource(again, r.Clone(context.Background()), "conversation")
	if again.Code != 200 || again.Body.String() != out.Body.String() {
		t.Fatal("repeat item retrieval changed response")
	}
	row, err := s.LookupResource(context.Background(), s.key.OwnerID, s.key.KeyID, "item", "msg_single")
	if err != nil {
		t.Fatalf("single item not registered: %v", err)
	}
	if bytes.Contains(row.Metadata, []byte("private item")) {
		t.Fatal("item content persisted")
	}
	for _, foreign := range []bool{false, true} {
		if foreign {
			s.key.KeyID = "other-key"
		}
		out = httptest.NewRecorder()
		e.Serve(out, resourceRequest("POST", "/v1/responses", `{"model":"gpt-test","store":false,"input":[{"type":"item_reference","id":"msg_single"}]}`), channel.FormatOpenAIResponses, "", false)
		want := 200
		if foreign {
			want = 404
		}
		if out.Code != want {
			t.Fatalf("resume foreign=%v: %d %s", foreign, out.Code, out.Body.String())
		}
	}
}
