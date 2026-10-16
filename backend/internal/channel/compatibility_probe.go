package channel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

const testStreamLimit = 4 << 20
const testEventLimit = 1 << 20

var ErrProbeBusy = errors.New("compatibility probes busy")
var compatibilityProbeSlots = make(chan struct{}, 2)

// A complete run is bounded by 200 cells, four workers and two minutes. At
// most two runs may execute in the process. Probes never use inference slots
// or the pooled gateway transport and never execute generated commands.
type compatibilityProbe struct {
	Model                    Model
	Protocol, Feature, Scope string
}

type probeResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Status      string `json:"status"`
	Deleted     bool   `json:"deleted"`
	ServiceTier string `json:"service_tier"`
	Reasoning   struct {
		Context string `json:"context"`
		Effort  string `json:"effort"`
	} `json:"reasoning"`
	Error        json.RawMessage   `json:"error"`
	Output       []json.RawMessage `json:"output"`
	Data         json.RawMessage   `json:"data"`
	InputTokens  *int64            `json:"input_tokens"`
	SearchOutput string            `json:"-"`
}

func (s *Service) testCompatibility(ctx context.Context, ownerID, id string, request TestRequest) ([]TestOutcome, Channel, error) {
	select {
	case compatibilityProbeSlots <- struct{}{}:
		defer func() { <-compatibilityProbeSlots }()
	default:
		return nil, Channel{}, ErrProbeBusy
	}
	requestCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	current, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, Channel{}, err
	}
	protocols := request.Formats
	if len(protocols) == 0 {
		protocols = []Format{FormatOpenAIResponses, FormatCodex}
	}
	for _, format := range protocols {
		if format != FormatOpenAIResponses && format != FormatCodex {
			return nil, Channel{}, ErrInvalidInput
		}
	}
	for _, wanted := range request.ModelIDs {
		if !slices.ContainsFunc(current.Models, func(m Model) bool { return m.ModelID == wanted }) {
			return nil, Channel{}, ErrInvalidInput
		}
	}
	var jobs []compatibilityProbe
	channelModel := false
	for _, model := range current.Models {
		if len(request.ModelIDs) > 0 && !slices.Contains(request.ModelIDs, model.ModelID) {
			continue
		}
		for _, format := range uniqueFormats(protocols) {
			protocol := "responses"
			if format == FormatCodex {
				protocol = "codex"
			}
			if !channelModel {
				features := slices.Clone(ChannelFeatures)
				if protocol == "codex" {
					features = append(features, CodexChannelFeatures...)
				}
				for _, feature := range features {
					jobs = append(jobs, compatibilityProbe{model, protocol, feature, "channel"})
				}
			}
			for _, feature := range ModelFeatures {
				jobs = append(jobs, compatibilityProbe{model, protocol, feature, "model"})
			}
		}
		channelModel = true
	}
	if len(jobs) == 0 || len(jobs) > maxTestCombos {
		return nil, Channel{}, ErrInvalidInput
	}
	stored, err := s.Store.Credential(ctx, id)
	if err != nil {
		return nil, Channel{}, err
	}
	secret, err := s.Keyring.Decrypt(id, stored)
	if err != nil {
		return nil, Channel{}, err
	}
	policy, err := s.outbound(ctx)
	if err != nil {
		return nil, Channel{}, err
	}
	results := make([]TestOutcome, len(jobs))
	queue := make(chan int)
	var workers sync.WaitGroup
	for range testParallelism {
		workers.Go(func() {
			for i := range queue {
				results[i] = s.probeCompatibilityCell(ctx, policy, current, jobs[i], secret)
			}
		})
	}
	for i := range jobs {
		queue <- i
	}
	close(queue)
	workers.Wait()
	if requestCtx.Err() != nil {
		return nil, Channel{}, requestCtx.Err()
	}
	latest, err := s.Store.Get(requestCtx, id)
	if err != nil {
		return nil, Channel{}, err
	}
	if !latest.UpdatedAt.Equal(current.UpdatedAt) {
		return nil, Channel{}, ErrConflict
	}
	if _, err := s.Store.SaveTests(requestCtx, id, results, time.Now().UTC(), false); err != nil {
		return nil, Channel{}, err
	}
	// Work on owned copies: apply:false returns a reviewable draft, not a write.
	next := current
	next.Models = slices.Clone(current.Models)
	next.Advanced.Capabilities = slices.Clone(current.Advanced.Capabilities)
	for i := range next.Models {
		next.Models[i].Capabilities = slices.Clone(current.Models[i].Capabilities)
		next.Models[i].FormatTests = make(map[Format]FormatTest, len(current.Models[i].FormatTests))
		for k, v := range current.Models[i].FormatTests {
			next.Models[i].FormatTests[k] = v
		}
	}
	for _, result := range results {
		if result.Skipped {
			continue
		}
		cell := result.Protocol + "/" + result.Capability
		change := func(values []string) []string {
			values = slices.DeleteFunc(values, func(v string) bool { return v == cell })
			if result.OK {
				values = append(values, cell)
			}
			return values
		}
		if result.Scope == "channel" {
			next.Advanced.Capabilities = change(next.Advanced.Capabilities)
		}
		for i := range next.Models {
			if next.Models[i].ModelID != result.ModelID {
				continue
			}
			if result.Scope == "model" {
				next.Models[i].Capabilities = change(next.Models[i].Capabilities)
			}
			next.Models[i].FormatTests[Format(cell)] = FormatTest{OK: result.OK, StatusCode: result.StatusCode, Error: result.Error, DurationMS: &result.DurationMS, TestedAt: time.Now().UTC()}
		}
	}
	for i := range next.Models {
		next.Models[i].Formats = DeclaredFormats(next.Advanced.Capabilities)
	}
	if request.Apply {
		next, err = s.Update(requestCtx, ownerID, id, UpdateInput{Advanced: &next.Advanced, Models: &next.Models})
		if err != nil {
			return nil, Channel{}, err
		}
	}
	return results, next, nil
}

func probeTarget(base, endpoint string) (string, error) {
	target, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	prefix := strings.TrimSuffix(target.Path, "/")
	if strings.HasSuffix(prefix, "/backend-api/codex") && strings.HasPrefix(endpoint, "/backend-api/codex/") {
		prefix = strings.TrimSuffix(prefix, "/backend-api/codex")
	}
	if strings.HasSuffix(prefix, "/v1") && strings.HasPrefix(endpoint, "/v1/") {
		prefix = strings.TrimSuffix(prefix, "/v1")
	}
	target.Path, target.RawPath, target.RawQuery = prefix+endpoint, "", ""
	return target.String(), nil
}

func probeOutputKind(output []json.RawMessage, kind string) bool {
	for _, item := range output {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(item, &v)
		if v.Type == kind {
			return true
		}
	}
	return false
}
func probeEncrypted(output []json.RawMessage, kind string) bool {
	for _, item := range output {
		var v struct {
			Type      string `json:"type"`
			Encrypted string `json:"encrypted_content"`
		}
		_ = json.Unmarshal(item, &v)
		if v.Type == kind && v.Encrypted != "" {
			return true
		}
	}
	return false
}
func probeText(output []json.RawMessage) string {
	var text strings.Builder
	for _, item := range output {
		var v struct {
			Content []struct{ Type, Text string } `json:"content"`
		}
		_ = json.Unmarshal(item, &v)
		for _, c := range v.Content {
			if c.Type == "output_text" {
				text.WriteString(c.Text)
			}
		}
	}
	return text.String()
}

func (s *Service) probeCompatibilityCell(ctx context.Context, policy Outbound, channel Channel, job compatibilityProbe, secret string) TestOutcome {
	result := TestOutcome{ModelID: job.Model.ModelID, Format: FormatOpenAIResponses, Protocol: job.Protocol, Capability: job.Feature, Scope: job.Scope}
	if job.Protocol == "codex" {
		result.Format = FormatCodex
	}
	started := time.Now()
	if ctx.Err() != nil {
		message := "已达到本轮探测时间预算，未修改声明"
		result.Skipped = true
		result.Error = &message
		return result
	}
	finish := func(err error) TestOutcome {
		result.DurationMS = int(time.Since(started).Milliseconds())
		if err == nil {
			result.OK = true
		} else {
			message := strings.ReplaceAll(err.Error(), secret, "***")
			runes := []rune(message)
			if len(runes) > errorTextLimit {
				message = string(runes[:errorTextLimit])
			}
			result.Error = &message
		}
		return result
	}
	skip := func() TestOutcome {
		result.Skipped = true
		return finish(errors.New("此项需要手动确认，未修改声明"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := policy.FreshClient(ctx, channel.BaseURL)
	if err != nil {
		return finish(errors.New("出站校验未通过"))
	}
	defer client.CloseIdleConnections()
	root := "/v1/responses"
	if job.Protocol == "codex" {
		root = "/backend-api/codex/responses"
	}
	message := func(role, text string) map[string]any {
		kind := "input_text"
		if role == "assistant" {
			kind = "output_text"
		}
		return map[string]any{"role": role, "content": []any{map[string]any{"type": kind, "text": text}}}
	}
	body := map[string]any{"model": job.Model.UpstreamModel, "instructions": "You are a helpful assistant. Follow the compatibility check precisely.", "input": []any{message("user", "Reply exactly OK.")}, "store": false, "stream": job.Protocol == "codex"}
	if job.Protocol != "codex" {
		body["max_output_tokens"] = 128
	}

	lite := false
	compressed := false
	// One transport helper applies the exact saved channel header policy as the
	// gateway does; Authorization always uses the channel credential.
	send := func(method, endpoint string, payload any) (probeResponse, error) {
		var value probeResponse
		target, err := probeTarget(channel.BaseURL, endpoint)
		if err != nil {
			return value, err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return value, err
		}
		if payload == nil {
			raw = nil
		}
		if compressed {
			encoder, e := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
			if e != nil {
				return value, e
			}
			raw = encoder.EncodeAll(raw, nil)
			encoder.Close()
		}
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
		if err != nil {
			return value, err
		}
		req.Header.Set("Content-Type", "application/json")
		for _, rule := range channel.Advanced.HeaderRules.Set {
			req.Header.Set(rule.Name, rule.Value)
		}
		for _, name := range channel.Advanced.HeaderRules.Remove {
			req.Header.Del(name)
		}
		if channel.Advanced.UserAgent != nil {
			req.Header.Set("User-Agent", *channel.Advanced.UserAgent)
		}
		if lite {
			req.Header.Set("X-OpenAI-Internal-Codex-Responses-Lite", "true")
		}
		if compressed {
			req.Header.Set("Content-Encoding", "zstd")
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		res, err := client.Do(req)
		if err != nil {
			return value, errors.New("连接失败或请求超时")
		}
		defer res.Body.Close()
		status := res.StatusCode
		result.StatusCode = &status
		if status < 200 || status >= 300 {
			limited, _ := io.ReadAll(io.LimitReader(res.Body, testBodyLimit))
			return value, fmt.Errorf("HTTP %d %s", status, strings.TrimSpace(string(limited)))
		}
		if strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
			if job.Feature == "nonstream" {
				return value, errors.New("上游未遵守非流式请求")
			}
			streamLimit := int64(testStreamLimit)
			if job.Feature == "image_generation" {
				streamLimit = 32 << 20
			}
			reader := &io.LimitedReader{R: res.Body, N: streamLimit + 1}
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 4096), testEventLimit)
			var data []string
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data:") {
					data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
					continue
				}
				if line != "" || len(data) == 0 {
					continue
				}
				var event struct {
					Type     string        `json:"type"`
					Response probeResponse `json:"response"`
				}
				if json.Unmarshal([]byte(strings.Join(data, "\n")), &event) != nil {
					return value, errors.New("无效 SSE 事件")
				}
				data = nil
				if event.Type == "response.failed" || event.Type == "error" {
					return value, errors.New("上游返回失败事件")
				}
				if event.Type == "response.completed" || event.Type == "response.incomplete" {
					if reader.N <= 0 {
						return value, errors.New("测试响应超过限制")
					}
					return event.Response, nil
				}
			}
			return value, errors.New("测试流未正常结束或超过大小限制")
		}
		if job.Feature == "stream" {
			return value, errors.New("流式请求未返回 SSE")
		}
		// Image payloads are bounded too; no generated artifact is persisted.
		limit := int64(testStreamLimit)
		if strings.Contains(endpoint, "/images/") || job.Feature == "image_generation" {
			limit = 32 << 20
		}
		raw, err = io.ReadAll(io.LimitReader(res.Body, limit+1))
		if err != nil || int64(len(raw)) > limit {
			return value, errors.New("响应读取失败或超过大小限制")
		}
		if job.Feature == "search" {
			var search struct {
				Output string          `json:"output"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(raw, &search) != nil || strings.TrimSpace(search.Output) == "" || len(search.Error) > 0 && string(search.Error) != "null" {
				return value, errors.New("缺少有效搜索结果")
			}
			value.SearchOutput = search.Output
			return value, nil
		}
		if json.Unmarshal(raw, &value) != nil {
			return value, errors.New("响应不是有效 JSON")
		}
		if len(value.Error) > 0 && string(value.Error) != "null" {
			return value, errors.New("上游返回错误")
		}
		return value, nil
	}
	validResponse := func(value probeResponse) error {
		if value.ID == "" || value.Output == nil || (value.Status != "completed" && value.Status != "incomplete") {
			return errors.New("响应缺少有效的 Responses 结果")
		}
		return nil
	}
	function := func(name string) map[string]any {
		return map[string]any{"type": "function", "name": name, "description": "Return the requested value.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}, "strict": true}
	}
	switch job.Feature {
	case "tool_search", "file_search", "code_interpreter", "mcp", "computer", "shell", "apply_patch", "images_edit", "vision":
		return skip()
	case "inference":
	case "nonstream":
		body["stream"] = false
	case "stream":
		body["stream"] = true
	case "zstd":
		compressed = true
	case "function_tools", "tool_namespaces", "parallel_tools":
		tools := []any{function("compat_echo")}
		body["tool_choice"] = "required"
		body["input"] = []any{message("user", "Call compat_echo with value OK.")}
		if job.Feature == "tool_namespaces" {
			tools = []any{map[string]any{"type": "namespace", "name": "compat", "description": "Compatibility probe tools", "tools": tools}}
		}
		if job.Feature == "parallel_tools" {
			tools = []any{function("compat_a"), function("compat_b")}
			body["parallel_tool_calls"] = true
			body["input"] = []any{message("user", "Call both compat_a and compat_b now with value OK. They are independent.")}
		}
		body["tools"] = tools
	case "custom_tools":
		body["tools"] = []any{map[string]any{"type": "custom", "name": "compat_echo", "description": "Call this tool with the exact text OK."}}
		body["tool_choice"] = "required"
	case "structured_output":
		body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "compat", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}}}
	case "reasoning", "reasoning_all_turns", "encrypted_reasoning":
		body["reasoning"] = map[string]any{"effort": "low"}
		if job.Feature == "reasoning_all_turns" {
			body["reasoning"] = map[string]any{"effort": "low", "context": "all_turns"}
		}
		if job.Feature == "encrypted_reasoning" {
			body["include"] = []string{"reasoning.encrypted_content"}
		}
	case "responses_lite":
		lite = true
		body["reasoning"] = map[string]any{"effort": "low", "context": "all_turns"}
		body["parallel_tool_calls"] = false
		body["input"] = []any{map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{map[string]any{"type": "namespace", "name": "functions", "description": "Compatibility probe tools", "tools": []any{function("compat_echo")}}}}, message("user", "Call compat_echo with value OK.")}
		body["tool_choice"] = "required"
	case "compact", "compact_v1", "compaction_input":
		compactBody := map[string]any{"model": job.Model.UpstreamModel, "input": []any{message("user", "Remember this compatibility check: OK."), message("assistant", "OK")}}
		value, err := send(http.MethodPost, root+"/compact", compactBody)
		if err != nil {
			return finish(err)
		}
		if !probeEncrypted(value.Output, "compaction") {
			return finish(errors.New("压缩结果缺少完整 compaction 条目"))
		}
		if job.Feature != "compaction_input" {
			return finish(nil)
		}
		items := make([]any, 0, len(value.Output)+1)
		for _, item := range value.Output {
			items = append(items, item)
		}
		items = append(items, message("user", "Reply OK."))
		body["input"] = items
	case "compact_v2":
		body["input"] = append(body["input"].([]any), map[string]any{"type": "compaction_trigger"})
	case "fast":
		body["service_tier"] = "priority"
	case "ultrafast":
		body["service_tier"] = "ultrafast"
	case "web_search":
		body["tools"] = []any{map[string]any{"type": "web_search"}}
		body["tool_choice"] = map[string]any{"type": "web_search"}
		body["input"] = []any{message("user", "Search for the official OpenAI website.")}
	case "image_generation":
		body["tools"] = []any{map[string]any{"type": "image_generation", "quality": "low"}}
		body["tool_choice"] = map[string]any{"type": "image_generation"}
		body["input"] = []any{message("user", "Generate a plain black dot on white.")}
	case "search":
		endpoint := strings.TrimSuffix(root, "/responses") + "/alpha/search"
		_, err := send(http.MethodPost, endpoint, map[string]any{
			"id": "compatibility-probe", "model": job.Model.UpstreamModel,
			"commands": map[string]any{"search_query": []any{map[string]any{"q": "OpenAI official website", "domains": []string{"openai.com"}}}, "response_length": "short"},
		})
		return finish(err)
	case "images_generate":
		endpoint := strings.TrimSuffix(root, "/responses") + "/images/generations"
		value, err := send(http.MethodPost, endpoint, map[string]any{"model": job.Model.UpstreamModel, "prompt": "A black dot on white.", "n": 1, "quality": "low"})
		if err != nil {
			return finish(err)
		}
		var data []map[string]any
		if json.Unmarshal(value.Data, &data) != nil || len(data) == 0 {
			return finish(errors.New("缺少图片结果"))
		}
		if data[0]["b64_json"] == nil && data[0]["url"] == nil {
			return finish(errors.New("缺少图片内容"))
		}
		return finish(nil)
	case "input_tokens":
		value, err := send(http.MethodPost, root+"/input_tokens", body)
		if err != nil {
			return finish(err)
		}
		if value.InputTokens == nil {
			return finish(errors.New("缺少 input_tokens"))
		}
		return finish(nil)
	case "conversations":
		value, err := send(http.MethodPost, "/v1/conversations", map[string]any{})
		if err != nil {
			return finish(err)
		}
		if value.ID == "" || value.Object != "conversation" {
			return finish(errors.New("缺少 conversation 结果"))
		}
		_, _ = send(http.MethodDelete, "/v1/conversations/"+url.PathEscape(value.ID), nil)
		return finish(nil)
	case "background", "response_read", "response_delete", "response_cancel", "input_items":
		body["store"] = true
		if job.Feature == "background" || job.Feature == "response_cancel" {
			body["background"] = true
		}
		value, err := send(http.MethodPost, root, body)
		if err != nil {
			return finish(err)
		}
		if value.ID == "" || !slices.Contains([]string{"queued", "in_progress", "completed", "incomplete"}, value.Status) {
			return finish(errors.New("未返回可管理的响应"))
		}
		resource := root + "/" + url.PathEscape(value.ID)
		if job.Feature == "background" {
			_, _ = send(http.MethodPost, resource+"/cancel", map[string]any{})
			_, _ = send(http.MethodDelete, resource, nil)
			return finish(nil)
		}
		if job.Feature == "input_items" {
			value, err = send(http.MethodGet, resource+"/input_items", nil)
			if err == nil && (len(value.Data) == 0 || value.Data[0] != '[') {
				err = errors.New("缺少输入条目列表")
			}
		} else if job.Feature == "response_delete" {
			value, err = send(http.MethodDelete, resource, nil)
			if err == nil && !value.Deleted {
				err = errors.New("缺少删除确认")
			}
		} else if job.Feature == "response_cancel" {
			value, err = send(http.MethodPost, resource+"/cancel", map[string]any{})
			if err == nil && value.Status != "cancelled" {
				err = errors.New("未确认取消，需手动验证运行中任务")
			}
		} else {
			value, err = send(http.MethodGet, resource, nil)
			if err == nil {
				err = validResponse(value)
			}
		}
		_, _ = send(http.MethodDelete, resource, nil)
		return finish(err)
	default:
		return skip()
	}
	value, err := send(http.MethodPost, root, body)
	if err != nil {
		return finish(err)
	}
	if err = validResponse(value); err != nil {
		return finish(err)
	}
	verifyToolRoundTrip := func() error {
		next := make(map[string]any, len(body))
		for key, value := range body {
			next[key] = value
		}
		input, ok := body["input"].([]any)
		if !ok {
			return errors.New("缺少工具调用输入")
		}
		input = slices.Clone(input)
		outputs := make([]any, 0)
		for _, raw := range value.Output {
			input = append(input, raw)
			var item struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			}
			if json.Unmarshal(raw, &item) != nil {
				return errors.New("工具条目无效")
			}
			if item.Type != "function_call" && item.Type != "custom_tool_call" {
				continue
			}
			if item.CallID == "" || !strings.HasPrefix(item.Name, "compat_") && !strings.Contains(item.Name, ".compat_") {
				return errors.New("未返回要求的工具或 call_id")
			}
			kind := "function_call_output"
			if item.Type == "custom_tool_call" {
				kind = "custom_tool_call_output"
			}
			outputs = append(outputs, map[string]any{"type": kind, "call_id": item.CallID, "output": `{"value":"compatibility-result-17"}`})
		}
		if len(outputs) == 0 {
			return errors.New("未返回要求的工具调用")
		}
		input = append(input, outputs...)
		input = append(input, message("user", "Print the value returned by the tool exactly. Do not call another tool."))
		next["input"], next["tool_choice"] = input, "none"
		continuation, err := send(http.MethodPost, root, next)
		if err != nil {
			return err
		}
		if err = validResponse(continuation); err != nil {
			return err
		}
		if !strings.Contains(probeText(continuation.Output), "compatibility-result-17") {
			return errors.New("工具结果回传未通过，未返回执行器提供的值")
		}
		return nil
	}
	require := func(ok bool, message string) TestOutcome {
		if !ok {
			return finish(errors.New(message))
		}
		if slices.Contains([]string{"function_tools", "custom_tools", "tool_namespaces", "parallel_tools", "responses_lite"}, job.Feature) {
			return finish(verifyToolRoundTrip())
		}
		return finish(nil)
	}
	switch job.Feature {
	case "function_tools", "responses_lite":
		return require(probeOutputKind(value.Output, "function_call"), "未返回要求的函数调用")
	case "tool_namespaces":
		ok := false
		for _, raw := range value.Output {
			var item struct{ Type, Namespace, Name string }
			_ = json.Unmarshal(raw, &item)
			if item.Type == "function_call" && (item.Namespace == "compat" || strings.HasPrefix(item.Name, "compat.")) {
				ok = true
			}
		}
		return require(ok, "未确认命名空间函数调用")
	case "parallel_tools":
		names := map[string]bool{}
		for _, raw := range value.Output {
			var item struct{ Type, Name string }
			_ = json.Unmarshal(raw, &item)
			if item.Type == "function_call" {
				names[item.Name] = true
			}
		}
		return require(names["compat_a"] && names["compat_b"], "未在同一响应中返回两个独立函数调用")
	case "custom_tools":
		return require(probeOutputKind(value.Output, "custom_tool_call"), "未返回 custom 工具调用")
	case "structured_output":
		var data map[string]bool
		return require(json.Unmarshal([]byte(probeText(value.Output)), &data) == nil && len(data) == 1 && data["ok"], "未返回指定结构化结果")
	case "reasoning":
		return require(value.Reasoning.Effort != "" || probeOutputKind(value.Output, "reasoning"), "未观察到推理能力")
	case "reasoning_all_turns":
		return require(value.Reasoning.Context == "all_turns", "上游未确认 all_turns；跨轮推理效果需另行验证")
	case "encrypted_reasoning":
		return require(probeEncrypted(value.Output, "reasoning"), "未返回加密推理条目")
	case "compact_v2":
		return require(probeEncrypted(value.Output, "compaction"), "未返回 V2 压缩结果")
	case "fast":
		return require(value.ServiceTier == "priority" || value.ServiceTier == "fast", "实际响应未使用 Fast/Priority")
	case "ultrafast":
		return require(value.ServiceTier == "ultrafast", "实际响应未使用 Ultrafast")
	case "web_search":
		return require(probeOutputKind(value.Output, "web_search_call"), "未观察到服务端搜索调用")
	case "image_generation":
		complete := false
		for _, raw := range value.Output {
			var image struct{ Type, Status, Result string }
			_ = json.Unmarshal(raw, &image)
			if image.Type == "image_generation_call" && image.Status == "completed" && image.Result != "" {
				complete = true
			}
		}
		return require(complete, "未返回完成的生图结果")
	}
	return finish(nil)
}
