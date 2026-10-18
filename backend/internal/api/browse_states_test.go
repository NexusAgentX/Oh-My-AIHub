package api

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

// 模型页的渠道行把成功率、首字延迟、冷却剩余写成 null 或字符串；
// 这些分支由处理器内联拼装，门禁只校验形状，这里钉住各状态下的取值。
func TestModelPageReportsEachChannelsState(t *testing.T) {
	const idPrefix = "00000000-0000-4000-8000-00000000"
	one := int32(1)
	records := []groupchannel.Record{
		{ID: idPrefix + "a001", Name: "无统计"},
		{ID: idPrefix + "a002", Name: "无首字延迟"},
		{ID: idPrefix + "a003", Name: "有首字延迟"},
		{ID: idPrefix + "a004", Name: "冷却中", Overrides: market.Defaults{CooldownFailures: &one}},
		{ID: idPrefix + "a005", Name: "并发已满", Overrides: market.Defaults{ConcurrencyLimit: &one}},
	}
	p, engine := marketModelFixture(t, "m1", records)
	admin := p.admin(t)
	now := time.Now()
	for range 3 {
		p.stats.Record(idPrefix+"a002", "m1", false, gateway.Attempt{EndReason: gateway.EndCompleted}, now)
	}
	p.stats.Record(idPrefix+"a002", "m1", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	seedChannelHealth(p.stats, idPrefix+"a003", "m1")

	// A cooldown and a held concurrency slot give the "cooldown" and "limited" states.
	engine.State().Failure(idPrefix+"a004", gateway.Limits{CooldownAfter: 1, CooldownFor: time.Hour}, time.Now(), "test")
	release, ok, _ := engine.State().Begin(idPrefix+"a005", gateway.Limits{Concurrency: 1}, time.Now())
	if !ok {
		t.Fatal("could not hold a concurrency slot")
	}
	defer release()

	page := admin.expect(t, http.StatusOK, http.MethodGet, "/api/market/models/m1", nil)
	type row struct {
		state    string
		metrics  int
		cooldown any
	}
	want := map[string]row{
		"无统计":   {state: "available"},
		"无首字延迟": {state: "available"}, // Legacy aggregate cannot populate the new model scope.
		"有首字延迟": {state: "available"},
		"冷却中":   {state: "cooldown"},
		"并发已满":  {state: "limited"},
	}
	got := map[string]row{}
	for _, entry := range page["routing_channels"].([]any) {
		channelRow := entry.(map[string]any)
		got[channelRow["name"].(string)] = row{
			state: channelRow["state"].(string), metrics: len(channelRow["routing_metrics"].([]any)),
			cooldown: channelRow["cooldown_remaining_seconds"],
		}
	}
	// The cooldown row also reports the seconds left, which depends on the clock.
	if seconds, _ := got["冷却中"].cooldown.(float64); seconds < 3590 || seconds > 3601 {
		t.Fatalf("cooldown remaining = %v", got["冷却中"].cooldown)
	}
	for name, entry := range got {
		entry.cooldown = nil
		got[name] = entry
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("channel rows = %+v, want %+v", got, want)
	}
}
