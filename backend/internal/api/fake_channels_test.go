package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// fakeChannelStore is an in-memory channel.Store. Every channel reports the
// same non-trivial "today" statistics so the response shape is exercised.
type fakeChannelStore struct {
	mu        sync.Mutex
	accounts  *fakeStore
	clock     time.Time
	channels  []*fakeStoredChannel
	events    map[string][]channel.Event
	nextEvent int64
}

type fakeStoredChannel struct {
	channel.Channel
	credential channel.EncryptedCredential
}

func newFakeChannelStore(accounts *fakeStore) *fakeChannelStore {
	return &fakeChannelStore{accounts: accounts, clock: time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC), events: map[string][]channel.Event{}}
}

func (s *fakeChannelStore) owner(id string) channel.Owner {
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	account := s.accounts.accounts[id]
	return channel.Owner{ID: id, Username: account.Username, DisplayName: account.DisplayName}
}

func (s *fakeChannelStore) tick() time.Time {
	s.clock = s.clock.Add(time.Minute)
	return s.clock
}

func (s *fakeChannelStore) record(id, kind, reason string) {
	s.nextEvent++
	s.events[id] = append(s.events[id], channel.Event{ID: s.nextEvent, Kind: kind, Reason: reason, CreatedAt: s.tick()})
}

func (s *fakeChannelStore) find(id string) (*fakeStoredChannel, error) {
	for _, item := range s.channels {
		if item.ID == id {
			return item, nil
		}
	}
	return nil, channel.ErrNotFound
}

func (s *fakeChannelStore) snapshot(item *fakeStoredChannel) channel.Channel {
	copied := item.Channel
	copied.Models = slices.Clone(item.Models)
	return copied
}

func (s *fakeChannelStore) Create(_ context.Context, n channel.NewChannel) (channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rate := 0.5
	created := s.tick()
	item := &fakeStoredChannel{Channel: channel.Channel{
		ID: n.ID, Owner: s.owner(n.OwnerID), Name: n.Name, BaseURL: n.BaseURL, Status: n.Status, Advanced: n.Advanced,
		Models: slices.Clone(n.Models), Today: channel.Today{Revenue: money.FromNano(1_500_000_000), Calls: 2, SuccessRate: &rate},
		CreatedAt: created, UpdatedAt: created,
	}, credential: n.Credential}
	s.channels = append(s.channels, item)
	kind := channel.EventListed
	if n.Status == channel.StatusUnlisted {
		kind = channel.EventUnlisted
	}
	s.record(n.ID, kind, "")
	return s.snapshot(item), nil
}

func (s *fakeChannelStore) Get(_ context.Context, id string) (channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return channel.Channel{}, err
	}
	return s.snapshot(item), nil
}

func (s *fakeChannelStore) ListByOwner(_ context.Context, ownerID string) ([]channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := []channel.Channel{}
	for _, item := range s.channels {
		if item.Owner.ID == ownerID {
			channels = append(channels, s.snapshot(item))
		}
	}
	return channels, nil
}

func (s *fakeChannelStore) ListAll(_ context.Context, filter channel.AdminFilter) ([]channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched []*fakeStoredChannel
	for _, item := range s.channels {
		if (filter.Status != "" && item.Status != filter.Status) || (filter.OwnerID != "" && item.Owner.ID != filter.OwnerID) ||
			(filter.Query != "" && !strings.Contains(item.Name, filter.Query)) {
			continue
		}
		matched = append(matched, item)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	if filter.AfterKey != "" {
		stamp, id, _ := strings.Cut(filter.AfterKey, "|")
		after, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || id == "" {
			return nil, channel.ErrInvalidInput
		}
		matched = slices.DeleteFunc(matched, func(item *fakeStoredChannel) bool { return !item.CreatedAt.Before(after) })
	}
	if len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	channels := []channel.Channel{}
	for _, item := range matched {
		channels = append(channels, s.snapshot(item))
	}
	return channels, nil
}

func (s *fakeChannelStore) Update(_ context.Context, id string, update channel.Update) (channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return channel.Channel{}, err
	}
	if update.Name != nil {
		item.Name = *update.Name
	}
	if update.BaseURL != nil {
		item.BaseURL = *update.BaseURL
	}
	if update.Credential != nil {
		item.credential = *update.Credential
	}
	if update.Status != nil && *update.Status != item.Status {
		item.Status = *update.Status
		kind := channel.EventListed
		if item.Status == channel.StatusUnlisted {
			kind = channel.EventUnlisted
		}
		s.record(id, kind, "")
	}
	if update.Advanced != nil {
		item.Advanced = *update.Advanced
	}
	if update.Models != nil {
		item.Models = slices.Clone(*update.Models)
	}
	item.UpdatedAt = s.tick()
	return s.snapshot(item), nil
}

func (s *fakeChannelStore) SoftDelete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return err
	}
	s.channels = slices.DeleteFunc(s.channels, func(candidate *fakeStoredChannel) bool { return candidate == item })
	return nil
}

func (s *fakeChannelStore) Credential(_ context.Context, id string) (channel.EncryptedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return channel.EncryptedCredential{}, err
	}
	return item.credential, nil
}

func (s *fakeChannelStore) Events(_ context.Context, id string, limit int) ([]channel.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := []channel.Event{}
	for index := len(s.events[id]) - 1; index >= 0 && len(events) < limit; index-- {
		events = append(events, s.events[id][index])
	}
	return events, nil
}

func (s *fakeChannelStore) RecordEvent(_ context.Context, id, kind, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(id, kind, reason)
	return nil
}

func (s *fakeChannelStore) SaveTests(_ context.Context, id string, results []channel.TestOutcome, testedAt time.Time, apply bool) (channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return channel.Channel{}, err
	}
	item.Models = slices.Clone(item.Models)
	for _, result := range results {
		for index := range item.Models {
			model := &item.Models[index]
			if model.ModelID != result.ModelID {
				continue
			}
			tests := map[channel.Format]channel.FormatTest{}
			for format, test := range model.FormatTests {
				tests[format] = test
			}
			tests[result.Format] = channel.FormatTest{
				OK: result.OK, StatusCode: result.StatusCode, Error: result.Error, DurationMS: &result.DurationMS, TestedAt: testedAt,
			}
			model.FormatTests = tests
			if apply && !result.OK && len(model.Formats) > 1 {
				model.Formats = slices.DeleteFunc(slices.Clone(model.Formats), func(format channel.Format) bool { return format == result.Format })
			}
		}
	}
	item.UpdatedAt = s.tick()
	return s.snapshot(item), nil
}

func (s *fakeChannelStore) moderate(id string, status channel.Status, kind, reason string) (channel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.find(id)
	if err != nil {
		return channel.Channel{}, err
	}
	item.Status = status
	item.SuspendedReason = nil
	if status == channel.StatusSuspended {
		item.SuspendedReason = &reason
	}
	item.UpdatedAt = s.tick()
	s.record(id, kind, reason)
	return s.snapshot(item), nil
}

func (s *fakeChannelStore) Suspend(_ context.Context, _, id, reason string) (channel.Channel, error) {
	return s.moderate(id, channel.StatusSuspended, channel.EventSuspended, reason)
}

func (s *fakeChannelStore) Unsuspend(_ context.Context, _, id, reason string) (channel.Channel, error) {
	return s.moderate(id, channel.StatusListed, channel.EventUnsuspended, reason)
}

// ---- the listed-channel read side: model pages and the home page ----

type fakeBrowse struct {
	channels *fakeChannelStore
	home     gateway.HomeStats
	recent   []gateway.CallSummary
	revenue  money.Amount
}

func (b *fakeBrowse) OnlineChannels(context.Context) ([]gateway.OnlineChannel, error) {
	b.channels.mu.Lock()
	defer b.channels.mu.Unlock()
	online := []gateway.OnlineChannel{}
	for _, item := range b.channels.channels {
		if item.Status != channel.StatusListed {
			continue
		}
		for _, model := range item.Models {
			if model.Enabled {
				online = append(online, gateway.OnlineChannel{
					ModelID: model.ModelID, ChannelID: item.ID, ChannelName: item.Name, OwnerID: item.Owner.ID, OwnerName: item.Owner.DisplayName,
					Formats: model.Formats, MultiplierNano: model.MultiplierNano, Advanced: item.Advanced,
				})
			}
		}
	}
	return online, nil
}

func (b *fakeBrowse) Home(context.Context, string, time.Time) (gateway.HomeStats, error) {
	return b.home, nil
}

func (b *fakeBrowse) RecentCalls(_ context.Context, _ string, limit int) ([]gateway.CallSummary, error) {
	if len(b.recent) > limit {
		return b.recent[:limit], nil
	}
	return b.recent, nil
}

func (b *fakeBrowse) ChannelRevenue(context.Context, string, time.Time) (money.Amount, error) {
	return b.revenue, nil
}

// fakeGatewayStore backs only what the model pages read from the gateway engine.
type fakeGatewayStore struct {
	gateway.Store
	channels *fakeChannelStore
}

func (s fakeGatewayStore) ChannelStats(context.Context) (map[string]gateway.ChannelStats, error) {
	s.channels.mu.Lock()
	defer s.channels.mu.Unlock()
	median := 420.0
	stats := map[string]gateway.ChannelStats{}
	for _, item := range s.channels.channels {
		stats[item.ID] = gateway.ChannelStats{Attempts: 10, Successes: 9, TTFTP50MS: &median}
	}
	return stats, nil
}

// ---- a loopback stand-in for the pinned egress policy ----

type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

// testOutbound keeps the real URL normalization and validation but sends
// every request to a loopback test server, which the real policy refuses.
type testOutbound struct {
	channel.Outbound
	target *url.URL
}

func newTestOutbound(t *testing.T, upstream *httptest.Server) testOutbound {
	t.Helper()
	policy, err := channel.NewOutboundPolicyWithResolver(nil, nil, publicResolver{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return testOutbound{Outbound: policy, target: target}
}

type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme, rewritten.URL.Host, rewritten.Host = t.target.Scheme, t.target.Host, t.target.Host
	return http.DefaultTransport.RoundTrip(rewritten)
}

func (o testOutbound) client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout, Transport: rewriteTransport{o.target},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (o testOutbound) Client(_ context.Context, _ string, timeout time.Duration) (*http.Client, error) {
	return o.client(timeout), nil
}

func (o testOutbound) GatewayClient(_ context.Context, _ string, timeout time.Duration) (*http.Client, error) {
	return o.client(timeout), nil
}

func (o testOutbound) WithExtraBlockedHosts(hosts []string) (channel.Outbound, error) {
	inner, err := o.Outbound.WithExtraBlockedHosts(hosts)
	if err != nil {
		return nil, err
	}
	return testOutbound{Outbound: inner, target: o.target}, nil
}

// newFakeUpstream is a relay that lists two models and streams a completed
// OpenAI chat answer, which is all discovery and the format test ask for.
func newFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"deepseek-chat"},{"id":"some-unlisted-model"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
