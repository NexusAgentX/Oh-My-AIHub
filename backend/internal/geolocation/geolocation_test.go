package geolocation

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type memoryStore struct {
	entries  []Entry
	used     int
	saved    int
	enriched chan map[string]*Location
}

func (m *memoryStore) LoadGeoCache(context.Context) ([]Entry, error) { return m.entries, nil }
func (m *memoryStore) SaveGeoCache(_ context.Context, e Entry) error {
	m.entries = append(m.entries, e)
	m.saved++
	return nil
}
func (m *memoryStore) ReserveGeoLookup(_ context.Context, _ time.Time, limit int) (bool, error) {
	if m.used >= limit {
		return false, nil
	}
	m.used++
	return true, nil
}
func (m *memoryStore) EnrichCallLocations(_ context.Context, _ string, p map[string]*Location) error {
	if m.enriched != nil {
		m.enriched <- p
	}
	return nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const answer = `{"success":true,"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View","latitude":37.386,"longitude":-122.084}`

func stub(s *Service, body string, code int, calls *int) {
	s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.URL.Scheme != "https" || r.URL.Host != "ipwho.is" {
			panic("unexpected endpoint")
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
}
func TestPersistentWarmCacheAndNegativeExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		store := &memoryStore{}
		s := New(store)
		calls := 0
		stub(s, answer, 200, &calls)
		if got := s.resolve(ctx, "8.8.8.8"); got == nil || got.Latitude != 37.4 {
			t.Fatalf("lookup: %+v", got)
		}
		for range 10 {
			s.resolve(ctx, "8.8.8.8")
		}
		if calls != 1 || store.used != 1 {
			t.Fatal("cache did not deduplicate")
		}
		restarted := New(store)
		if err := restarted.Warm(ctx); err != nil {
			t.Fatal(err)
		}
		if got := restarted.Lookup("8.8.8.8"); got == nil {
			t.Fatal("persistent cache lost")
		}
		time.Sleep(positiveTTL)
		if s.Lookup("8.8.8.8") != nil {
			t.Fatal("expired location reused")
		}
		stub(s, `{"success":false}`, 200, &calls)
		s.resolve(ctx, "8.8.8.8")
		s.resolve(ctx, "8.8.8.8")
		if calls != 2 {
			t.Fatal("negative cache missing")
		}
		time.Sleep(negativeTTL)
		s.resolve(ctx, "8.8.8.8")
		if calls != 3 {
			t.Fatal("negative entry not retried after expiry")
		}
	})
}
func TestBudgetPersistsAcrossRestartAndQueueIsBounded(t *testing.T) {
	store := &memoryStore{used: DailyLimit}
	s := New(store)
	calls := 0
	stub(s, answer, 200, &calls)
	if s.resolve(context.Background(), "8.8.8.8") != nil || calls != 0 {
		t.Fatal("budget exceeded")
	}
	restarted := New(store)
	stub(restarted, answer, 200, &calls)
	restarted.resolve(context.Background(), "8.8.8.8")
	if calls != 0 {
		t.Fatal("restart reset daily budget")
	}
	for range QueueSize + 10 {
		s.Offer(Job{CallID: "test", Addresses: []string{"8.8.8.8"}})
	}
	if len(s.queue) != QueueSize || s.Dropped() != 10 {
		t.Fatal("queue was not bounded")
	}
	for i := range CacheSize + 10 {
		s.remember(Entry{IP: strings.Repeat("x", i+1)})
	}
	if len(s.cache) > CacheSize {
		t.Fatal("unbounded memory cache")
	}
}
func TestRejectsUnusableOrMismatchedResponses(t *testing.T) {
	for _, body := range []string{
		strings.Replace(answer, `"8.8.8.8"`, `"1.1.1.1"`, 1),
		strings.Replace(answer, `37.386`, `100`, 1),
		`{"success":true,"ip":"8.8.8.8","country":"Example"}`,
		`{"success":false}`, strings.Repeat("x", (16<<10)+1),
	} {
		s := New(&memoryStore{})
		n := 0
		stub(s, body, 200, &n)
		if got, _ := s.fetch(context.Background(), "8.8.8.8"); got != nil {
			t.Fatal("accepted invalid response")
		}
	}
	s := New(&memoryStore{})
	n := 0
	stub(s, answer, 429, &n)
	if _, wait := s.fetch(context.Background(), "8.8.8.8"); wait != 24*time.Hour {
		t.Fatal("429 did not back off")
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "::1", "::ffff:127.0.0.1", "100.64.1.1", "198.51.100.1", "2001:db8::1", "invalid"} {
		if PublicIP(ip) {
			t.Errorf("non-public: %s", ip)
		}
	}
}
func TestWorkerDeduplicatesAndEnrichesWithoutIPInEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memoryStore{enriched: make(chan map[string]*Location, 2)}
		s := New(store)
		calls := 0
		stub(s, answer, 200, &calls)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go s.Run(ctx)
		for range 2 {
			s.Offer(Job{CallID: "test", Addresses: []string{"8.8.8.8", "8.8.8.8", "127.0.0.1"}})
		}
		synctest.Wait()
		if calls != 1 || len(store.enriched) != 2 {
			t.Fatalf("calls %d patches %d", calls, len(store.enriched))
		}
		if len(<-store.enriched) != 1 {
			t.Fatal("private or duplicate address was queried")
		}
	})
}
func TestGatewayCoordinatesValidate(t *testing.T) {
	for _, tc := range [][3]string{{"region", "NaN", "1"}, {"region", "91", "1"}, {"region", "1", "181"}, {"", "1", "2"}} {
		if _, err := ParseLocation(tc[0], tc[1], tc[2]); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if p, err := ParseLocation("Hong Kong", "22.3", "114.2"); err != nil || p == nil {
		t.Fatal("valid config refused")
	}
}
