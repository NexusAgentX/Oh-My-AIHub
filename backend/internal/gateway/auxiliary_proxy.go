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
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	uuidids "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/nativeencoding"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func protocolFormat(protocol string) channel.Format {
	switch protocol {
	case "openai-chat-completions":
		return channel.FormatOpenAIChat
	case "openai-responses", "codex-special":
		return channel.FormatOpenAIResponses
	case "native-messages":
		return channel.FormatAnthropic
	case "native-generate-content":
		return channel.FormatGemini
	case "openai-images":
		return channel.FormatOpenAIImages
	}
	return ""
}
func endpointRequirement(id string) []CapabilityRequirement {
	if required := codexContextRequirements(id); len(required) > 0 {
		return required
	}
	cap := ""
	switch {
	case id == "codex.models":
		cap = "codex-special.RL15/native_catalog"
	case strings.HasPrefix(id, "chat."):
		cap = "openai-chat-completions.stored/owned_list_get_metadata_messages_delete"
	case id == "conversations.get" || id == "conversations.update":
		cap = "openai-responses.state/conversation_metadata"
	case strings.HasPrefix(id, "conversations.items.") && id != "conversations.items.create":
		cap = "openai-responses.state/owned_item_list_get_delete"
	case strings.HasPrefix(id, "conversations."):
		cap = "openai-responses.state/conversation_flow"
	case id == "responses.count":
		cap = "openai-responses.count_compact/native_token_count"
	case id == "responses.compact":
		cap = "openai-responses.count_compact/native_compact_when_applicable"
	case id == "responses.cancel":
		cap = "openai-responses.background/owned_cancel"
	case strings.HasPrefix(id, "responses."):
		cap = "openai-responses.state/owned_response_retrieve_input_items_delete"
	case id == "messages.count":
		cap = "native-messages.count_compact/native_token_count"
	case id == "gemini.countTokens":
		cap = "native-generate-content.cache_tier/native_cached_token_count"
	case strings.HasPrefix(id, "cache."):
		cap = "native-generate-content.cache_tier/owned_explicit_cache_lifecycle"
	}
	if cap == "" {
		return nil
	}
	return []CapabilityRequirement{{Layer: "upstream_protocol", ID: cap}}
}
func endpointPathRef(ep gatewaycontract.Endpoint, r *http.Request) ([]gatewaycontract.Reference, string) {
	var refs []gatewaycontract.Reference
	parent := ""
	add := func(kind, id, p string) {
		if id != "" {
			refs = append(refs, gatewaycontract.Reference{Kind: kind, ID: id, ParentID: p})
		}
	}
	switch {
	case strings.HasPrefix(ep.ID, "chat."):
		add("stored_chat", r.PathValue("completionID"), "")
	case strings.HasPrefix(ep.ID, "responses."):
		add("response", r.PathValue("responseID"), "")
	case strings.HasPrefix(ep.ID, "conversations."):
		parent = r.PathValue("conversationID")
		add("conversation", parent, "")
		add("item", r.PathValue("itemID"), parent)
	case strings.HasPrefix(ep.ID, "cache."):
		if id := r.PathValue("cacheID"); id != "" {
			add("cache", "cachedContents/"+id, "")
		}
	}
	return refs, parent
}
func isResourceList(id string) bool {
	return id == "chat.list" || id == "cache.list" || id == "chat.messages" || id == "responses.items" || id == "conversations.items.list"
}
func resourceListKind(ep gatewaycontract.Endpoint) string {
	if ep.ID == "chat.list" {
		return "stored_chat"
	}
	if ep.ID == "cache.list" {
		return "cache"
	}
	return "item"
}
func nativePath(ep gatewaycontract.Endpoint, r *http.Request, upstreamModel string) string {
	path := ep.Path
	for _, v := range []struct{ old, key string }{{"{response_id}", "responseID"}, {"{conversation_id}", "conversationID"}, {"{item_id}", "itemID"}, {"{completion_id}", "completionID"}, {"{cache_id}", "cacheID"}} {
		path = strings.ReplaceAll(path, v.old, url.PathEscape(r.PathValue(v.key)))
	}
	path = strings.ReplaceAll(path, "{model}", url.PathEscape(upstreamModel))
	return path
}
func auxiliaryURL(c Candidate, path, query string, location authLocation, secret string) (string, error) {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", err
	}
	target, err := UpstreamURL(c.BaseURL, decoded, query, "", "", location, secret)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	u.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + path
	return u.String(), nil
}

// ServeAuxiliary handles the finite, versioned non-compute endpoint matrix. No
// URL from an upstream response becomes a proxy target. All compute operations
// use Serve and its existing synchronous call/ledger pipeline instead.
func (e *Engine) ServeAuxiliary(w http.ResponseWriter, r *http.Request, ep gatewaycontract.Endpoint) {
	fail := func(status int, code, message string) { WriteError(w, DialectOf(r), status, code, message, "") }
	if e.Ready != nil && e.Ready() != nil {
		fail(503, "market_unavailable", "供应商配置正在同步")
		return
	}
	key, location, ok := e.authenticate(w, r)
	if !ok {
		return
	}
	if !key.AccountActive || !keyUsable(key, e.Now()) {
		fail(403, "invalid_api_key", "API Key 或账号已停用")
		return
	}
	if e.Resources == nil {
		fail(503, "resource_service_unavailable", "会话资源服务尚未就绪")
		return
	}
	body, encoding, err := readNativeRequest(w, r)
	if err != nil {
		fail(transportError(err))
		return
	}
	contextBody := body
	if len(contextBody) == 0 && ep.Protocol == "codex-special" {
		contextBody = []byte("{}")
	}
	if len(contextBody) > 0 {
		_, scan, scanErr := ScanRequest(contextBody)
		if scanErr == nil {
			var mappedBody []byte
			r, mappedBody, _, err = e.isolateCodexRequest(r, contextBody, scan, protocolFormat(ep.Protocol), key)
			if err != nil {
				if errors.Is(err, errContextNamespaceUnavailable) {
					fail(transportError(err))
				} else {
					fail(400, "invalid_context", "会话标识或元数据无效")
				}
				return
			}
			if len(body) > 0 {
				body = mappedBody
			}
		}
	}
	var object map[string]json.RawMessage
	if len(body) > 0 && json.Unmarshal(body, &object) != nil {
		fail(400, "invalid_request", "请求体不是合法 JSON 对象")
		return
	}
	if ep.Scope == "context_acl_only" && !validCodexAuxiliaryContext(object) {
		fail(400, "invalid_context", "请求需要有效的会话编号和代理名称")
		return
	}
	refs, parent := endpointPathRef(ep, r)
	pathRefs := append([]gatewaycontract.Reference(nil), refs...)
	var explicitRefs []gatewaycontract.Reference
	if len(body) > 0 {
		inputKey := "input"
		if ep.ID == "conversations.items.create" {
			inputKey = "items"
		}
		protocol := ep.Protocol
		if protocol == "codex-special" {
			protocol = "openai-responses"
		}
		more, e := nativeReferences(protocol, body, inputKey)
		if e != nil {
			fail(400, "invalid_request", "请求包含无效资源引用")
			return
		}
		refs = append(refs, more...)
		explicitRefs = more
	}
	actor := principal(key)
	owned, err := resourcestate.Resolve(r.Context(), e.Resources, actor, refs)
	if err != nil {
		fail(404, "resource_unavailable", "资源不存在、归属冲突或已不可用")
		return
	}
	target := owned
	if len(pathRefs) > 0 && len(pathRefs) != len(refs) {
		target, err = resourcestate.Resolve(r.Context(), e.Resources, actor, pathRefs)
		if err != nil {
			fail(404, "resource_unavailable", "资源不存在或归属冲突")
			return
		}
	}
	// A durable background call already owns its admission and original sealed
	// snapshot. It cannot switch to a replacement credential for a follow-up.
	if target != nil && e.Jobs != nil && (ep.ID == "responses.get" || ep.ID == "responses.cancel") {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		job, err := e.Jobs.LookupBackground(ctx, key.OwnerID, key.KeyID, r.PathValue("responseID"))
		cancel()
		if err == nil {
			// readNativeRequest consumed the caller's body. Restore its native
			// bytes before the frozen follow-up path forwards cancellation.
			wire, encodeErr := nativeencoding.Encode(r.Context(), encoding, body)
			if encodeErr != nil {
				fail(400, "invalid_request", "请求编码无效")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(wire))
			r.ContentLength = int64(len(wire))
			e.serveResponseJob(w, r, key, r.PathValue("responseID"), ep.ID == "responses.cancel", job, target)
			return
		}
		if !errors.Is(err, ErrBackgroundNotFound) {
			fail(503, "response_service_unavailable", "响应状态读取失败")
			return
		}
	}
	model := ""
	_ = json.Unmarshal(object["model"], &model)
	if ep.Protocol == "native-generate-content" {
		model = strings.TrimPrefix(model, "models/")
	}
	if v := r.PathValue("model"); v != "" {
		model = strings.TrimSuffix(v, ":countTokens")
	}
	if model == "" && ep.ID == "chat.list" {
		model = r.URL.Query().Get("model")
	}
	if target, ok := key.Aliases[model]; ok {
		model = target
	}
	if model == "" && owned != nil {
		model = owned.ContextModelID
	}
	if model != "" {
		if !gatewaycontract.ValidModelID(model) || len(key.AllowedModels) > 0 && !slices.Contains(key.AllowedModels, model) {
			fail(403, "model_not_allowed", "该 API Key 不允许使用此模型")
			return
		}
		m, e := e.Catalog.Get(r.Context(), model)
		if e != nil || !m.Enabled {
			fail(404, "model_not_found", "模型不存在或已停用")
			return
		}
	}
	if (ep.Model == "explicit_or_context" || ep.Model == "path") && model == "" {
		fail(400, "model_context_required", "请求需要明确的模型上下文")
		return
	}
	defaults, policy, err := e.Settings(r.Context())
	if err != nil {
		fail(503, "settings_unavailable", "网关配置尚未就绪")
		return
	}
	pref, err := e.Routing.Resolve(r.Context(), key.OwnerID, key.KeyID, model)
	if err != nil {
		fail(503, "routing_unavailable", "路由配置尚未就绪")
		return
	}
	source, ok := e.Store.(AuxiliarySource)
	if !ok {
		fail(503, "candidate_source_unavailable", "辅助请求候选尚未就绪")
		return
	}
	records, err := source.AuxiliaryCandidates(r.Context(), protocolFormat(ep.Protocol))
	if err != nil {
		fail(503, "candidate_source_unavailable", "候选读取失败")
		return
	}
	// Count/list/context operations require their own endpoint capability,
	// not execution of every inference feature described in their input.
	requestedCapabilities := nativeRequirements{protocol: ep.Protocol, capabilities: endpointRequirement(ep.ID)}
	filtered := make([]CandidateRecord, 0, len(records))
	for _, record := range records {
		if missingRequestCapability(record.Candidate, requestedCapabilities, r.Header) == "" {
			filtered = append(filtered, record)
		}
	}
	records = filtered
	selection := AuxiliarySelection{ModelID: model, Format: protocolFormat(ep.Protocol), AllowedModels: key.AllowedModels, Preference: pref, Required: append(endpointRequirement(ep.ID), referenceRequirements(explicitRefs)...)}
	if encoding != "" {
		selection.Required = append(selection.Required, nativeZstdRequirement(r.URL.Path))
	}
	if owned != nil {
		selection.Reference = &ResourceScope{GroupID: owned.Binding.GroupID, ChannelID: owned.Binding.ChannelID, CredentialScope: owned.CredentialScope}
	}
	// Read the protected continuation before choosing an engine. Its ACL, native
	// filters and original credential revision are rechecked on every request.
	cursorToken, cursorNative := "", ""
	var cursorScope *gatewaycontract.CursorScope
	var listBoundary *resourcestate.Record
	if isResourceList(ep.ID) {
		cursorToken = r.URL.Query().Get("after")
		if ep.ID == "cache.list" {
			cursorToken = r.URL.Query().Get("pageToken")
		}
		parentID := resourceListParent(ep, r, parent)
		var before *resourcestate.Record
		if boundary := r.URL.Query().Get("before"); boundary != "" {
			if ep.ID == "cache.list" || len(boundary) > 2048 {
				fail(400, "invalid_cursor", "分页边界无效")
				return
			}
			before, err = resourcestate.Resolve(r.Context(), e.Resources, actor, []gatewaycontract.Reference{{Kind: resourceListKind(ep), ID: boundary, ParentID: parentID}})
			if err != nil || before == nil || owned != nil && before.Binding != owned.Binding {
				fail(400, "invalid_cursor", "分页边界不属于当前资源范围")
				return
			}
			selection.Reference = &ResourceScope{GroupID: before.Binding.GroupID, ChannelID: before.Binding.ChannelID, CredentialScope: before.CredentialScope}
			listBoundary = before
		}
		if cursorToken != "" {
			scope, native, decodeErr := e.ResourceCursors.UnwrapPageCursor(actor, cursorToken)
			if decodeErr != nil && ep.ID != "cache.list" && len(cursorToken) <= 2048 && before == nil {
				boundary, lookupErr := resourcestate.Resolve(r.Context(), e.Resources, actor, []gatewaycontract.Reference{{Kind: resourceListKind(ep), ID: cursorToken, ParentID: parentID}})
				if lookupErr == nil && boundary != nil {
					scope = gatewaycontract.CursorScope{AccountID: key.OwnerID, KeyID: key.KeyID, Kind: resourceListKind(ep), ParentID: parentID, Binding: boundary.Binding, QueryDigest: pageQueryDigest(r, ep, pref.ModelID)}
					native, decodeErr = e.ResourceCursors.OwnedBoundary(actor, scope, boundary.Resource)
				}
			}
			if decodeErr != nil || owned != nil && owned.Binding != scope.Binding || before != nil && before.Binding != scope.Binding {
				fail(400, "invalid_cursor", "分页游标无效或不属于当前资源范围")
				return
			}
			cursorScope = &scope
			cursorNative = native
			selection.Reference = &ResourceScope{GroupID: scope.Binding.GroupID, ChannelID: scope.Binding.ChannelID}
			for _, v := range records {
				if candidateBinding(v.Candidate) == scope.Binding {
					selection.Reference.CredentialScope = v.Candidate.CredentialScope
					break
				}
			}
		}
	}
	selected, err := SelectAuxiliary(records, selection)
	if err != nil {
		if errors.Is(err, ErrManualOrderRequired) {
			fail(400, "manual_order_required", "没有适用的已保存手动渠道顺序")
			return
		}
		fail(503, "required_capability_evidence_missing", "没有符合权限、协议版本及有效能力证据的渠道："+ep.ID)
		return
	}
	candidates := make([]Candidate, 0, len(selected))
	for _, v := range selected {
		if !resourceMatch(v.Candidate, owned) || !resourceMatch(v.Candidate, listBoundary) {
			continue
		}
		state, _ := e.Runtime.Check(v.Candidate.ChannelID, EffectiveLimits(v.Candidate.Advanced, defaults), e.Now())
		if state == StateAvailable {
			candidates = append(candidates, v.Candidate)
		}
	}
	if model != "" {
		m, _ := e.Catalog.Get(r.Context(), model)
		for i := range candidates {
			p := candidates[i].PricingModel(m)
			candidates[i].RateNano = ledger.ModelRate(p.PriceCurrency, defaults.USDCNYRateNano)
		}
		e.applyTypicalPrices(candidates, model, defaults.USDCNYRateNano)
		// Auxiliary entry points have no generation cadence of their own. Use
		// only the verified model context in the matching protocol, never a
		// channel-wide speed or a made-up model.
		stats, demoted := e.statsView().forProtocol(model, protocolFormat(ep.Protocol), ep.Protocol, "", false)
		candidates = Rank(RankInput{Mode: pref.Mode, Order: pref.Order, Excluded: pref.Excluded, Candidates: candidates, Stats: stats, Demoted: demoted, ConsumerID: key.OwnerID})
	}
	if len(candidates) == 0 {
		fail(503, "resource_scope_unavailable", "原资源渠道或凭据已不可用")
		return
	}
	select {
	case e.followupSlots <- struct{}{}:
		defer func() { <-e.followupSlots }()
	default:
		fail(429, "too_many_auxiliary_requests", "辅助请求过多，请稍后重试")
		return
	}
	release, admitted := e.inflight.Acquire(key.OwnerID, int(defaults.AccountMaxInflightCalls))
	if !admitted {
		fail(429, "too_many_inflight_calls", "同时进行的请求过多")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), e.CallTimeLimit)
	defer cancel()
	budget := int(defaults.DefaultMaxAttempts)
	if pref.MaxAttempts != nil {
		budget = int(*pref.MaxAttempts)
	}
	operationID, err := uuidids.NewUUID()
	if err != nil {
		fail(500, "internal_error", "无法创建操作标识")
		return
	}
	for slot := range e.reservations(candidates, budget, defaults) {
		c := slot.candidate
		if ep.ID == "responses.get" && r.URL.Query().Get("stream") == "true" && target != nil {
			err = e.serveOwnedResponseStream(w, r, ep, c, key, location, policy, defaults, pref, target, &budget)
			slot.release()
			if err != nil {
				fail(502, "upstream_failed", "读取上游响应失败")
			}
			return
		}
		var payload []byte
		var response *http.Response
		if isResourceList(ep.ID) {
			payload, response, err = e.ownedResourcePage(ctx, r, ep, c, key, location, policy, defaults, pref, parent, cursorScope, cursorNative, target, &budget)
		} else {
			payload, response, err = e.auxiliaryExchange(ctx, r, ep, c, location, policy, defaults, pref, body, &budget)
			if err == nil && response.StatusCode == 200 && ep.ID == "codex.models" {
				payload, err = filterCodexCatalog(payload, records, c, key)
			}
			if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 && ep.Scope != "context_acl_only" && ep.ID != "codex.models" {
				err = e.observeAuxiliary(ctx, ep, c, key, model, operationID, parent, payload, target)
			}
		}
		slot.release()
		if err != nil {
			var codecError *requestCodecError
			if errors.As(err, &codecError) {
				fail(transportError(codecError.cause))
				return
			}
			if ep.Replay == "write" || budget <= 0 || ctx.Err() != nil || owned != nil {
				fail(502, "upstream_result_unknown", "上游操作未能确认，未重复写入或更换资源归属")
				return
			}
			continue
		}
		for name, values := range ClientResponseHeaders(response.Header, response.Uncompressed) {
			w.Header()[name] = values
		}
		if ep.Scope == "context_acl_only" || ep.ID == "codex.models" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if ep.ID == "codex.models" {
			w.Header().Del("ETag")
			w.Header().Del("Last-Modified")
		}
		w.Header().Del("Content-Length")
		w.Header().Set("X-AIHub-Request-Id", operationID)
		_ = http.NewResponseController(w).SetWriteDeadline(e.Now().Add(clientWriteLimit))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(payload)
		return
	}
	fail(503, "no_channel_available", "当前没有可用的渠道")
}

// auxiliaryExchange bounds every body and uses one shared address/channel
// attempt budget. Any attempted write is never automatically replayed: a 5xx,
// reset or timeout cannot prove the server did not apply it.
func (e *Engine) auxiliaryExchange(ctx context.Context, r *http.Request, ep gatewaycontract.Endpoint, c Candidate, location authLocation, policy channel.Outbound, defaults settings.Settings, pref routing.Pref, body []byte, budget *int) ([]byte, *http.Response, error) {
	secret, err := e.Keyring.OpenCredential(c.ChannelID, c.Credential)
	if err != nil {
		return nil, nil, err
	}
	upstream := body
	if len(body) > 0 {
		_, scan, scanErr := ScanRequest(body)
		if scanErr == nil {
			replacement := c.UpstreamModel
			if ep.ID == "cache.create" {
				replacement = "models/" + replacement
			}
			upstream, _ = UpstreamBody(body, scan, replacement, false)
		}
	}
	encoding, err := nativeencoding.Encoding(r.Header.Values("Content-Encoding"))
	if err != nil {
		return nil, nil, &requestCodecError{cause: err}
	}
	if encoding != "" {
		upstream, err = nativeencoding.Encode(ctx, encoding, upstream)
		if err != nil {
			return nil, nil, &requestCodecError{cause: err}
		}
	}
	entries := c.Entrances
	if len(entries) == 0 {
		entries = []Entrance{{BaseURL: c.BaseURL}}
	}
	var last error
	for _, entry := range entries {
		if *budget <= 0 {
			return nil, nil, errors.New("attempt budget exhausted")
		}
		c.BaseURL = entry.BaseURL
		client, err := policy.GatewayClient(ctx, c.BaseURL)
		if err != nil {
			last = err
			continue
		}
		query := r.URL.RawQuery
		if ep.ID == "chat.list" && r.URL.Query().Get("model") != "" {
			pairs := strings.Split(query, "&")
			for i, pair := range pairs {
				key, _, _ := strings.Cut(pair, "=")
				name, _ := url.QueryUnescape(key)
				if name == "model" {
					pairs[i] = key + "=" + url.QueryEscape(c.UpstreamModel)
				}
			}
			query = strings.Join(pairs, "&")
		}
		target, err := auxiliaryURL(c, nativePath(ep, r, c.UpstreamModel), query, location, secret)
		if err != nil {
			return nil, nil, err
		}
		limits := resolveTimeouts(defaults, pref).withChannel(c.Advanced)
		timeout := limits.request
		if timeout <= 0 {
			timeout = e.CallTimeLimit
		}
		attempt, cancel := context.WithTimeout(ctx, timeout)
		request, err := http.NewRequestWithContext(attempt, r.Method, target, bytes.NewReader(upstream))
		if err != nil {
			cancel()
			return nil, nil, err
		}
		request.Header = UpstreamHeaders(r.Header, location, secret, c.Advanced)
		if ep.ID == "codex.models" {
			request.Header.Del("If-None-Match")
			request.Header.Del("If-Modified-Since")
		}
		mapped, _ := r.Context().Value(codexNamespaceApplied{}).(bool)
		if mapped {
			contextWireHeaders(request.Header, r.Header)
		}
		if encoding != "" || mapped {
			requestWireHeaders(request.Header, encoding)
		}
		*budget--
		response, err := client.Do(request)
		if err != nil {
			cancel()
			if ep.Replay == "write" {
				return nil, nil, err
			}
			last = err
			continue
		}
		readLimit := maxRequestBytes
		if ep.ID == "codex.models" {
			readLimit = 4 << 20
		}
		if isResourceList(ep.ID) {
			readLimit = gatewaycontract.MaxPageBytes
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, int64(readLimit)+1))
		_ = response.Body.Close()
		cancel()
		if readErr != nil || len(payload) > readLimit {
			if readErr == nil {
				readErr = errors.New("response exceeds limit")
			}
			return nil, nil, readErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if pause, limited := RateLimitCooldown(response.StatusCode, response.Header, e.Now(), EffectiveLimits(c.Advanced, defaults)); limited {
				e.Runtime.CooldownFor(c.ChannelID, pause, e.Now(), CooldownRateLimited)
			}
			safe := newUpstreamFailure(response, payload, secret)
			response.Header = safe.header
			payload = safe.body
			if ep.Replay == "read" && (response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504) {
				last = errors.New("retryable upstream read")
				continue
			}
		}
		return payload, response, nil
	}
	return nil, nil, last
}
