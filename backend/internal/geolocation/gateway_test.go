package geolocation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestGatewayDiscoveryRefreshesWithoutMovingCapturedSnapshots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memoryStore{}
		s := New(store)
		calls := 0
		s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/" {
				t.Errorf("discovery supplied an IP: %s", r.URL.Path)
			}
			calls++
			body := answer
			if calls > 1 {
				body = strings.ReplaceAll(answer, "8.8.8.8", "1.1.1.1")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		synctest.Wait()
		first := s.Gateway()
		if first.IP != "8.8.8.8" || first.Location == nil || store.used != 1 {
			t.Fatalf("first discovery: %+v, budget=%d", first, store.used)
		}
		time.Sleep(gatewayRefresh)
		synctest.Wait()
		if s.Gateway().IP != "1.1.1.1" || calls != 2 || store.used != 2 {
			t.Fatal("periodic discovery did not share quota")
		}
		if first.IP != "8.8.8.8" || first.Location.Label == "" {
			t.Fatal("refresh mutated a captured call")
		}
		restarted := New(store)
		if err := restarted.Warm(ctx); err != nil {
			t.Fatal(err)
		}
		if restarted.Gateway().IP != "" {
			t.Fatal("another process inherited an old self IP")
		}
		cancel()
		<-done
	})
}

func TestDiscoverySharesRateLimitWithExplicitQueries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memoryStore{}
		s := New(store)
		var times []time.Time
		s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
			times = append(times, time.Now())
			body := answer
			if r.URL.Path != "/" {
				body = strings.ReplaceAll(answer, "8.8.8.8", "1.1.1.1")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})
		s.discoverGateway(context.Background())
		s.resolve(context.Background(), "1.1.1.1")
		if len(times) != 2 || times[1].Sub(times[0]) < 2*time.Second || store.used != 2 {
			t.Fatal("self-discovery bypassed shared throttle")
		}
	})
}

func TestGatewayDiscoveryFailuresExpireAndBackOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memoryStore{}
		s := New(store)
		calls := 0
		stub(s, answer, 200, &calls)
		s.discoverGateway(context.Background())
		captured := s.Gateway()
		time.Sleep(gatewayRefresh)
		stub(s, answer, 429, &calls)
		if delay := s.discoverGateway(context.Background()); delay != negativeTTL || s.Gateway().IP != "" {
			t.Fatal("failed refresh retained expired self IP")
		}
		before := calls
		s.discoverGateway(context.Background())
		s.resolve(context.Background(), "1.1.1.1")
		if calls != before || store.used != 2 || s.blockedUntil.IsZero() {
			t.Fatal("429 backoff was not shared")
		}
		if captured.IP != "8.8.8.8" {
			t.Fatal("past snapshot changed")
		}
		exhausted := New(&memoryStore{used: DailyLimit})
		stub(exhausted, answer, 200, &calls)
		exhausted.discoverGateway(context.Background())
		if calls != before || exhausted.Gateway().IP != "" {
			t.Fatal("discovery bypassed the daily quota")
		}
	})
}

func TestBlockedDiscoveryDoesNotBlockRequestSnapshotReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New(&memoryStore{})
		started := make(chan struct{})
		s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, errors.New("cancelled")
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		<-started
		if got := s.Gateway(); got.IP != "" {
			t.Fatal("unknown discovery fabricated a node")
		}
		if n := testing.AllocsPerRun(100, func() { s.Gateway() }); n != 0 {
			t.Fatalf("snapshot read allocated %v", n)
		}
		s.Offer(Job{CallID: "test", Addresses: []string{"1.1.1.1"}})
		if len(s.queue) != 1 {
			t.Fatal("request producer waited for discovery")
		}
		cancel()
		<-done
	})
}

func TestGatewayDiscoveryRejectsPrivateOrInvalidSelfAddresses(t *testing.T) {
	for _, body := range []string{strings.ReplaceAll(answer, "8.8.8.8", "10.0.0.1"), strings.ReplaceAll(answer, "8.8.8.8", "invalid"), `{"success":false}`} {
		s := New(&memoryStore{})
		calls := 0
		stub(s, body, 200, &calls)
		if delay := s.discoverGateway(context.Background()); delay != negativeTTL || s.Gateway().IP != "" {
			t.Fatal("invalid self address accepted")
		}
	}
	s := New(&memoryStore{})
	calls := 0
	stub(s, `{"success":true,"ip":"8.8.8.8"}`, 200, &calls)
	s.discoverGateway(context.Background())
	if got := s.Gateway(); got.IP != "8.8.8.8" || got.Location != nil {
		t.Fatalf("valid IP without location lost: %+v", got)
	}
}

func TestGatewaySnapshotReadAllocatesNothing(t *testing.T) {
	s := New(nil)
	s.gateway.Store(&Entry{IP: "8.8.8.8", Location: &Location{Label: "Example"}, Expires: time.Now().Add(time.Hour)})
	if n := testing.AllocsPerRun(100, func() {
		if s.Gateway().IP != "8.8.8.8" {
			panic("snapshot missing")
		}
	}); n != 0 {
		t.Fatalf("snapshot read allocated %v", n)
	}
	var disabled *Service
	if disabled.Gateway().IP != "" {
		t.Fatal("disabled lookup produced a snapshot")
	}
}

func BenchmarkGatewaySnapshotRead(b *testing.B) {
	s := New(nil)
	s.gateway.Store(&Entry{IP: "8.8.8.8", Location: &Location{Label: "Example"}, Expires: time.Now().Add(time.Hour)})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Gateway()
		}
	})
}
