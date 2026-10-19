package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

type nativeListingModels struct{ proxyModels }

func (nativeListingModels) List(context.Context, bool) ([]catalog.Model, error) {
	return []catalog.Model{{ID: "platform/model:2026", Enabled: true}}, nil
}

type nativeListingStore struct{ *proxyStore }

func (nativeListingStore) OnlineModels(context.Context) ([]gateway.OnlineModel, error) {
	return []gateway.OnlineModel{{ID: "platform/model:2026", Formats: []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini, channel.FormatOpenAIImages}}}, nil
}
func (s nativeListingStore) ModelListCandidates(ctx context.Context, format channel.Format) ([]gateway.CandidateRecord, error) {
	return s.AuxiliaryCandidates(ctx, format)
}
func TestGatewayNativeModelGetUsesRegisteredRoutesAndKeyScopedAliases(t *testing.T) {
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "group", GroupRevision: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", Revision: 1, Quote: market.Quote{ID: "quote", GroupModelID: "offering", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source := nativeListingStore{&proxyStore{key: gateway.KeyAuth{OwnerID: "consumer", KeyID: "key", Enabled: true, AccountActive: true, AllowedModels: []string{"platform/model:2026"}, Aliases: map[string]string{"alias": "platform/model:2026", "hidden": "forbidden"}}, candidate: gateway.Candidate{ChannelID: "channel", GroupID: "group", GroupModelID: "offering", Quotes: book}}}
	var engine *gateway.Engine
	p := newPlatform(t, func(d *Dependencies) {
		engine = d.Gateway
		engine.Store = source
		engine.Catalog = nativeListingModels{}
	})
	request := func(path string, headers map[string]string, status int) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer sk-aih-synthetic-models")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		out := httptest.NewRecorder()
		p.handler.ServeHTTP(out, req)
		if out.Code != status {
			t.Fatal(path, out.Code, out.Body.String())
		}
		if status == 200 && (req.URL.Path != "/v1/models" && req.URL.Path != "/v1beta/models") {
			p.spec.assertResponse(t, "GET", path, out)
		}
		var body map[string]any
		if json.Unmarshal(out.Body.Bytes(), &body) != nil {
			t.Fatal("non-JSON model metadata")
		}
		return body
	}
	if got := request("/v1/models/platform%2Fmodel:2026", nil, 200); got["id"] != "platform/model:2026" || got["object"] != "model" {
		t.Fatal(got)
	}
	if got := request("/v1/models/alias", map[string]string{"anthropic-version": "2023-06-01"}, 200); got["id"] != "alias" || got["type"] != "model" {
		t.Fatal(got)
	}
	if got := request("/v1beta/models/alias", nil, 200); got["name"] != "models/alias" {
		t.Fatal(got)
	}
	if got := request("/v1/models/alias", map[string]string{"x-goog-api-key": "sk-aih-synthetic-models"}, 200); got["name"] != "models/alias" {
		t.Fatal("Gemini v1 misclassified", got)
	}
	if got := request("/v1/models?key=sk-aih-synthetic-models", nil, 200); got["models"] == nil {
		t.Fatal("Gemini v1 list misclassified", got)
	}
	request("/v1/models/hidden", nil, 404)
	source.key.AllowedModels = []string{"other"}
	request("/v1/models/alias", nil, 404)
	source.key.Enabled = false
	request("/v1/models/alias", nil, http.StatusUnauthorized)
	engine.Ready = func() error { return errors.New("private publication detail") }
	request("/v1/models/alias", nil, http.StatusServiceUnavailable)
	if source.count != 0 || len(source.finished) != 0 {
		t.Fatal("model metadata billed a request")
	}
}
