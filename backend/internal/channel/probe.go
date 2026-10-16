package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
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
	probeMaxPages   = 20
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
		query   url.Values
	}
	// Anthropic and Gemini page their lists; ask for the largest page and
	// follow the cursor. OpenAI returns everything at once.
	probes := []probe{
		{path: "/v1/models", headers: map[string]string{"Authorization": "Bearer " + apiKey}},
		{path: "/backend-api/codex/models", headers: map[string]string{"Authorization": "Bearer " + apiKey}},
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
		query := p.query
		for page := 0; page < probeMaxPages; page++ {
			target.RawQuery = query.Encode()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
			if err != nil {
				break
			}
			for name, value := range p.headers {
				request.Header.Set(name, value)
			}
			request.Header.Set("Accept", "application/json")
			list := readModelList(client, request)
			for _, id := range list.ids {
				if !seen[id] {
					seen[id] = true
					upstream = append(upstream, id)
				}
			}
			next := list.nextQuery(query)
			if next == nil {
				break
			}
			query = next
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

// modelList is one page of an upstream model list and its cursor.
type modelList struct {
	ids []string
	// lastID is set when an Anthropic-style list says more pages follow.
	lastID string
	// pageToken is a Gemini-style nextPageToken.
	pageToken string
}

// nextQuery returns the query of the following page, or nil on the last one.
func (l modelList) nextQuery(current url.Values) url.Values {
	if l.lastID == "" && l.pageToken == "" {
		return nil
	}
	next := url.Values{}
	for name, values := range current {
		next[name] = values
	}
	if l.lastID != "" {
		next.Set("after_id", l.lastID)
	} else {
		next.Set("pageToken", l.pageToken)
	}
	return next
}

// readModelList extracts model IDs from an OpenAI- or Anthropic-style
// ({"data":[{"id"}],"has_more","last_id"}) or Gemini-style
// ({"models":[{"name":"models/x"}],"nextPageToken"}) list. Any failure yields
// an empty page.
func readModelList(client *http.Client, request *http.Request) modelList {
	response, err := client.Do(request)
	if err != nil {
		return modelList{}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return modelList{}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, probeBodyLimit))
	if err != nil {
		return modelList{}
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
		Models  []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"models"`
		NextPageToken string `json:"nextPageToken"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return modelList{}
	}
	var list modelList
	for _, item := range parsed.Data {
		if id := strings.TrimSpace(item.ID); id != "" && len(id) <= 256 {
			list.ids = append(list.ids, id)
		}
	}
	for _, item := range parsed.Models {
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = strings.TrimSpace(item.Slug)
		}
		if id != "" && len(id) <= 256 {
			list.ids = append(list.ids, id)
		}
	}
	// A page without models cannot advance the cursor; stop rather than loop.
	if len(list.ids) > 0 {
		if parsed.HasMore {
			list.lastID = strings.TrimSpace(parsed.LastID)
		}
		list.pageToken = strings.TrimSpace(parsed.NextPageToken)
	}
	return list
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

// SuggestFormats deliberately makes no protocol claims from model names.
func SuggestFormats(string) []Format { return []Format{} }

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
	return s.testCompatibility(ctx, ownerID, id, request)
}
