package marketbrowse

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func offer(id, group string, features ...string) Offering {
	f := Facts{Supported: map[string]bool{}}
	for _, name := range features {
		f.Supported[name] = true
	}
	return Offering{Protocol: "openai-responses", ModelID: id, GroupModelID: group + "-model", GroupID: group, SupplierID: "supplier", ModelType: "text", Visible: true, Enabled: true, Format: channel.FormatOpenAIResponses, Facts: f, Channels: []Channel{{ID: group + "-channel", OwnerID: "PRIVATE-owner", Visible: true, Enabled: true, Routable: true, Facts: f, Price: &Price{Amount: 100, Unit: "tokens", Quantity: 1_000_000}}}}
}
func scope() Scope { return Scope{Version: "approved-evidence-price-v1", ActorID: "consumer"} }
func query() Query { return Query{OnlyOnline: true} }

func TestNoUnionAcrossGroupsProtocolsOrChannels(t *testing.T) {
	q := query()
	q.Features = []string{"tools", "strict_json"}
	for _, rows := range [][]Offering{
		{offer("same/model:v1", "a", "tools"), offer("same/model:v1", "b", "strict_json")},
		{offer("same/model:v1", "a", "tools"), func() Offering {
			v := offer("same/model:v1", "a", "strict_json")
			v.Format = channel.FormatAnthropic
			v.Protocol = "native-messages"
			return v
		}()},
		{func() Offering {
			v := offer("same/model:v1", "a", "tools", "strict_json")
			v.Channels = []Channel{offer("same/model:v1", "x", "tools").Channels[0], offer("same/model:v1", "y", "strict_json").Channels[0]}
			return v
		}()},
	} {
		page, err := Select(rows, scope(), q, "", 20)
		if err != nil || page.Total != 0 || len(page.Items) != 0 {
			t.Fatalf("union created a usable model: %+v %v", page, err)
		}
	}
	rows := []Offering{offer("same/model:v1", "a", "tools", "strict_json"), offer("same/model:v1", "b", "tools")}
	page, err := Select(rows, scope(), q, "", 20)
	if err != nil || page.Total != 1 || page.Items[0].ID != "same/model:v1" || len(page.Items[0].Offers) != 1 || page.Items[0].Offers[0].GroupID != "a" {
		t.Fatal("matching witness was lost or unrelated quote included", page, err)
	}
}

func TestCodexAndPublicResponsesShareNoFeatureWitness(t *testing.T) {
	public := offer("same/model", "group", "tools")
	native := offer("same/model", "group", "codex_cli_basic")
	native.Protocol = "codex-special"
	q := query()
	q.Features = []string{"tools", "codex_cli_basic"}
	page, err := Select([]Offering{public, native}, scope(), q, "", 20)
	if err != nil || page.Total != 0 {
		t.Fatal("public tools and native CLI proof made a false Codex witness", page, err)
	}
	public.Facts.Supported["codex_cli_basic"] = true
	page, _ = Select([]Offering{public}, scope(), q, "", 20)
	if page.Total != 0 {
		t.Fatal("public surface advertised native CLI compatibility")
	}
}

func TestPermissionsAndAllFiltersPrecedeCountingAndPagination(t *testing.T) {
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
			t.Fatal("cursor crossed a changed visibility/evidence scope", err)
		}
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "PRIVATE-owner") {
		t.Fatal("selector serialized a private owner/source record")
	}
}

func TestPriceSortingUsesOnlyMatchingRoutableCandidatesAndExactAmounts(t *testing.T) {
	rows := []Offering{offer("model-a", "a", "tools"), offer("model-b", "b", "tools"), offer("model-c", "c", "tools")}
	cheap := rows[0].Channels[0]
	cheap.ID = "cheap-nonmatching"
	cheap.Facts = Facts{Supported: map[string]bool{}}
	cheap.Price = &Price{Amount: 1, Unit: "tokens", Quantity: 1_000_000}
	rows[0].Channels = append(rows[0].Channels, cheap)
	rows[1].Channels[0].Price.Amount = 80
	rows[2].Channels[0].Price = nil
	q := query()
	q.Features = []string{"tools"}
	q.Sort = "price"
	page, err := Select(rows, scope(), q, "", 20)
	if err != nil || page.Items[0].ID != "model-b" || page.Items[1].ID != "model-a" || page.Items[1].Price.Amount != 100 || page.Items[2].Price != nil {
		t.Fatal("nonmatching or unknown quote affected sorting", page, err)
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

func TestIncomparableUnitsAndMissingEvidenceNeverBecomeZero(t *testing.T) {
	rows := []Offering{offer("model-a", "a", "tools"), offer("model-b", "b", "tools")}
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
	rows[0].Facts.ContextLower = 100000
	rows[0].Channels[0].Facts = Facts{}
	q = query()
	q.MinContext = 1000
	page, _ = Select(rows, scope(), q, "", 20)
	if page.Total != 0 {
		t.Fatal("group declaration/proof filled missing channel capacity")
	}
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
	q.Features = []string{"strict_json"}
	page, _ = Select([]Offering{row}, scope(), q, "", 20)
	if page.Total != 0 {
		t.Fatal("unknown report was treated as explicit capability support")
	}
}
