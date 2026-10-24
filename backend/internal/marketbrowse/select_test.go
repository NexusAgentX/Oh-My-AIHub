package marketbrowse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func offer(id, group string) Offering {
	return Offering{Protocol: "openai-responses", ModelID: id, GroupModelID: group + "-model", GroupID: group, SupplierID: "supplier", ModelType: "text", Visible: true, Enabled: true, Format: channel.FormatOpenAIResponses, Channels: []Channel{{ID: group + "-channel", OwnerID: "PRIVATE-owner", Visible: true, Enabled: true, Routable: true, Price: &Price{Amount: 100, Unit: "tokens", Quantity: 1_000_000}}}}
}
func scope() Scope { return Scope{Version: "approved-price-v1", ActorID: "consumer"} }
func query() Query { return Query{OnlyOnline: true} }

func TestPermissionsAndFiltersPrecedeCountingAndPagination(t *testing.T) {
	rows := []Offering{offer("model-a", "a"), offer("model-b", "b"), offer("model-c", "c"), offer("model-d", "d"), offer("model-e", "e")}
	rows[2].Visible = false
	rows[4].Enabled = false
	s := scope()
	s.AllowedModels = []string{"model-b", "model-c", "model-d", "model-e"}
	first, err := Select(rows, s, query(), "", 1)
	if err != nil || first.Total != 2 || len(first.Items) != 1 || first.Items[0].ID != "model-b" || first.NextCursor == nil {
		t.Fatal("permission filtering happened after pagination", first, err)
	}
	second, err := Select(rows, s, query(), *first.NextCursor, 1)
	if err != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID != "model-d" || second.NextCursor != nil {
		t.Fatal("empty/incorrect second page", second, err)
	}
	for _, changed := range []Scope{{Version: "v2", ActorID: s.ActorID, AllowedModels: s.AllowedModels}, {Version: s.Version, ActorID: "another", AllowedModels: s.AllowedModels}, {Version: s.Version, ActorID: s.ActorID, KeyID: "other-key", AllowedModels: s.AllowedModels}} {
		if _, err := Select(rows, changed, query(), *first.NextCursor, 1); !errors.Is(err, ErrPageChanged) {
			t.Fatal("cursor crossed a changed visibility scope", err)
		}
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "PRIVATE-owner") {
		t.Fatal("selector serialized a private owner/source record")
	}
}

func TestPriceSortingUsesOnlyRoutableCandidatesAndExactAmounts(t *testing.T) {
	rows := []Offering{offer("model-a", "a"), offer("model-b", "b"), offer("model-c", "c")}
	rows[1].Channels[0].Price.Amount = 80
	rows[2].Channels[0].Price = nil
	q := query()
	q.Sort = "price"
	page, err := Select(rows, scope(), q, "", 20)
	if err != nil || page.Items[0].ID != "model-b" || page.Items[1].ID != "model-a" || page.Items[1].Price.Amount != 100 || page.Items[2].Price != nil {
		t.Fatal("unknown quote affected sorting", page, err)
	}
	rows[0].Channels[0].Price.Amount = money.Amount(1<<53) + 1
	rows[1].Channels[0].Price.Amount = money.Amount(1 << 53)
	page, _ = Select(rows, scope(), q, "", 20)
	if page.Items[0].ID != "model-b" {
		t.Fatal("price comparator lost integer precision")
	}
	rows[2].Channels[0].OwnerID = "consumer"
	page, _ = Select(rows, scope(), q, "", 20)
	if page.Items[0].ID != "model-c" || page.Items[0].Price == nil || !page.Items[0].Price.SelfNetZero {
		t.Fatal("self net zero was not recognized")
	}
	rows[2].Channels[0].Routable = false
	page, _ = Select(rows, scope(), q, "", 20)
	if page.Total != 2 {
		t.Fatal("unroutable self channel authorized online model")
	}
}

func TestIncomparableUnitsNeverBecomeZero(t *testing.T) {
	rows := []Offering{offer("model-a", "a"), offer("model-b", "b")}
	rows[1].Channels[0].Price = &Price{Amount: 1, Unit: "request", Quantity: 1}
	q := query()
	q.Sort = "price"
	page, err := Select(rows, scope(), q, "", 20)
	if err != nil || !page.PriceSortFallback || page.Items[0].ID != "model-a" {
		t.Fatal("per-request and token prices were numerically compared", page, err)
	}
	rows[1].ModelID = "model-a"
	page, _ = Select(rows, scope(), q, "", 20)
	if page.Items[0].Price != nil {
		t.Fatal("one model with conflicting workload denominators fabricated a minimum")
	}
	rows = []Offering{offer("model-a", "a")}
	rows[0].Channels[0].Price = &Price{SelfNetZero: true}
	page, _ = Select(rows, scope(), query(), "", 20)
	if page.Items[0].Price != nil {
		t.Fatal("a nonowner's invalid price was treated as free")
	}
}

func TestOfflineQuotesStayExplicitAndModelIdentityPreservesNamespaces(t *testing.T) {
	row := offer("Vendor/model:2026", "group")
	row.Channels = nil
	q := query()
	page, _ := Select([]Offering{row}, scope(), q, "", 20)
	if page.Total != 0 {
		t.Fatal("no-channel quote advertised online availability")
	}
	q.OnlyOnline = false
	page, err := Select([]Offering{row}, scope(), q, "", 20)
	if err != nil || page.Total != 1 || page.Items[0].ID != "Vendor/model:2026" || page.Items[0].OnlineChannels != 0 || page.Items[0].Price != nil {
		t.Fatal("offline identity or unknown price fabricated", page, err)
	}
}

func TestRetiredCodexProtocolIsNotAFilter(t *testing.T) {
	for _, q := range []Query{{Protocol: "codex-special"}, {Format: channel.Format("codex-special")}} {
		if _, err := Select([]Offering{offer("model-a", "a")}, scope(), q, "", 20); !errors.Is(err, ErrFilter) {
			t.Fatalf("%+v: retired protocol accepted", q)
		}
	}
}

func TestSupplierAndGroupScopesUnionWithoutOrderDependence(t *testing.T) {
	rows := []Offering{offer("model-a", "a"), offer("model-b", "b"), offer("model-c", "c")}
	rows[1].SupplierID = "supplier-2"
	rows[2].SupplierID = "supplier-2"
	q := query()
	q.SupplierIDs = []string{"supplier"}
	q.GroupIDs = []string{"b"}
	page, err := Select(rows, scope(), q, "", 20)
	if err != nil || page.Total != 2 || page.Items[0].ID != "model-a" || page.Items[1].ID != "model-b" {
		t.Fatal("supplier scope and explicit group were not unioned", page, err)
	}
	q.GroupIDs = []string{"c", "b", "b"}
	page, err = Select(rows, scope(), q, "", 20)
	if err != nil || page.Total != 3 {
		t.Fatal("repeated group scopes did not union and deduplicate", page, err)
	}

	first := query()
	first.GroupIDs = []string{"a", "b"}
	second := query()
	second.GroupIDs = []string{"b", "a", "a"}
	one, err := Select(rows, scope(), first, "", 1)
	if err != nil || one.NextCursor == nil {
		t.Fatal("fixture did not paginate", one, err)
	}
	expected, err := Select(rows, scope(), first, *one.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Select(rows, scope(), second, *one.NextCursor, 1)
	if err != nil || resumed.Total != 2 || len(resumed.Items) != 1 || resumed.Items[0].ID != expected.Items[0].ID {
		t.Fatal("scope order changed the cursor binding", resumed, expected, err)
	}

	tooMany := Query{SupplierIDs: make([]string, maxFilterScopes+1)}
	for i := range tooMany.SupplierIDs {
		tooMany.SupplierIDs[i] = fmt.Sprintf("supplier-%d", i)
	}
	for _, bad := range []Query{{GroupIDs: []string{strings.Repeat("x", 129)}}, tooMany} {
		if _, err := Select(rows, scope(), bad, "", 20); !errors.Is(err, ErrFilter) {
			t.Fatalf("%+v: oversized scope accepted", bad)
		}
	}
}
