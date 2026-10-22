//go:build uiqa

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Explicit loopback-only fixture. All directory data is synthetic.
func TestMarketUIHarness(t *testing.T) {
	if os.Getenv("AIHUB_MARKET_UI_QA") != "1" {
		t.Skip("explicit synthetic browser QA only")
	}
	ctx := context.Background()
	repo := &browseMarketRepository{}
	runtime := &groupchannel.Runtime{Repository: repo}
	runtime.Rebuild = func(ctx context.Context) error {
		_, err := runtime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	var engine *gateway.Engine
	p := newPlatform(t, func(d *Dependencies) {
		d.CookieSecure = false
		engine = d.Gateway
		d.GroupChannels = &groupchannel.Service{Runtime: runtime, Repository: repo}
		d.Keys = apikey.NewService(newFakeKeyStore(), d.Gateway.Keyring, func(context.Context) ([]string, error) {
			return []string{"fixture/model:2026", "fixture/image:2026", "fixture/unknown:2026"}, nil
		})
	})
	admin := &client{spec: p.spec, handler: p.handler}
	account := admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "qaadmin", "display_name": "合成验收管理员", "password": "Synthetic-QA-2026"})["account"].(map[string]any)
	owner := account["id"].(string)
	d := market.Directory{}
	now := time.Now().UTC()
	for i, kind := range []string{"official_api", "relay", "official_subscription"} {
		sid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 10+i)
		gid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 20+i)
		aid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 30+i)
		link := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 40+i)
		mid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 50+i)
		qid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 60+i)
		cid := fmt.Sprintf("f0000000-0000-4000-8000-%012d", 70+i)
		model := "fixture/model:2026"
		typ := "text"
		format := channel.FormatOpenAIResponses
		if i == 2 {
			model = "fixture/image:2026"
			typ = "image"
			format = channel.FormatOpenAIImages
		}
		d.Suppliers = append(d.Suppliers, market.Supplier{ID: sid, Name: []string{"合成官方 API", "合成中转站", "合成订阅分类"}[i], Kind: kind, Enabled: true, Summary: "本地合成目录，无真实上游", WebsiteURL: "https://synthetic.example", Description: "## 合成验收数据\n用于核对模型聚合、报价、渠道与公开报告。"})
		g := market.Group{ID: gid, SupplierID: sid, Name: []string{"标准分组", "共享引擎分组", "图片分组"}[i], Description: "多入口属于同一上游引擎", Enabled: true, Revision: 1, ConnectionVersion: 1}
		g.CapabilityDigest = market.GroupCapabilityDigest(g)
		d.Groups = append(d.Groups, g)
		m := market.GroupModel{ID: mid, GroupID: gid, ModelID: model, DisplayName: []string{"显示名甲", "显示名乙", "合成图片模型"}[i], UpstreamModel: model, ModelType: typ, Enabled: true, Revision: 1, QuoteID: &qid}
		m.CapabilityDigest = market.ModelCapabilityDigest(g.CapabilityDigest, m)
		d.Models = append(d.Models, m)
		d.Quotes = append(d.Quotes, market.Quote{ID: qid, GroupModelID: mid, Version: 1, Currency: []string{"CNY", "USD", "CNY"}[i], BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.FromNano(int64(i) * money.Scale), Output: money.FromNano(int64(i+1) * money.Scale)}})
		d.Addresses = append(d.Addresses, market.Address{ID: aid, SupplierID: sid, Label: "合成入口", BaseURL: "https://private.synthetic.example"})
		d.GroupAddresses = append(d.GroupAddresses, market.GroupAddress{ID: link, GroupID: gid, SupplierID: sid, AddressID: aid})
		channelOwner := "f0000000-0000-4000-8000-000000000090"
		if i == 1 {
			channelOwner = owner
		}
		row := groupchannel.Record{ID: cid, Owner: channel.Owner{ID: channelOwner}, OwnerActive: true, Name: []string{"有统计的合成渠道", "自己的合成渠道", "暂无速率的图片渠道"}[i], Summary: "本地验收渠道", GroupID: gid, Status: channel.StatusListed, Revision: 1, CredentialRevision: 1, AddressIDs: []string{link}, CreatedAt: now, UpdatedAt: now}
		repo.data.Channels = append(repo.data.Channels, row)
		repo.data.Probes = append(repo.data.Probes, groupchannel.Probe{ChannelID: cid, GroupModelID: mid, Binding: groupchannel.ProbeBinding(row, g, m), Tests: map[channel.Format]channel.FormatTest{format: {OK: true, TestedAt: now}}})
	}
	// A model with no candidate remains an explicit offline/unknown directory row.
	qid := "f0000000-0000-4000-8000-000000000080"
	d.Models = append(d.Models, market.GroupModel{ID: "f0000000-0000-4000-8000-000000000081", GroupID: d.Groups[0].ID, ModelID: "fixture/unknown:2026", UpstreamModel: "fixture/unknown:2026", ModelType: "text", Enabled: true, Revision: 1, QuoteID: &qid})
	d.Quotes = append(d.Quotes, market.Quote{ID: qid, GroupModelID: d.Models[3].ID, Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"})
	if err := runtime.Install(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := engine.LoadStats(ctx); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual handler and contract before exposing the fixture.
	for _, path := range []string{"/api/market/models", "/api/market/models/fixture%2Fmodel:2026", "/api/market/suppliers", "/api/market/groups/" + d.Groups[0].ID, "/api/market/channels"} {
		admin.expect(t, 200, "GET", path, nil)
	}
	var mode atomic.Value
	mode.Store("normal")
	stopped := make(chan struct{})
	var stop sync.Once
	server := &http.Server{Addr: "127.0.0.1:18491", ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__uiqa/stop" {
			w.WriteHeader(204)
			stop.Do(func() { close(stopped) })
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__uiqa/mode" {
			mode.Store(r.URL.Query().Get("value"))
			w.WriteHeader(204)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/market/") {
			switch mode.Load() {
			case "error":
				writeError(w, 503, "synthetic_unavailable", "合成验收：目录暂不可用")
				return
			case "forbidden":
				writeError(w, 403, "forbidden", "合成验收：没有访问权限")
				return
			case "processing":
				timer := time.NewTimer(10 * time.Second)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
			case "empty":
				recorder := httptest.NewRecorder()
				p.handler.ServeHTTP(recorder, r)
				var page map[string]any
				if recorder.Code == 200 && json.Unmarshal(recorder.Body.Bytes(), &page) == nil {
					if _, ok := page["items"]; ok {
						page["items"] = []any{}
						page["total"] = 0
						page["next_cursor"] = nil
						writeJSON(w, 200, page)
						return
					}
				}
			}
		}
		p.handler.ServeHTTP(w, r)
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			stop.Do(func() { close(stopped) })
		}
	}()
	fmt.Printf("UI_QA_READY market api=http://127.0.0.1:18491 group=%s model=fixture/model:2026\n", d.Groups[0].ID)
	<-stopped
	shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
