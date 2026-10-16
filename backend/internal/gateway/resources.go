package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	timeout := resolveTimeouts(defaults, routing.Pref{}).withChannel(route.Candidate.Advanced).request
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > e.CallTimeLimit {
		timeout = e.CallTimeLimit
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
	if !e.acquireFollowup(w) {
		return
	}
	defer func() { <-e.followupSlots }()
	body, err := readRequestBody(w, r)
	if err != nil {
		WriteError(w, DialectOpenAI, 413, "request_too_large", "请求体过大", "")
		return
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
