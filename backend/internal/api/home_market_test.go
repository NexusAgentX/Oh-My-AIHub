package api

import (
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestHomeUsesCurrentGroupCountsAndKeepsHistoricalRevenue(t *testing.T) {
	p, _ := marketModelFixture(t, "approved-model", nil)
	viewer, _ := p.member(t, "home-market")
	p.browse.home = gateway.HomeStats{ChannelsTotal: 9, ChannelsOnline: 7, RevenueToday: money.FromNano(money.Scale)}
	page := viewer.expect(t, 200, "GET", "/api/home", nil)
	counts := page["channels"].(map[string]any)
	if counts["total"] != float64(0) || counts["online"] != float64(0) || counts["today_revenue"] != "1" {
		t.Fatal("retired counts leaked or historical revenue lost", counts)
	}
}

func TestHomeClosesBeforeDefaultKeyCreationWhenMarketIsUnavailable(t *testing.T) {
	for _, runtime := range []*groupchannel.Runtime{nil, {Ready: func() error { return errors.New("PRIVATE-publication-failure") }}} {
		p := newPlatform(t, func(d *Dependencies) { d.GroupChannels = &groupchannel.Service{Runtime: runtime} })
		viewer, _ := p.member(t, "home-publication")
		before := viewer.expect(t, 200, "GET", "/api/keys", nil)
		if len(before["items"].([]any)) != 0 {
			t.Fatal("fixture already has a Key")
		}
		page := viewer.expect(t, 503, "GET", "/api/home", nil)
		if page["error"] != "group_channels_unavailable" {
			t.Fatal("private publication failure escaped", page)
		}
		after := viewer.expect(t, 200, "GET", "/api/keys", nil)
		if len(after["items"].([]any)) != 0 {
			t.Fatal("unavailable home created a default Key")
		}
	}
}
