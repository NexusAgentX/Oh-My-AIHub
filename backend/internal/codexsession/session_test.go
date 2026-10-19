package codexsession

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type memoryRepository struct {
	mu      sync.Mutex
	rows    map[Identity]Route
	lookups int
}

func (m *memoryRepository) LookupSession(_ context.Context, id Identity) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	if r, ok := m.rows[id]; ok {
		return r, nil
	}
	return Route{}, gatewaycontract.ErrNotFound
}
func (m *memoryRepository) ClaimSession(_ context.Context, id Identity, r Route) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.rows[id]; ok {
		return old, nil
	}
	m.rows[id] = r
	return r, nil
}
func testIdentity(n int) Identity {
	return Identity{"account", "key", fmt.Sprintf("10000000-0000-4000-8000-%012d", n)}
}
func testRoute(n int) Route {
	return Route{Binding: gatewaycontract.Binding{GroupID: "group", ChannelID: fmt.Sprint(n), CredentialVersion: "1"}, CredentialScope: "scope"}
}

func TestConcurrentFirstRequestsAndRestartKeepOneRoute(t *testing.T) {
	repo := &memoryRepository{rows: map[Identity]Route{}}
	s := New(repo)
	id := testIdentity(1)
	var wg sync.WaitGroup
	routes := make(chan Route, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r, err := s.Claim(context.Background(), id, testRoute(n))
			if err != nil {
				t.Error(err)
				return
			}
			routes <- r
		}(i)
	}
	wg.Wait()
	close(routes)
	winner := repo.rows[id]
	for r := range routes {
		if r != winner {
			t.Fatal("concurrent request escaped the winning route")
		}
	}
	for i := 0; i < 100; i++ {
		if _, err := s.Lookup(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if repo.lookups != 0 {
		t.Fatal("active session hit SQL", repo.lookups)
	}
	r, err := New(repo).Lookup(context.Background(), id)
	if err != nil || r != winner {
		t.Fatal("restart changed the route", err)
	}
	other := id
	other.KeyID = "another-key"
	if _, err = New(repo).Lookup(context.Background(), other); err != gatewaycontract.ErrNotFound {
		t.Fatal("another Key inherited the binding", err)
	}
}

func TestBoundedCacheEvictionNeverRebindsDurableSession(t *testing.T) {
	repo := &memoryRepository{rows: map[Identity]Route{}}
	s := New(repo)
	for i := 0; i <= cacheCapacity; i++ {
		if _, err := s.Claim(context.Background(), testIdentity(i), testRoute(i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.cache) != cacheCapacity || len(s.order) != cacheCapacity {
		t.Fatal("unbounded route cache")
	}
	r, err := s.Lookup(context.Background(), testIdentity(0))
	if err != nil || r != testRoute(0) || repo.lookups != 1 {
		t.Fatal("eviction lost durable state", r, err)
	}
}

func BenchmarkActiveSessionLookup(b *testing.B) {
	repo := &memoryRepository{rows: map[Identity]Route{}}
	s := New(repo)
	id := testIdentity(0)
	_, _ = s.Claim(context.Background(), id, testRoute(0))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Lookup(context.Background(), id); err != nil {
			b.Fatal(err)
		}
	}
}

type blockedRepository struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockedRepository) LookupSession(ctx context.Context, _ Identity) (Route, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
		return Route{}, gatewaycontract.ErrNotFound
	case <-ctx.Done():
		return Route{}, ctx.Err()
	}
}
func (r *blockedRepository) ClaimSession(context.Context, Identity, Route) (Route, error) {
	panic("unused")
}
func TestMetadataSaturationRejectsWithoutWaitingOrGrowingTheQueue(t *testing.T) {
	repo := &blockedRepository{started: make(chan struct{}, repositoryConcurrency), release: make(chan struct{})}
	s := New(repo)
	var wg sync.WaitGroup
	for i := 0; i < repositoryConcurrency; i++ {
		wg.Add(1)
		go func(n int) { defer wg.Done(); _, _ = s.Lookup(context.Background(), testIdentity(n)) }(i)
	}
	for i := 0; i < repositoryConcurrency; i++ {
		<-repo.started
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := s.Lookup(ctx, testIdentity(repositoryConcurrency+1))
	close(repo.release)
	wg.Wait()
	if err != ErrBusy {
		t.Fatal("saturated metadata work was queued", err)
	}
}
