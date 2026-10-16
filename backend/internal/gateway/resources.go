package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

var ErrResourceNotFound = errors.New("resource not found")

// Resource contains only authorization/routing facts. Never add arbitrary
// upstream fields, request metadata, messages or content here.
type Resource struct {
	AccountID, KeyID, Kind, ID, CallID, ChannelID, ModelID string
	Metadata                                               []byte
	CreatedAt                                              time.Time
}
type ResourceStore interface {
	SaveResource(context.Context, Resource) error
	SaveResourceItems(context.Context, Resource, []string) error
	LookupResource(context.Context, string, string, string, string) (Resource, error)
	ListResources(context.Context, string, string, string, string, bool, int) ([]Resource, error)
	DeleteResource(context.Context, string, string, string, string) error
}
type resourceRoute struct {
	Candidate Candidate
	Headers   http.Header
}

func protocolHeaders(h http.Header) http.Header {
	out := http.Header{}
	for _, name := range []string{"OpenAI-Beta", "OpenAI-Organization", "OpenAI-Project"} {
		if v := h.Get(name); v != "" && len(v) <= 1024 {
			out.Set(name, v)
		}
	}
	return out
}
func resourceKind(f channel.Format) string {
	switch f {
	case channel.FormatOpenAIChat:
		return "chat"
	case channel.FormatOpenAIResponses:
		return "response"
	case channel.FormatOpenAIConversations:
		return "conversation"
	case channel.FormatOpenAICompact:
		return "compaction"
	}
	return ""
}
func (e *Engine) keepResource(ctx context.Context, call *callState, candidate Candidate, id string) error {
	if e.Resources == nil || call.resourceKind == "" {
		return nil
	}
	if !responseIDPattern.MatchString(id) {
		return errors.New("invalid upstream resource id")
	}
	raw, err := json.Marshal(resourceRoute{Candidate: candidate, Headers: call.upstreamHeaders})
	if err != nil || len(raw) > 65536 {
		return errors.New("resource route exceeds budget")
	}
	return e.Resources.SaveResource(ctx, Resource{AccountID: call.key.OwnerID, KeyID: call.key.KeyID, Kind: call.resourceKind, ID: id, CallID: call.id, ChannelID: candidate.ChannelID, ModelID: call.modelID, Metadata: raw})
}
func (e *Engine) resourceKey(w http.ResponseWriter, r *http.Request) (KeyAuth, authLocation, bool) {
	key, location, ok := e.authenticate(w, r)
	if !ok {
		return key, location, false
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		WriteError(w, DialectOpenAI, 401, "invalid_api_key", "API Key 或账号已停用或过期", "")
		return key, location, false
	}
	if e.Resources == nil {
		WriteError(w, DialectOpenAI, 503, "resources_unavailable", "资源服务暂不可用", "")
		return key, location, false
	}
	return key, location, true
}
func (e *Engine) ownedResource(ctx context.Context, key KeyAuth, kind, id string) (Resource, error) {
	if !responseIDPattern.MatchString(id) {
		return Resource{}, ErrResourceNotFound
	}
	row, err := e.Resources.LookupResource(ctx, key.OwnerID, key.KeyID, kind, id)
	if err != nil {
		return row, err
	}
	if len(key.AllowedModels) > 0 && !containsString(key.AllowedModels, row.ModelID) {
		return Resource{}, ErrResourceNotFound
	}
	return row, nil
}
func resourceError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrResourceNotFound) {
		WriteError(w, DialectOpenAI, 404, "resource_not_found", "资源不存在或不可访问", "")
		return
	}
	WriteError(w, DialectOpenAI, 503, "resource_unavailable", "资源暂不可用", "")
}

// resourceExchange uses the immutable creation credential and upstream protocol
// scope, but current platform authentication. It never re-routes a resource.
func (e *Engine) resourceExchange(ctx context.Context, r *http.Request, row Resource, location authLocation, path string, body []byte) (*http.Response, string, context.CancelFunc, error) {
	var route resourceRoute
	if err := json.Unmarshal(row.Metadata, &route); err != nil {
		return nil, "", nil, err
	}
	secret, err := e.Keyring.OpenCredential(route.Candidate.ChannelID, route.Candidate.Credential)
	if err != nil {
		return nil, "", nil, err
	}
	defaults, policy, err := e.Settings(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	timeouts := resolveTimeouts(defaults, routing.Pref{}).withChannel(route.Candidate.Advanced)
	timeout := timeouts.request
	streaming := r.URL.Query().Get("stream") == "true"
	if streaming {
		timeout = e.CallTimeLimit
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > e.CallTimeLimit {
		timeout = e.CallTimeLimit
	}
	ctx, cancelContext := context.WithTimeout(ctx, timeout)
	var clock *time.Timer
	if streaming && timeouts.ttft > 0 {
		clock = time.AfterFunc(timeouts.ttft, cancelContext)
	}
	cancel := func() {
		if clock != nil {
			clock.Stop()
		}
		cancelContext()
	}
	target, err := UpstreamURL(route.Candidate.BaseURL, path, r.URL.RawQuery, channel.FormatOpenAIResponses, "", location, secret)
	if err != nil {
		cancel()
		return nil, "", nil, err
	}
	client, err := policy.GatewayClient(ctx, route.Candidate.BaseURL)
	if err != nil {
		cancel()
		return nil, "", nil, err
	}
	request, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, "", nil, err
	}
	headers := r.Header.Clone()
	// Client-controlled project/organization must not select another namespace.
	for _, name := range []string{"OpenAI-Beta", "OpenAI-Organization", "OpenAI-Project"} {
		headers.Del(name)
	}
	for name, values := range route.Headers {
		headers[name] = values
	}
	request.Header = UpstreamHeaders(headers, location, secret, route.Candidate.Advanced)
	response, err := client.Do(request)
	if err != nil {
		cancel()
		return nil, "", nil, errors.New("resource upstream exchange failed")
	}
	if streaming {
		response.Body = &resourceStreamBody{ReadCloser: response.Body, clock: clock, idle: timeouts.idle, cancel: cancelContext}
	}
	return response, secret, cancel, nil
}

func (e *Engine) ServeResource(w http.ResponseWriter, r *http.Request, kind string) {
	key, location, ok := e.resourceKey(w, r)
	if !ok {
		return
	}
	id := r.PathValue("resourceID")
	if id == "" {
		id = r.PathValue("responseID")
	}
	row, err := e.ownedResource(r.Context(), key, kind, id)
	if err != nil {
		resourceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-AIHub-Request-Id", row.CallID)
	if !e.acquireFollowup(w) {
		return
	}
	defer func() { <-e.followupSlots }()
	body, err := readRequestBody(w, r)
	if err != nil {
		WriteError(w, DialectOpenAI, 413, "request_too_large", "请求体过大", "")
		return
	}
	if r.Method == http.MethodPost && kind == "conversation" {
		_, scan, err := ScanRequest(body)
		if err != nil {
			WriteError(w, DialectOpenAI, 400, "invalid_request", "请求必须为 JSON 对象", "")
			return
		}
		refs, err := inputReferences(body, scan)
		if err != nil {
			WriteError(w, DialectOpenAI, 400, "invalid_request", "条目引用无效", "")
			return
		}
		for _, id := range refs {
			item, err := e.ownedResource(r.Context(), key, "item", id)
			if err != nil || !sameResourceScope(row, item) {
				resourceError(w, ErrResourceNotFound)
				return
			}
		}
	}
	// Child item IDs and pagination cursors stay within the authorized parent.
	if item := r.PathValue("itemID"); item != "" && !responseIDPattern.MatchString(item) {
		resourceError(w, ErrResourceNotFound)
		return
	}
	response, secret, cancel, err := e.resourceExchange(r.Context(), r, row, location, r.URL.Path, body)
	if err != nil {
		resourceError(w, err)
		return
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, maxFailureBody))
		newUpstreamFailure(response, payload, secret).write(w, "")
		return
	}
	if r.Method == http.MethodDelete && (kind != "conversation" || r.PathValue("itemID") == "") {
		// A failure to forget a route is safe: every later operation still checks
		// ownership and the upstream remains authoritative for deletion.
		if r.PathValue("itemID") == "" {
			_ = e.Resources.DeleteResource(r.Context(), key.OwnerID, key.KeyID, kind, id)
		}
	}
	if kind == "conversation" || strings.HasSuffix(r.URL.Path, "/input_items") {
		payload, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes+1))
		if err != nil || len(payload) > maxRequestBytes || !json.Valid(payload) {
			resourceError(w, errors.New("resource response exceeds budget"))
			return
		}
		if ids := itemIDs(payload); len(ids) > 0 {
			if err := e.Resources.SaveResourceItems(r.Context(), row, ids); err != nil {
				resourceError(w, err)
				return
			}
		}
		response.Body = io.NopCloser(bytes.NewReader(payload))
	}
	for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
		w.Header()[name] = values
	}
	w.WriteHeader(response.StatusCode)
	buffer := getChunk()
	defer putChunk(buffer)
	controller := http.NewResponseController(w)
	for {
		n, err := response.Body.Read(*buffer)
		if n > 0 {
			_ = controller.SetWriteDeadline(e.Now().Add(clientWriteLimit))
			if _, writeErr := w.Write((*buffer)[:n]); writeErr != nil {
				return
			}
			if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
				_ = controller.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
func (e *Engine) acquireFollowup(w http.ResponseWriter) bool {
	select {
	case e.followupSlots <- struct{}{}:
		return true
	default:
		WriteError(w, DialectOpenAI, 429, "too_many_resource_reads", "资源操作过多，请稍后重试", "")
		return false
	}
}

// ListStoredChats builds a platform-owned page. The upstream's account-wide
// list must never be exposed when multiple consumers share its credential.
// At most 1000 owned candidates and 100 returned objects per operation; the
// whole exchange has a 30s budget. Exhaustion is explicit, never a partial page
// presented as a complete result. No response content is persisted.
func (e *Engine) ListStoredChats(w http.ResponseWriter, r *http.Request) {
	key, location, ok := e.resourceKey(w, r)
	if !ok {
		return
	}
	if !e.acquireFollowup(w) {
		return
	}
	defer func() { <-e.followupSlots }()
	q := r.URL.Query()
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			WriteError(w, DialectOpenAI, 400, "invalid_request", "limit 必须为 1～100", "")
			return
		}
		limit = n
	}
	desc := q.Get("order") != "asc"
	if order := q.Get("order"); order != "" && order != "asc" && order != "desc" {
		WriteError(w, DialectOpenAI, 400, "invalid_request", "order 无效", "")
		return
	}
	after := q.Get("after")
	if after != "" {
		if _, err := e.ownedResource(r.Context(), key, "chat", after); err != nil {
			resourceError(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := e.Resources.ListResources(ctx, key.OwnerID, key.KeyID, "chat", after, desc, 1001)
	if err != nil {
		resourceError(w, err)
		return
	}
	data := make([]json.RawMessage, 0, limit+1)
	retainedBytes := 0
	for at, row := range rows {
		if at >= 1000 {
			WriteError(w, DialectOpenAI, 503, "resource_scan_limit", "筛选扫描达到上限，请缩小筛选范围", "")
			return
		}
		if len(key.AllowedModels) > 0 && !slices.Contains(key.AllowedModels, row.ModelID) {
			continue
		}
		if model := q.Get("model"); model != "" && model != row.ModelID && key.Aliases[model] != row.ModelID {
			continue
		}
		get := r.Clone(ctx)
		get.Method = http.MethodGet
		get.URL = &url.URL{}
		response, secret, stop, err := e.resourceExchange(ctx, get, row, location, "/v1/chat/completions/"+row.ID, nil)
		if err != nil {
			resourceError(w, err)
			return
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes+1))
		response.Body.Close()
		stop()
		if response.StatusCode == 404 {
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			newUpstreamFailure(response, payload, secret).write(w, "")
			return
		}
		if readErr != nil || len(payload) > maxRequestBytes || !json.Valid(payload) {
			resourceError(w, errors.New("invalid resource body"))
			return
		}
		if !chatMetadataMatches(payload, q) {
			continue
		}
		retainedBytes += len(payload)
		if retainedBytes > maxRequestBytes {
			WriteError(w, DialectOpenAI, 503, "resource_page_limit", "返回内容达到上限，请减小 limit", "")
			return
		}
		data = append(data, payload)
		if len(data) > limit {
			break
		}
	}
	more := len(data) > limit
	if more {
		data = data[:limit]
	}
	var first, last any
	if len(data) > 0 {
		var value struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data[0], &value)
		first = value.ID
		_ = json.Unmarshal(data[len(data)-1], &value)
		last = value.ID
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSONBody(w, map[string]any{"object": "list", "data": data, "has_more": more, "first_id": first, "last_id": last})
}
func chatMetadataMatches(raw []byte, q url.Values) bool {
	var value struct {
		Metadata map[string]string `json:"metadata"`
	}
	for name, values := range q {
		if !strings.HasPrefix(name, "metadata[") || !strings.HasSuffix(name, "]") {
			continue
		}
		if value.Metadata == nil {
			if json.Unmarshal(raw, &value) != nil {
				return false
			}
		}
		if len(values) != 1 || value.Metadata[strings.TrimSuffix(strings.TrimPrefix(name, "metadata["), "]")] != values[0] {
			return false
		}
	}
	return true
}

// Background tasks retain their original polling/settlement semantics. Ordinary
// stored responses use the ownership registry and never create a second bill.
func (e *Engine) ServeOpenAIResponse(w http.ResponseWriter, r *http.Request) {
	if e.Resources == nil {
		e.ServeResponse(w, r, false)
		return
	}
	key, _, ok := e.resourceKey(w, r)
	if !ok {
		return
	}
	if e.Jobs != nil {
		_, err := e.Jobs.LookupBackground(r.Context(), key.OwnerID, key.KeyID, r.PathValue("responseID"))
		if err == nil {
			e.ServeResponse(w, r, false)
			return
		}
		if !errors.Is(err, ErrBackgroundNotFound) {
			resourceError(w, err)
			return
		}
	}
	e.ServeResource(w, r, "response")
}

// Only IDs of items observed under an owned parent can be used as opaque
// item_reference inputs. Otherwise shared upstream credentials would let a
// caller borrow another consumer's history without its parent response ID.
func inputReferences(body []byte, scan objectScan) ([]string, error) {
	member, ok := scan.last("input")
	if !ok {
		member, ok = scan.last("items")
	}
	if !ok {
		return nil, nil
	}
	raw := body[member.start:member.end]
	if !bytes.Contains(raw, []byte("item_reference")) && !bytes.Contains(raw, []byte("compaction")) && !bytes.Contains(raw, []byte(`\u`)) {
		return nil, nil
	}
	var ids []string
	var failure error
	eachElement(raw, func(item []byte) {
		var ref struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(item, &ref) != nil {
			return
		}
		if ref.Type != "item_reference" && ref.Type != "compaction" {
			return
		}
		if len(ids) >= maxBillingItems || !responseIDPattern.MatchString(ref.ID) {
			failure = errors.New("invalid item reference")
			return
		}
		ids = append(ids, ref.ID)
	})
	return ids, failure
}
func itemIDs(raw []byte) []string {
	var envelope struct {
		Output borrowedJSON `json:"output"`
		Items  borrowedJSON `json:"items"`
		Data   borrowedJSON `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	ids := make([]string, 0)
	for _, items := range []borrowedJSON{envelope.Output, envelope.Items, envelope.Data} {
		eachElement(items, func(item []byte) {
			var value struct {
				ID string `json:"id"`
			}
			if len(ids) < maxBillingItems && json.Unmarshal(item, &value) == nil && responseIDPattern.MatchString(value.ID) {
				ids = append(ids, value.ID)
			}
		})
	}
	return ids
}
func (e *Engine) keepItems(ctx context.Context, call *callState, candidate Candidate, id string, ids []string) error {
	if e.Resources == nil || len(ids) == 0 || call.resourceKind == "" {
		return nil
	}
	raw, err := json.Marshal(resourceRoute{Candidate: candidate, Headers: call.upstreamHeaders})
	if err != nil {
		return err
	}
	parent := Resource{AccountID: call.key.OwnerID, KeyID: call.key.KeyID, CallID: call.id, ChannelID: candidate.ChannelID, ModelID: call.modelID, Kind: call.resourceKind, ID: id, Metadata: raw}
	return e.Resources.SaveResourceItems(ctx, parent, ids)
}

func sameResourceScope(a, b Resource) bool {
	var x, y resourceRoute
	if json.Unmarshal(a.Metadata, &x) != nil || json.Unmarshal(b.Metadata, &y) != nil {
		return false
	}
	return x.Candidate.ChannelID == y.Candidate.ChannelID && x.Candidate.BaseURL == y.Candidate.BaseURL && reflect.DeepEqual(x.Candidate.Credential, y.Candidate.Credential) && reflect.DeepEqual(x.Headers, y.Headers)
}

type resourceStreamBody struct {
	io.ReadCloser
	clock  *time.Timer
	idle   time.Duration
	cancel context.CancelFunc
}

func (b *resourceStreamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.idle > 0 {
		if b.clock == nil {
			b.clock = time.AfterFunc(b.idle, b.cancel)
		} else {
			b.clock.Reset(b.idle)
		}
	}
	return n, err
}
func (b *resourceStreamBody) Close() error {
	if b.clock != nil {
		b.clock.Stop()
	}
	return b.ReadCloser.Close()
}
