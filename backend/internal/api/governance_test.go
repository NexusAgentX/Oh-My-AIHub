package api

import (
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestContributionParentChoicesFilterBeforePagination(t *testing.T) {
	store := newAPIReviewStore()
	parent := "00000000-0000-4000-8000-000000000501"
	other := "00000000-0000-4000-8000-000000000502"
	store.live.Groups = []market.Group{
		{ID: "00000000-0000-4000-8000-000000000401", SupplierID: other, Name: "Other", Enabled: true},
		{ID: "00000000-0000-4000-8000-000000000402", SupplierID: parent, Name: "First", Enabled: true},
		{ID: "00000000-0000-4000-8000-000000000403", SupplierID: parent, Name: "Second", Enabled: false},
	}
	store.live.Models = []market.GroupModel{
		{ID: "00000000-0000-4000-8000-000000000404", GroupID: other, ModelID: "Other", Enabled: true},
		{ID: "00000000-0000-4000-8000-000000000405", GroupID: parent, ModelID: "First", Enabled: true},
		{ID: "00000000-0000-4000-8000-000000000406", GroupID: parent, ModelID: "Second", Enabled: false},
	}
	service := &contribution.Service{Live: store}
	p := newPlatform(t, func(d *Dependencies) { d.Contributions = service })
	admin := p.admin(t)
	for _, kind := range []string{"group", "group_model"} {
		path := "/api/contributions/targets?kind=" + kind + "&parent_id=" + parent + "&limit=1"
		first := admin.expect(t, http.StatusOK, http.MethodGet, path, nil)
		items := first["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["parent_id"] != parent || first["next_cursor"] == nil {
			t.Fatal("parent filtering happened after the page limit")
		}
		next := admin.expect(t, http.StatusOK, http.MethodGet, path+"&cursor="+first["next_cursor"].(string), nil)
		if len(next["items"].([]any)) != 1 || next["next_cursor"] != nil || next["items"].([]any)[0].(map[string]any)["enabled"] != false {
			t.Fatal("pagination lost a disabled revision target")
		}
	}
}
