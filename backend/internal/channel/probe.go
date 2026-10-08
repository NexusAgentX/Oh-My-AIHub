package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// DiscoveredModel is one upstream model ID with the catalog model it matched
// and the formats suggested for it.
type DiscoveredModel struct {
	ID               string
	MatchedModelID   *string
	SuggestedFormats []Format
}

const (
	probeTimeout    = 10 * time.Second
	probeBodyLimit  = 4 << 20
	testTimeout     = 30 * time.Second
	testBodyLimit   = 64 << 10
	testParallelism = 4
	maxTestCombos   = 200
	errorTextLimit  = 200
	probeAnthropicV = "2023-06-01"
)

// Discover reads the upstream model lists and matches them with the catalog.
// A failing probe is not an error: the caller adds models by hand instead.
func (s *Service) Discover(ctx context.Context, baseURL, apiKey string) (string, []DiscoveredModel, error) {
	apiKey = strings.TrimSpace(apiKey)
	if !validCredential(apiKey) {
		return "", nil, ErrInvalidInput
	}
	normalized, err := s.validateBase(ctx, baseURL)
	if err != nil {
		return "", nil, err
	}
	policy, err := s.outbound(ctx)
	if err != nil {
		return "", nil, err
	}
	known, err := s.KnownModels(ctx)
	if err != nil {
		return "", nil, err
	}

	type probe struct {
		path    string
		headers map[string]string
		query   string
	}
	probes := []probe{
		{path: "/v1/models", headers: map[string]string{"Authorization": "Bearer " + apiKey}},
		{path: "/v1/models", headers: map[string]string{"x-api-key": apiKey, "anthropic-version": probeAnthropicV}},
		{path: "/v1beta/models", query: "key=" + url.QueryEscape(apiKey)},
	}
	seen := map[string]bool{}
	var upstream []string
	for _, p := range probes {
		client, err := policy.Client(ctx, normalized, probeTimeout)
		if err != nil {
			return "", nil, ErrInvalidInput
		}
		target, err := url.Parse(normalized)
		if err != nil {
			return "", nil, ErrInvalidInput
		}
		target.Path = strings.TrimSuffix(target.Path, "/") + p.path
		target.RawQuery = p.query
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			continue
		}
		for name, value := range p.headers {
			request.Header.Set(name, value)
		}
		request.Header.Set("Accept", "application/json")
		for _, id := range readModelList(client, request) {
			if !seen[id] {
				seen[id] = true
				upstream = append(upstream, id)
			}
		}
	}
	slices.Sort(upstream)
	result := make([]DiscoveredModel, 0, len(upstream))
	for _, id := range upstream {
		matched := MatchModel(id, known)
		family := id
		if matched != nil {
			family = *matched
		}
		result = append(result, DiscoveredModel{ID: id, MatchedModelID: matched, SuggestedFormats: SuggestFormats(family)})
	}
	return normalized, result, nil
}

// readModelList extracts model IDs from an OpenAI-style ({"data":[{"id"}]})
// or Gemini-style ({"models":[{"name":"models/x"}]}) list. Any failure yields
// an empty list.
func readModelList(client *http.Client, request *http.Request) []string {
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, probeBodyLimit))
	if err != nil {
		return nil
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	var ids []string
	for _, item := range parsed.Data {
		if id := strings.TrimSpace(item.ID); id != "" && len(id) <= 256 {
			ids = append(ids, id)
		}
	}
	for _, item := range parsed.Models {
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id != "" && len(id) <= 256 {
			ids = append(ids, id)
		}
	}
	return ids
}

var dateSuffix = regexp.MustCompile(`[-@](?:\d{8}|\d{4}-\d{2}-\d{2}|\d{2}-\d{2}|\d{4})$`)

// MatchModel finds the catalog model for an upstream ID: exact, then
// case-insensitive, then with a date suffix removed.
func MatchModel(upstreamID string, catalogIDs []string) *string {
	candidates := []string{upstreamID}
	if trimmed := dateSuffix.ReplaceAllString(upstreamID, ""); trimmed != upstreamID {
		candidates = append(candidates, trimmed)
	}
	for _, candidate := range candidates {
		if slices.Contains(catalogIDs, candidate) {
			id := candidate
			return &id
		}
		for _, id := range catalogIDs {
			if strings.EqualFold(id, candidate) {
				match := id
				return &match
			}
		}
	}
	return nil
}

var openAIReasoning = regexp.MustCompile(`^o\d`)

// SuggestFormats pre-selects formats by model family (Epic #170 decision 4);
// the test button corrects them against the real upstream.
func SuggestFormats(modelID string) []Format {
	name := strings.ToLower(modelID)
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	switch {
	case strings.HasPrefix(name, "claude"):
		return []Format{FormatOpenAIChat, FormatAnthropic}
	case strings.HasPrefix(name, "gpt"), openAIReasoning.MatchString(name):
		return []Format{FormatOpenAIChat, FormatOpenAIResponses}
	case strings.HasPrefix(name, "gemini"):
		return []Format{FormatOpenAIChat, FormatGemini}
	}
	return []Format{FormatOpenAIChat}
}

// testBody is the smallest request of each format.
func testBody(format Format, upstreamModel string) []byte {
	model, _ := json.Marshal(upstreamModel)
	switch format {
	case FormatOpenAIResponses:
		return []byte(fmt.Sprintf(`{"model":%s,"input":"hi","max_output_tokens":16}`, model))
	case FormatAnthropic:
		return []byte(fmt.Sprintf(`{"model":%s,"messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model))
	case FormatGemini:
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":1}}`)
	}
	return []byte(fmt.Sprintf(`{"model":%s,"messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model))
}

// TestRequest selects what to probe; empty slices mean everything.
type TestRequest struct {
	ModelIDs []string
	Formats  []Format
	Apply    bool
}

// Test sends one minimal real request per selected model × format, stores the
// results in format_tests and optionally rewrites formats to the passing ones.
// It writes no call records and moves no points: the owner pays the upstream.
func (s *Service) Test(ctx context.Context, ownerID, id string, request TestRequest) ([]TestOutcome, Channel, error) {
	channel, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return nil, Channel{}, err
	}
	formats := request.Formats
	if len(formats) == 0 {
		formats = Formats
	}
	for _, format := range formats {
		if !format.Valid() {
			return nil, Channel{}, ErrInvalidInput
		}
	}
	type job struct {
		model  Model
		format Format
	}
	var jobs []job
	for _, model := range channel.Models {
		if len(request.ModelIDs) > 0 && !slices.Contains(request.ModelIDs, model.ModelID) {
			continue
		}
		for _, format := range uniqueFormats(formats) {
			jobs = append(jobs, job{model: model, format: format})
		}
	}
	if len(jobs) == 0 || len(jobs) > maxTestCombos {
		return nil, Channel{}, ErrInvalidInput
	}
	for _, wanted := range request.ModelIDs {
		if !slices.ContainsFunc(channel.Models, func(m Model) bool { return m.ModelID == wanted }) {
			return nil, Channel{}, ErrInvalidInput
		}
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

	outcomes := make([]TestOutcome, len(jobs))
	var wait sync.WaitGroup
	slots := make(chan struct{}, testParallelism)
	for index, item := range jobs {
		wait.Add(1)
		slots <- struct{}{}
		go func() {
			defer wait.Done()
			defer func() { <-slots }()
			outcomes[index] = s.probeOnce(ctx, policy, channel, item.model, item.format, secret)
		}()
	}
	wait.Wait()

	testedAt := time.Now().UTC()
	updated, err := s.Store.SaveTests(ctx, id, outcomes, testedAt, request.Apply)
	if err != nil {
		return nil, Channel{}, err
	}
	var failures []string
	for _, outcome := range outcomes {
		if !outcome.OK {
			reason := ""
			if outcome.Error != nil {
				reason = *outcome.Error
			}
			failures = append(failures, fmt.Sprintf("%s/%s: %s", outcome.ModelID, outcome.Format, reason))
		}
	}
	if len(failures) > 0 {
		reason := strings.Join(failures, "; ")
		if len(reason) > 480 {
			reason = reason[:480]
		}
		_ = s.Store.RecordEvent(ctx, id, EventTestFailed, strings.ToValidUTF8(reason, ""))
	}
	return outcomes, updated, nil
}

func (s *Service) probeOnce(ctx context.Context, policy Outbound, channel Channel, model Model, format Format, secret string) TestOutcome {
	outcome := TestOutcome{ModelID: model.ModelID, Format: format}
	fail := func(status *int, message string, started time.Time) TestOutcome {
		message = strings.ReplaceAll(message, secret, "***")
		if utf8Len := len([]rune(message)); utf8Len > errorTextLimit {
			message = string([]rune(message)[:errorTextLimit])
		}
		outcome.StatusCode, outcome.Error = status, &message
		outcome.DurationMS = int(time.Since(started).Milliseconds())
		return outcome
	}
	started := time.Now()
	client, err := policy.Client(ctx, channel.BaseURL, testTimeout)
	if err != nil {
		return fail(nil, "出站校验未通过", started)
	}
	target, err := BuildEndpoint(channel.BaseURL, format, model.UpstreamModel, false)
	if err != nil {
		return fail(nil, "无法构造请求地址", started)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(string(testBody(format, model.UpstreamModel))))
	if err != nil {
		return fail(nil, "无法构造请求", started)
	}
	request.Header.Set("Content-Type", "application/json")
	ApplyAuthentication(request, format, secret)
	response, err := client.Do(request)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return fail(nil, "请求超时", started)
		}
		return fail(nil, "连接失败", started)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, testBodyLimit))
	status := response.StatusCode
	outcome.StatusCode = &status
	outcome.DurationMS = int(time.Since(started).Milliseconds())
	if status >= 200 && status < 300 {
		outcome.OK = true
		return outcome
	}
	return fail(&status, fmt.Sprintf("HTTP %d %s", status, strings.TrimSpace(string(body))), started)
}
