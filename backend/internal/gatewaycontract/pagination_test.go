package gatewaycontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func cursorFixture(t *testing.T) (*CursorCodec, Principal, CursorScope) {
	t.Helper()
	codec, err := NewCursorCodec(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	actor := Principal{"a", "k", true}
	scope := CursorScope{AccountID: "a", KeyID: "k", Kind: "response", Binding: Binding{"g", "c", "v1"}, QueryDigest: "canonical-method-path-filters-order-limit"}
	return codec, actor, scope
}

func TestOwnedPaginationFiltersBeforeResponseAndBoundsEachRequest(t *testing.T) {
	codec, actor, scope := cursorFixture(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		index := 0
		if after := r.URL.Query().Get("after"); after != "" {
			var err error
			index, err = strconv.Atoi(strings.TrimPrefix(after, "private-native-"))
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
		}
		id := fmt.Sprintf("other-account-%d", index)
		if index >= MaxScanPages {
			id = fmt.Sprintf("mine-%d", index)
		}
		page := NativePage{Items: []ListedResource{{Reference: Reference{Kind: "response", ID: id}, Raw: json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))}}}
		if index < MaxScanPages+1 {
			page.Next = fmt.Sprintf("private-native-%d", index+1)
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer upstream.Close()
	fetch := func(ctx context.Context, after string, limit int) (NativePage, error) {
		if limit < 1 {
			t.Fatal("requested more after a full page")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"?after="+after, nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return NativePage{}, err
		}
		defer response.Body.Close()
		var page NativePage
		err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, MaxPageBytes)).Decode(&page)
		return page, err
	}
	lookup := func(r Reference) ([]Resource, error) {
		owner, key := "other-account", "other-key"
		if strings.HasPrefix(r.ID, "mine-") {
			owner, key = "a", "k"
		}
		return []Resource{{Kind: r.Kind, ID: r.ID, AccountID: owner, KeyID: key, Binding: scope.Binding, CredentialScope: "upstream-key", CreationID: "creation-" + r.ID}}, nil
	}
	first, err := ReadOwnedPage(t.Context(), actor, scope, "", 1, codec, fetch, lookup)
	if err != nil || len(first.Items) != 0 || first.Next == "" || requests.Load() != MaxScanPages {
		t.Fatal("bounded empty-page continuation failed", first, err, requests.Load())
	}
	public, _ := json.Marshal(first)
	if bytes.Contains(public, []byte("private-native")) || bytes.Contains(public, []byte("other-account")) {
		t.Fatal("shared pagination metadata leaked")
	}
	second, err := ReadOwnedPage(t.Context(), actor, scope, first.Next, 1, codec, fetch, lookup)
	if err != nil || len(second.Items) != 1 || second.Items[0].Reference.ID != "mine-4" || second.Next == "" {
		t.Fatal("owned continuation failed", second, err)
	}
	third, err := ReadOwnedPage(t.Context(), actor, scope, second.Next, 1, codec, fetch, lookup)
	if err != nil || len(third.Items) != 1 || third.Items[0].Reference.ID != "mine-5" || third.Next != "" || requests.Load() != 6 {
		t.Fatal("complete owned pagination lost or duplicated an item", third, err)
	}
	// Both keys can share an upstream credential. The cursor remains isolated by
	// platform owner/key and filters, independently of the native cursor's ACL.
	for _, change := range []string{"account", "key", "credential", "query", "parent", "kind"} {
		next, nextActor := scope, actor
		switch change {
		case "account":
			nextActor.AccountID, next.AccountID = "b", "b"
		case "key":
			nextActor.KeyID, next.KeyID = "other", "other"
		case "credential":
			next.Binding.CredentialVersion = "v2"
		case "query":
			next.QueryDigest = "changed"
		case "parent":
			next.ParentID = "other-parent"
		case "kind":
			next.Kind = "conversation"
		}
		_, err := ReadOwnedPage(t.Context(), nextActor, next, first.Next, 1, codec, fetch, lookup)
		if err == nil || requests.Load() != 6 {
			t.Fatalf("%s cursor scope changed or reached upstream: %v", change, err)
		}
	}
	corrupt := []byte(first.Next)
	if corrupt[0] == 'A' {
		corrupt[0] = 'B'
	} else {
		corrupt[0] = 'A'
	}
	if _, err := ReadOwnedPage(t.Context(), actor, scope, string(corrupt), 1, codec, fetch, lookup); err == nil || requests.Load() != 6 {
		t.Fatal("tampered cursor accepted")
	}
}

func TestOwnedPaginationRejectsMalformedOrUnboundedPages(t *testing.T) {
	codec, actor, scope := cursorFixture(t)
	lookup := func(r Reference) ([]Resource, error) {
		return []Resource{{Kind: r.Kind, ID: r.ID, AccountID: "a", KeyID: "k", Binding: scope.Binding, CredentialScope: "upstream-key", CreationID: "creation-" + r.ID}}, nil
	}
	item := ListedResource{Reference: Reference{Kind: "response", ID: "mine"}, Raw: json.RawMessage(`{"id":"mine"}`)}
	for _, name := range []string{"cycle", "too_many", "too_large", "bad_json", "wrong_parent", "wrong_kind", "long_cursor", "quarantined"} {
		t.Run(name, func(t *testing.T) {
			look := lookup
			fetch := func(_ context.Context, after string, _ int) (NativePage, error) {
				page := NativePage{Items: []ListedResource{item}}
				switch name {
				case "cycle":
					page.Items, page.Next = nil, "same"
				case "too_many":
					page.Items = append(page.Items, item)
				case "too_large":
					page.Items[0].Raw = json.RawMessage(`"` + strings.Repeat("x", MaxPageBytes) + `"`)
				case "bad_json":
					page.Items[0].Raw = json.RawMessage(`{`)
				case "wrong_parent":
					page.Items[0].Reference.ParentID = "other"
				case "wrong_kind":
					page.Items[0].Reference.Kind = "file"
				case "long_cursor":
					page.Next = strings.Repeat("x", MaxNativeToken+1)
				}
				return page, nil
			}
			if name == "quarantined" {
				look = func(Reference) ([]Resource, error) { return nil, ErrAmbiguous }
			}
			if _, err := ReadOwnedPage(t.Context(), actor, scope, "", 1, codec, fetch, look); err == nil {
				t.Fatal("unsafe page accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadOwnedPage(ctx, actor, scope, "", 1, codec, func(context.Context, string, int) (NativePage, error) {
		t.Fatal("cancelled page fetched upstream")
		return NativePage{}, nil
	}, lookup); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestContextPaginationDoesNotPinSourceChannel(t *testing.T) {
	codec, actor, scope := cursorFixture(t)
	scope.Kind, scope.Binding = "codex_item", Binding{}
	lookup := func(r Reference) ([]Resource, error) {
		return []Resource{{Kind: r.Kind, ID: r.ID, AccountID: "a", KeyID: "k"}}, nil
	}
	first, err := ReadOwnedPage(t.Context(), actor, scope, "", 1, codec, func(context.Context, string, int) (NativePage, error) {
		return NativePage{Items: []ListedResource{{Reference: Reference{Kind: "codex_item", ID: "item-1"}, Raw: json.RawMessage(`{"id":"item-1"}`)}}, Next: "portable-context-token"}, nil
	}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	// A different capability-qualified channel supplies this second page. No
	// source mapping exists in scope or lookup; only platform ACL remains.
	second, err := ReadOwnedPage(t.Context(), actor, scope, first.Next, 1, codec, func(_ context.Context, next string, _ int) (NativePage, error) {
		if next != "portable-context-token" {
			t.Fatal("native context token changed")
		}
		return NativePage{Items: []ListedResource{{Reference: Reference{Kind: "codex_item", ID: "item-2"}, Raw: json.RawMessage(`{"id":"item-2"}`)}}}, nil
	}, lookup)
	if err != nil || len(second.Items) != 1 || second.Next != "" {
		t.Fatal("context acquired source binding", second, err)
	}
}
