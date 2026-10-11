package snapshot

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// wrapped describes one store the cache wraps: its interface, what its writes
// invalidate, and the methods that do not write anything the snapshots hold.
type wrapped struct {
	name   string
	iface  reflect.Type
	writes map[string]Domain
	others []string
	// build wraps a stub whose writes succeed (fail nil) or return fail.
	build func(c *Cache, fail error) any
}

func wrappedStores() []wrapped {
	return []wrapped{
		{"apikey.Store", reflect.TypeFor[apikey.Store](), keyStoreWrites, keyStoreOthers,
			func(c *Cache, fail error) any { return c.Keys(stubKeys{fail: fail, created: true}) }},
		{"channel.Store", reflect.TypeFor[channel.Store](), channelStoreWrites, channelStoreOthers,
			func(c *Cache, fail error) any { return c.Channels(stubChannels{fail: fail}) }},
		{"catalog.Store", reflect.TypeFor[catalog.Store](), catalogStoreWrites, catalogStoreOthers,
			func(c *Cache, fail error) any { return c.Catalog(stubCatalog{fail: fail}) }},
		{"identity.Store", reflect.TypeFor[identity.Store](), identityStoreWrites, identityStoreOthers,
			func(c *Cache, fail error) any { return c.Identity(stubIdentity{fail: fail}) }},
		{"routing.Store", reflect.TypeFor[routing.Store](), routingStoreWrites, routingStoreOthers,
			func(c *Cache, fail error) any { return c.Routing(stubRouting{fail: fail}) }},
	}
}

func interfaceMethods(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for index := range t.NumMethod() {
		names = append(names, t.Method(index).Name)
	}
	sort.Strings(names)
	return names
}

func writeNames(writes map[string]Domain) []string {
	names := make([]string, 0, len(writes))
	for name := range writes {
		names = append(names, name)
	}
	return names
}

// A method added to one of the wrapped interfaces must be decided on: does it
// change what the snapshots hold? If it is in neither list this test fails, so
// a new write path cannot go unnoticed.
func TestEveryStoreMethodIsClassified(t *testing.T) {
	check := func(name string, iface reflect.Type, groups ...[]string) {
		t.Helper()
		var classified []string
		for _, group := range groups {
			classified = append(classified, group...)
		}
		sort.Strings(classified)
		for index := 1; index < len(classified); index++ {
			if classified[index] == classified[index-1] {
				t.Errorf("%s: %s is classified twice", name, classified[index])
			}
		}
		methods := interfaceMethods(iface)
		for _, method := range methods {
			if !slices.Contains(classified, method) {
				t.Errorf("%s.%s is not classified: say whether it changes what the snapshots hold (see wrap.go) and hook it if it does", name, method)
			}
		}
		for _, method := range classified {
			if !slices.Contains(methods, method) {
				t.Errorf("%s lists %s, which the interface no longer has", name, method)
			}
		}
	}
	for _, store := range wrappedStores() {
		check(store.name, store.iface, writeNames(store.writes), store.others)
	}
	check("gateway.Store", reflect.TypeFor[gateway.Store](), gatewayStoreSnapshot, gatewayStoreOthers)
}

// generations are the change counters of the four snapshots.
func generations(c *Cache) [4]uint64 {
	return [4]uint64{c.keys.want.Load(), c.models.want.Load(), c.fleet.want.Load(), c.routes.want.Load()}
}

// staleDomains are the snapshots that were counted a change or never loaded.
func staleDomains(c *Cache) Domain {
	var domains Domain
	for flag, cell := range map[Domain]interface{ stale() bool }{DomainKeys: c.keys, DomainModels: c.models, DomainFleet: c.fleet, DomainRoutes: c.routes} {
		if cell.stale() {
			domains |= flag
		}
	}
	return domains
}

func changed(before, after [4]uint64) Domain {
	var domains Domain
	for index, flag := range []Domain{DomainKeys, DomainModels, DomainFleet, DomainRoutes} {
		if after[index] > before[index] {
			domains |= flag
		}
	}
	return domains
}

// call invokes a method of the wrapper with zero values.
func call(t *testing.T, store any, method string) []reflect.Value {
	t.Helper()
	target := reflect.ValueOf(store).MethodByName(method)
	if !target.IsValid() {
		t.Fatalf("%T has no method %s", store, method)
	}
	var args []reflect.Value
	for index := range target.Type().NumIn() {
		in := target.Type().In(index)
		if in == reflect.TypeFor[context.Context]() {
			args = append(args, reflect.ValueOf(context.Background()))
		} else {
			args = append(args, reflect.Zero(in))
		}
	}
	return target.Call(args)
}

// A successful write invalidates exactly the snapshots it declares, a failed
// one invalidates nothing, and a method that is not a write never does.
func TestWritesInvalidateWhatTheyDeclare(t *testing.T) {
	failure := errors.New("rolled back")
	for _, store := range wrappedStores() {
		for method, domains := range store.writes {
			t.Run(store.name+"."+method, func(t *testing.T) {
				cache := newTestCache(&fakeLoader{})
				before := generations(cache)
				call(t, store.build(cache, nil), method)
				if got := changed(before, generations(cache)); got != domains {
					t.Errorf("a successful %s invalidated %04b, want %04b", method, got, domains)
				}
				// Whatever it invalidated is rebuilt before it returned.
				if stale := staleDomains(cache) & domains; stale != 0 {
					t.Errorf("snapshots %04b are stale after %s returned", stale, method)
				}
				failed := newTestCache(&fakeLoader{})
				before = generations(failed)
				call(t, store.build(failed, failure), method)
				got := changed(before, generations(failed))
				if store.name == "routing.Store" && method == "Set" {
					// Set can fail after its transaction committed, so it
					// rebuilds whatever happens.
					if got != domains {
						t.Errorf("a failed Set invalidated %04b, want %04b", got, domains)
					}
				} else if got != 0 {
					t.Errorf("a failed %s invalidated %04b", method, got)
				}
			})
		}
	}
}

// EnsureDefault only changes anything when it created the key.
func TestEnsureDefaultInvalidatesOnlyWhenItCreatedTheKey(t *testing.T) {
	for _, created := range []bool{true, false} {
		cache := newTestCache(&fakeLoader{})
		before := generations(cache)
		if _, err := cache.Keys(stubKeys{created: created}).EnsureDefault(context.Background(), apikey.Sealed{}); err != nil {
			t.Fatal(err)
		}
		got := changed(before, generations(cache))
		if want := map[bool]Domain{true: DomainKeys, false: 0}[created]; got != want {
			t.Errorf("created=%v invalidated %04b, want %04b", created, got, want)
		}
	}
}

// A write to the channel store is either an edit of an existing channel, which
// makes the gateway forget the channel's 4xx answers, or the creation of a new
// one; a method added to the store has to be decided on, like in the test above.
func TestEveryChannelWriteIsDecidedOnForTheAnomalyStats(t *testing.T) {
	decided := append(slices.Clone(channelStoreEdits), channelStoreNotEdits...)
	sort.Strings(decided)
	writes := writeNames(channelStoreWrites)
	sort.Strings(writes)
	if !slices.Equal(decided, writes) {
		t.Fatalf("channelStoreEdits and channelStoreNotEdits together are %v, the channel store's writes are %v", decided, writes)
	}
}

// Editing a channel calls the hook with its ID once the write has succeeded and
// the snapshots are rebuilt; creating one, failing, a probe that only records
// its results, and reads never do.
func TestChannelEditsReachTheEditHook(t *testing.T) {
	ctx := context.Background()
	newHooked := func(fail error) (channel.Store, *Cache, *[]string) {
		cache := newTestCache(&fakeLoader{})
		var edited []string
		cache.OnChannelEdited(func(id string) {
			if stale := staleDomains(cache) & DomainFleet; stale != 0 {
				t.Errorf("the hook ran for %s before the fleet was rebuilt", id)
			}
			edited = append(edited, id)
		})
		return cache.Channels(stubChannels{fail: fail}), cache, &edited
	}

	store, _, edited := newHooked(nil)
	if _, err := store.Update(ctx, "u", channel.Update{}); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveTests(ctx, "s", nil, time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Suspend(ctx, "admin", "p", "reason"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Unsuspend(ctx, "admin", "q", "reason"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"u", "d", "s", "p", "q"}; !slices.Equal(*edited, want) {
		t.Fatalf("edited channels = %v, want %v", *edited, want)
	}

	// Not edits.
	store, _, edited = newHooked(nil)
	if _, err := store.Create(ctx, channel.NewChannel{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveTests(ctx, "s", nil, time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	if len(*edited) != 0 {
		t.Fatalf("hook ran for %v", *edited)
	}

	// A write that failed changed nothing.
	failing := errors.New("rolled back")
	store, _, edited = newHooked(failing)
	_, _ = store.Update(ctx, "u", channel.Update{})
	_ = store.SoftDelete(ctx, "d")
	_, _ = store.SaveTests(ctx, "s", nil, time.Time{}, true)
	_, _ = store.Suspend(ctx, "admin", "p", "reason")
	_, _ = store.Unsuspend(ctx, "admin", "q", "reason")
	if len(*edited) != 0 {
		t.Fatalf("hook ran for %v after failed writes", *edited)
	}

	// No hook registered is fine, and a second registration replaces the first.
	bare := newTestCache(&fakeLoader{})
	if _, err := bare.Channels(stubChannels{}).Update(ctx, "u", channel.Update{}); err != nil {
		t.Fatal(err)
	}
	var first, second int
	bare.OnChannelEdited(func(string) { first++ })
	bare.OnChannelEdited(func(string) { second++ })
	if _, err := bare.Channels(stubChannels{}).Update(ctx, "u", channel.Update{}); err != nil {
		t.Fatal(err)
	}
	if first != 0 || second != 1 {
		t.Fatalf("hooks called %d and %d times, want 0 and 1", first, second)
	}
}

// The stubs implement only the writes: a wrapper passes every other method
// straight through to the store it wraps, so it cannot invalidate on them.
type stubKeys struct {
	apikey.Store
	fail    error
	created bool
}

func (s stubKeys) Create(context.Context, apikey.Sealed) (apikey.Key, error) {
	return apikey.Key{}, s.fail
}
func (s stubKeys) EnsureDefault(context.Context, apikey.Sealed) (bool, error) {
	return s.created, s.fail
}
func (s stubKeys) Update(context.Context, string, string, apikey.Update) (apikey.Key, error) {
	return apikey.Key{}, s.fail
}
func (s stubKeys) Delete(context.Context, string, string) error { return s.fail }

type stubChannels struct {
	channel.Store
	fail error
}

func (s stubChannels) Create(context.Context, channel.NewChannel) (channel.Channel, error) {
	return channel.Channel{}, s.fail
}
func (s stubChannels) Update(context.Context, string, channel.Update) (channel.Channel, error) {
	return channel.Channel{}, s.fail
}
func (s stubChannels) SoftDelete(context.Context, string) error { return s.fail }
func (s stubChannels) SaveTests(context.Context, string, []channel.TestOutcome, time.Time, bool) (channel.Channel, error) {
	return channel.Channel{}, s.fail
}
func (s stubChannels) Suspend(context.Context, string, string, string) (channel.Channel, error) {
	return channel.Channel{}, s.fail
}
func (s stubChannels) Unsuspend(context.Context, string, string, string) (channel.Channel, error) {
	return channel.Channel{}, s.fail
}

type stubCatalog struct {
	catalog.Store
	fail error
}

func (s stubCatalog) CreateModel(context.Context, string, catalog.Model) (catalog.Model, error) {
	return catalog.Model{}, s.fail
}
func (s stubCatalog) UpdateModel(context.Context, string, string, func(catalog.Model) (catalog.Model, error)) (catalog.Model, error) {
	return catalog.Model{}, s.fail
}
func (s stubCatalog) DeleteModel(context.Context, string, string) error { return s.fail }

type stubIdentity struct {
	identity.Store
	fail error
}

func (s stubIdentity) UpdateAccount(context.Context, string, string, identity.AccountUpdate) (identity.AdminAccount, error) {
	return identity.AdminAccount{}, s.fail
}

type stubRouting struct {
	routing.Store
	fail error
}

func (s stubRouting) Set(context.Context, string, string, routing.Pref) (routing.Pref, error) {
	return routing.Pref{}, s.fail
}
func (s stubRouting) Delete(context.Context, string, string, string) error { return s.fail }
