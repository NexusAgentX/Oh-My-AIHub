package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/resourcestate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestGatewayOwnedSDKBoundariesAndProtectedContinuation(t *testing.T) {
	queries := []url.Values{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"foreign-%d"}],"has_more":true,"total":999}`, len(queries))
	}))
	defer upstream.Close()
	ring, err := channel.ParseKeyring(testKeyring, "k1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := ring.Encrypt("channel-own", 1, "synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	book, err := quotepricing.Freeze(quotepricing.Book{GroupID: "group-own", GroupRevision: 1, ConnectionVersion: 1, Offers: map[string]quotepricing.Offer{"platform/model:2026": {ModelID: "platform/model:2026", UpstreamModel: "native/model:2026", Revision: 1, Quote: market.Quote{ID: "q", GroupModelID: "gm", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}})
	if err != nil {
		t.Fatal(err)
	}
	c := gateway.Candidate{GroupID: "group-own", GroupModelID: "gm", ChannelID: "channel-own", CredentialRevision: 1, CredentialScope: "scope", OwnerID: "provider", BaseURL: "https://synthetic.example", UpstreamModel: "native/model:2026", Credential: sealed, Quotes: book, Evidence: map[string]bool{"upstream_protocol/openai-chat-completions.stored/owned_list_get_metadata_messages_delete": true}}
	store := &proxyStore{candidate: c, key: gateway.KeyAuth{OwnerID: "owner", KeyID: "key", AccountActive: true, Enabled: true}}
	record := resourcestate.Record{Resource: gatewaycontract.Resource{Kind: "stored_chat", ID: "mine", AccountID: "owner", KeyID: "key", Binding: gatewaycontract.Binding{GroupID: "group-own", ChannelID: "channel-own", CredentialVersion: "1"}, CredentialScope: "scope", CreationID: "create"}, ModelID: "platform/model:2026"}
	resources := &proxyRecords{rows: []resourcestate.Record{record}}
	codec, err := gatewaycontract.NewCursorCodec(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	engine := gateway.NewEngine(gateway.Dependencies{Store: store, Catalog: proxyModels{}, Settings: settings.NewService(newFakeStore()), Routing: newFakeRoutingStore(), Keyring: ring, Outbound: newTestOutbound(t, upstream), Resources: resources, ResourceCursors: codec})
	handler := NewHandler(Dependencies{Gateway: engine})
	call := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/chat/completions?"+query, nil)
		r.Header.Set("Authorization", "Bearer sk-aih-synthetic")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, direction := range []string{"after", "before"} {
		queries = nil
		first := call(direction + "=mine&limit=1")
		var page struct {
			Data []json.RawMessage `json:"data"`
			Next string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != 200 || len(page.Data) != 0 || page.Next == "" || len(queries) < 1 || len(queries) > 4 {
			t.Fatalf("bounded empty page: %d %s queries=%v", first.Code, first.Body.String(), queries)
		}
		if queries[0].Get(direction) != "mine" || strings.Contains(first.Body.String(), "foreign") || strings.Contains(first.Body.String(), "999") {
			t.Fatal("native boundary/leak", queries, first.Body.String())
		}
		query := "after=" + url.QueryEscape(page.Next) + "&limit=1"
		if direction == "before" {
			query += "&before=mine"
		}
		firstCount := len(queries)
		second := call(query)
		if second.Code != 200 || len(queries) <= firstCount || queries[firstCount].Get(direction) != fmt.Sprintf("foreign-%d", firstCount) {
			t.Fatal("protected scan did not continue", second.Code, queries, second.Body.String())
		}
		before := len(queries)
		for _, bad := range []string{"after=unknown&limit=1", "before=foreign&limit=1", query + "&order=asc", strings.Replace(query, "limit=1", "limit=2", 1)} {
			if w := call(bad); w.Code < 400 || len(queries) != before {
				t.Fatal("invalid cursor reached shared upstream", bad, w.Code, queries)
			}
		}
		store.key.KeyID = "other"
		if w := call(query); w.Code < 400 || len(queries) != before {
			t.Fatal("other Key reused cursor", w.Code)
		}
		store.key.KeyID = "key"
		store.candidate.CredentialRevision = 2
		if w := call(direction + "=mine&limit=1"); w.Code < 400 || len(queries) != before {
			t.Fatal("rotated credential accepted old boundary", w.Code)
		}
		store.candidate.CredentialRevision = 1
	}
	if len(store.finished) != 0 {
		t.Fatal("pagination billed inference")
	}
	if _, err := resources.Lookup(context.Background(), gatewaycontract.Reference{Kind: "stored_chat", ID: "foreign"}); err != nil {
		t.Fatal(err)
	}
}
