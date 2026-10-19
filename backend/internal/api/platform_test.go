package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity/identitytest"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// platform 是装配了全部领域的内存实现的处理器：密钥与路由、渠道与模型浏览、
// 调用观测、论坛。契约覆盖门禁要求的各个接口都经由它按真实 HTTP 往返并按规范校验。
type platform struct {
	spec     *openAPISpec
	rates    *fakeRateSource
	store    *fakeStore
	channels *fakeChannelStore
	browse   *fakeBrowse
	observe  *fakeObserveStore
	c2c      *fakeC2CStore
	bus      *fakeEvents
	stats    *gateway.StatsBook
	handler  http.Handler
	groups   *groupchannel.Service
}

// fakeEvents 是进程内网关事件总线的替身，用于驱动实时流。
type fakeEvents struct {
	mu          sync.Mutex
	subscribers []chan gateway.Event
	ready       chan struct{}
	once        sync.Once
}

func (b *fakeEvents) Subscribe(buffer int) (<-chan gateway.Event, func()) {
	events := make(chan gateway.Event, buffer)
	b.mu.Lock()
	b.subscribers = append(b.subscribers, events)
	b.mu.Unlock()
	b.once.Do(func() { close(b.ready) })
	return events, func() {}
}

func (b *fakeEvents) publish(event gateway.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		subscriber <- event
	}
}

const testKeyring = "k1=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func newPlatform(t *testing.T, options ...func(*Dependencies)) *platform {
	t.Helper()
	store := newFakeStore()
	identityService, err := identitytest.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := channel.ParseKeyring(testKeyring, "k1")
	if err != nil {
		t.Fatal(err)
	}
	c2cKeyring, err := c2c.ParseKeyring(testKeyring, "k1")
	if err != nil {
		t.Fatal(err)
	}
	groupRepo := &apiGroupChannels{rows: map[string]groupchannel.Record{}, choices: map[string][]groupchannel.Choice{}}
	groupRuntime := &groupchannel.Runtime{Repository: groupRepo}
	groupRuntime.Rebuild = func(ctx context.Context) error {
		_, err := groupRuntime.Wrap(groupBaseLoader{}).LoadCandidates(ctx)
		return err
	}
	if err := groupRuntime.Install(context.Background(), market.Directory{}); err != nil {
		t.Fatal(err)
	}
	groupService := &groupchannel.Service{Runtime: groupRuntime, Repository: groupRepo, Keyring: keyring}
	models := platformModelReader{runtime: groupRuntime}
	settingsService := settings.NewService(store)
	outbound := newTestOutbound(t, newFakeUpstream(t))
	channelStore := newFakeChannelStore(store)
	routingStore := newFakeRoutingStore()
	knownModels := func(ctx context.Context) ([]string, error) {
		models, err := models.List(ctx, true)
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids, err
	}
	stats := gateway.NewStatsBook()
	engine := gateway.NewEngine(gateway.Dependencies{
		Catalog: models, Settings: settingsService, Routing: routingStore, Keyring: keyring, Outbound: outbound, StatsBook: stats,
	})
	observeStore := &fakeObserveStore{channels: channelStore}
	observeService := observe.NewService(observeStore)
	bus := &fakeEvents{ready: make(chan struct{})}
	feed := observe.NewFeed(observeService, bus, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go feed.Run(ctx)
	<-bus.ready
	browse := &fakeBrowse{channels: channelStore}

	c2cStore := newFakeC2CStore(store)
	rates := &fakeRateSource{rateNano: 7_100_000_000}
	dependencies := Dependencies{
		Identity: identityService, GroupChannels: groupService, Ledger: ledger.NewService(store), Settings: settingsService,
		RateSync: settings.NewRateSyncer(store, rates),
		Audit:    audit.NewService(store), Keys: apikey.NewService(newFakeKeyStore(), keyring, knownModels),
		Routing: routingStore, Gateway: engine, Browse: browse, C2C: c2c.NewService(c2cStore, c2cKeyring),
		Observe: observeService, Feed: feed, Forum: forum.NewService(newFakeForumStore(store)), CookieSecure: true,
	}
	for _, option := range options {
		option(&dependencies)
	}
	handler := NewHandler(dependencies)
	return &platform{spec: loadOpenAPI(t), rates: rates, store: store, channels: channelStore, browse: browse, observe: observeStore, c2c: c2cStore, bus: bus, stats: stats, handler: handler, groups: groupService}
}

func (p *platform) admin(t *testing.T) *client {
	t.Helper()
	return bootstrap(t, p.spec, p.handler)
}

// member 直接在内存里建立一个已完成首次改密的成员及其会话，不经过登录与改密，
// 省去每个测试里重复的口令哈希；登录与改密本身由 flow_test.go 覆盖。
func (p *platform) member(t *testing.T, username string) (*client, string) {
	t.Helper()
	account, err := p.store.CreateAccount(context.Background(), identity.NewAccount{Username: username, DisplayName: "用户" + username, PasswordHash: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	session := identity.Session{TokenHash: hash[:], AccountID: account.ID, PasswordVersion: account.PasswordVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if err := p.store.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return &client{spec: p.spec, handler: p.handler, cookie: &http.Cookie{Name: defaultSessionCookie, Value: token}}, account.ID
}
