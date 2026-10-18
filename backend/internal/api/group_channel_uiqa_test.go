//go:build uiqa

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Local, explicit synthetic browser harness. It exercises real services and
// handlers without a database, personal login, paid Key or supplier evidence.
func TestGroupChannelUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_GROUP_CHANNEL_UI_QA") != "1" {
		t.Skip("explicit synthetic browser QA only")
	}
	ctx := context.Background()
	repo := &apiGroupChannels{rows: map[string]groupchannel.Record{}, choices: map[string][]groupchannel.Choice{}}
	runtime := &groupchannel.Runtime{Repository: repo}
	runtime.Rebuild = func(ctx context.Context) error {
		_, err := runtime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	service := &groupchannel.Service{Repository: repo, Runtime: runtime}
	p := newPlatform(t, func(d *Dependencies) {
		service.Keyring = d.Channels.Keyring
		d.GroupChannels = service
		d.CookieSecure = false
	})
	admin := &client{spec: p.spec, handler: p.handler}
	account := admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})["account"].(map[string]any)
	owner := account["id"].(string)
	uuid := func() string {
		id, err := ids.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sid, gid := uuid(), uuid()
	ua := "AIHub-Channel-QA/1"
	rpm, concurrency, ttft := int32(120), int32(8), int32(30000)
	g := market.Group{ID: gid, SupplierID: sid, Name: "标准分组", Description: "合成分组配置", Enabled: true, Revision: 3, ConnectionVersion: 4, Defaults: market.Defaults{UserAgent: &ua, RPMLimit: &rpm, ConcurrencyLimit: &concurrency, TTFTTimeoutMS: &ttft, HeaderRules: &market.HeaderRules{Set: []market.HeaderSet{{Name: "X-Engine", Value: "shared"}}}}}
	g.CapabilityDigest = market.GroupCapabilityDigest(g)
	d := market.Directory{Suppliers: []market.Supplier{{ID: sid, Name: "合成供应商", Enabled: true}}, Groups: []market.Group{g}}
	for i, label := range []string{"CF 入口", "直连入口", "CDN 入口"} {
		aid := uuid()
		d.Addresses = append(d.Addresses, market.Address{ID: aid, SupplierID: sid, Label: label, BaseURL: fmt.Sprintf("https://entry-%d.synthetic.example", i+1)})
		d.GroupAddresses = append(d.GroupAddresses, market.GroupAddress{ID: uuid(), GroupID: gid, SupplierID: sid, AddressID: aid, Position: int32(i)})
	}
	for i, name := range []string{"合成聊天模型", "合成长上下文模型", "合成图像模型"} {
		mid, qid := uuid(), uuid()
		typ := "text"
		if i == 2 {
			typ = "image"
		}
		m := market.GroupModel{ID: mid, GroupID: gid, ModelID: fmt.Sprintf("fixture/model-%d:2026", i+1), UpstreamModel: fmt.Sprintf("fixture/model-%d:2026", i+1), DisplayName: name, ModelType: typ, Enabled: true, Revision: 1, QuoteID: &qid}
		m.CapabilityDigest = market.ModelCapabilityDigest(g.CapabilityDigest, m)
		d.Models = append(d.Models, m)
		d.Quotes = append(d.Quotes, market.Quote{ID: qid, GroupModelID: mid, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.Amount(money.Scale), Output: 3 * money.Amount(money.Scale)}})
	}
	if err := runtime.Install(ctx, d); err != nil {
		t.Fatal(err)
	}
	v, err := service.Create(ctx, groupchannel.Actor{ID: owner}, groupchannel.CreateInput{GroupID: gid, APIKey: "synthetic-ui-key", Name: "合成分组渠道", Summary: "同组多入口，按所选顺序回退", AddressIDs: []string{d.GroupAddresses[1].ID, d.GroupAddresses[0].ID}, Models: []groupchannel.Choice{{GroupModelID: d.Models[0].ID, DiscountNano: 800_000_000}, {GroupModelID: d.Models[2].ID, Excluded: true, DiscountNano: 700_000_000}}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := repo.Get(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range d.Models {
		if i == 2 {
			continue
		}
		reason := "upstream_rejected_probe"
		status, duration := 200, 42
		test := channel.FormatTest{OK: i == 0, StatusCode: &status, DurationMS: &duration, TestedAt: time.Now().UTC()}
		if i == 1 {
			test.Error = &reason
			status = 403
		}
		repo.probes = append(repo.probes, groupchannel.Probe{ChannelID: v.ID, GroupModelID: m.ID, Binding: groupchannel.ProbeBinding(record, g, m), Tests: map[channel.Format]channel.FormatTest{channel.FormatOpenAIChat: test}, TestedAt: time.Now().UTC()})
	}
	if err = runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	normalProbes := reviewClone(repo.probes)
	// Existing statistics/call endpoints resolve the same common channel ID.
	p.channels.mu.Lock()
	p.channels.channels = append(p.channels.channels, &fakeStoredChannel{Channel: channel.Channel{ID: v.ID, Owner: channel.Owner{ID: owner, Username: "qaadmin", DisplayName: "合成验收管理员"}, Name: v.Name, GroupID: &gid, Status: channel.StatusListed, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, credential: record.Credential})
	p.channels.mu.Unlock()
	var mode atomic.Value
	mode.Store("normal")
	stopped := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:18490", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__uiqa/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			select {
			case <-stopped:
			default:
				close(stopped)
			}
			return
		}
		if r.URL.Path == "/__uiqa/mode" && r.Method == "POST" {
			next := r.URL.Query().Get("value")
			mode.Store(next)
			repo.mu.Lock()
			row := repo.rows[v.ID]
			row.ProbeEpoch = 1
			row.Status = channel.StatusListed
			row.SuspendedReason = nil
			repo.probes = reviewClone(normalProbes)
			if next == "processing" {
				row.ProbeEpoch = 2
				repo.probes = nil
			}
			if next == "disabled" {
				row.Status = channel.StatusSuspended
				reason := "合成治理状态：渠道已下架"
				row.SuspendedReason = &reason
			}
			repo.rows[v.ID] = row
			repo.mu.Unlock()
			if err := runtime.Refresh(ctx); err != nil {
				t.Error(err)
			}
			w.WriteHeader(204)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/group-channels") {
			if mode.Load() == "error" {
				writeError(w, 503, "synthetic_unavailable", "合成测试：渠道服务暂不可用")
				return
			}
			if mode.Load() == "empty" && r.URL.Path == "/api/group-channels" {
				writeJSON(w, 200, map[string]any{"items": []any{}})
				return
			}
			if mode.Load() == "forbidden" && r.URL.Path == "/api/group-channels/"+v.ID {
				writeError(w, 403, "forbidden", "无权查看此渠道")
				return
			}
		}
		p.handler.ServeHTTP(w, r)
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			close(stopped)
		}
	}()
	fmt.Printf("UI_QA_READY channel=%s group=%s api=http://127.0.0.1:18490\n", v.ID, gid)
	<-stopped
	shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
