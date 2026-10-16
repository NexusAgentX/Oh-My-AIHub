// Package geolocation enriches completed calls through ipwho.is. The gateway
// reads memory only; one bounded worker owns all external and database I/O.
package geolocation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const CacheSize = 4096
const QueueSize = 128
const DailyLimit = 900
const positiveTTL = 7 * 24 * time.Hour
const negativeTTL = 15 * time.Minute
const gatewayRefresh = 6 * time.Hour

type Location struct {
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
type Entry struct {
	IP       string
	Location *Location
	Expires  time.Time
}
type Job struct {
	CallID    string
	Addresses []string
}
type Store interface {
	LoadGeoCache(context.Context) ([]Entry, error)
	SaveGeoCache(context.Context, Entry) error
	ReserveGeoLookup(context.Context, time.Time, int) (bool, error)
	EnrichCallLocations(context.Context, string, map[string]*Location) error
}

type Service struct {
	store   Store
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	cache   map[string]Entry
	queue   chan Job
	gateway atomic.Pointer[Entry]
	dropped atomic.Uint64
	failed  atomic.Uint64
	// These fields are owned by the single worker.
	nextQuery    time.Time
	blockedUntil time.Time
}

func New(store Store) *Service {
	return &Service{store: store, client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now, cache: map[string]Entry{}, queue: make(chan Job, QueueSize)}
}
func (s *Service) Warm(ctx context.Context) error {
	if s == nil {
		return nil
	}
	entries, err := s.store.LoadGeoCache(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s.remember(entry)
	}
	return nil
}
func (s *Service) remember(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= CacheSize {
		for key := range s.cache {
			delete(s.cache, key)
			break
		}
	}
	s.cache[e.IP] = e
}
func (s *Service) cached(ip string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[ip]
	return e, ok && s.now().Before(e.Expires)
}
func (s *Service) Lookup(ip string) *Location {
	if s == nil {
		return nil
	}
	e, ok := s.cached(ip)
	if !ok {
		return nil
	}
	return e.Location
}
func PublicIP(value string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Zone() != "" {
		return false
	}
	// Documentation, shared address space, benchmark, and reserved ranges are not
	// useful positions and must not be sent to an external service.
	for _, prefix := range nonPublic {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
}

func (s *Service) Offer(job Job) {
	if s == nil || len(job.Addresses) == 0 {
		return
	}
	select {
	case s.queue <- job:
	default:
		s.dropped.Add(1)
	}
}
func (s *Service) Dropped() uint64 { return s.dropped.Load() }
func (s *Service) Failed() uint64  { return s.failed.Load() }
func (s *Service) Run(ctx context.Context) {
	// Discovery shares this worker with individual IP lookups: it cannot run an
	// unbudgeted second HTTP stream or delay the forwarding goroutine.
	refresh := time.NewTimer(s.discoverGateway(ctx))
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			refresh.Reset(s.discoverGateway(ctx))
		case job := <-s.queue:
			positions := map[string]*Location{}
			for _, ip := range job.Addresses {
				if _, seen := positions[ip]; seen || !PublicIP(ip) {
					continue
				}
				positions[ip] = s.resolve(ctx, ip)
			}
			known := map[string]*Location{}
			for ip, pos := range positions {
				if pos != nil {
					known[ip] = pos
				}
			}
			if len(known) > 0 {
				bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
				if err := s.store.EnrichCallLocations(bounded, job.CallID, known); err != nil {
					s.failed.Add(1)
				}
				cancel()
			}
		}
	}
}
func (s *Service) resolve(ctx context.Context, ip string) *Location {
	if e, ok := s.cached(ip); ok {
		return e.Location
	}
	if !s.reserve(ctx) {
		return nil
	}
	location, retryAfter := s.fetch(ctx, ip)
	ttl := positiveTTL
	if location == nil {
		ttl = negativeTTL
		s.failed.Add(1)
	}
	if retryAfter > 0 {
		s.blockedUntil = s.now().Add(retryAfter)
	}
	entry := Entry{ip, location, s.now().Add(ttl)}
	s.remember(entry)
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	if err := s.store.SaveGeoCache(bounded, entry); err != nil {
		s.failed.Add(1)
	}
	cancel()
	return location
}
func (s *Service) fetch(ctx context.Context, ip string) (*Location, time.Duration) {
	_, location, retryAfter := s.query(ctx, ip)
	return location, retryAfter
}
func (s *Service) query(ctx context.Context, ip string) (string, *Location, time.Duration) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/"+ip+"?fields=success,ip,country,region,city,latitude,longitude", nil)
	if err != nil {
		return "", nil, 0
	}
	response, err := s.client.Do(request)
	if err != nil {
		return "", nil, 0
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		wait := 24 * time.Hour
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			wait = min(time.Duration(min(seconds, 86400))*time.Second, 24*time.Hour)
		}
		return "", nil, wait
	}
	if response.StatusCode != http.StatusOK {
		return "", nil, 0
	}
	var result struct {
		Success   bool     `json:"success"`
		IP        string   `json:"ip"`
		Country   string   `json:"country"`
		Region    string   `json:"region"`
		City      string   `json:"city"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &result) != nil {
		return "", nil, 0
	}
	got, err := netip.ParseAddr(result.IP)
	expected, _ := netip.ParseAddr(ip)
	if err != nil || !PublicIP(got.Unmap().String()) || (ip != "" && got.Unmap() != expected.Unmap()) || !result.Success {
		return "", nil, 0
	}
	address := got.Unmap().String()
	if result.Latitude == nil || result.Longitude == nil || !validCoordinates(*result.Latitude, *result.Longitude) {
		return address, nil, 0
	}

	parts := []string{}
	for _, part := range []string{result.Country, result.Region, result.City} {
		if len(part) > 160 || strings.ContainsAny(part, "\r\n\x00") {
			return "", nil, 0
		}
		if part != "" && (len(parts) == 0 || parts[len(parts)-1] != part) {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return address, nil, 0
	}
	return address, &Location{strings.Join(parts, " · "), math.Round(*result.Latitude*10) / 10, math.Round(*result.Longitude*10) / 10}, 0
}
func validCoordinates(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}
func ParseLocation(label, latitude, longitude string) (*Location, error) {
	if label == "" && latitude == "" && longitude == "" {
		return nil, nil
	}
	lat, errLat := strconv.ParseFloat(latitude, 64)
	lon, errLon := strconv.ParseFloat(longitude, 64)
	if strings.TrimSpace(label) == "" || len(label) > 160 || errLat != nil || errLon != nil || !validCoordinates(lat, lon) {
		return nil, errors.New("gateway location requires label and valid latitude/longitude")
	}
	return &Location{label, lat, lon}, nil
}

// reserve is worker-owned and shared by self-discovery and explicit lookups.
func (s *Service) reserve(ctx context.Context) bool {
	if ctx.Err() != nil || s.now().Before(s.blockedUntil) {
		return false
	}
	if wait := s.nextQuery.Sub(s.now()); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	allowed, err := s.store.ReserveGeoLookup(bounded, s.now().UTC().Truncate(24*time.Hour), DailyLimit)
	cancel()
	if err != nil {
		s.failed.Add(1)
		return false
	}
	if !allowed {
		s.blockedUntil = s.now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return false
	}
	s.nextQuery = s.now().Add(2 * time.Second)
	return true
}

// Gateway is an immutable, process-local view of this instance's discovered
// egress. Never warm it from a previous process: it could have run on a different
// host. No request performs discovery or waits for the worker.
func (s *Service) Gateway() Entry {
	if s == nil {
		return Entry{}
	}
	entry := s.gateway.Load()
	if entry == nil || !s.now().Before(entry.Expires) {
		return Entry{}
	}
	return *entry
}

func (s *Service) discoverGateway(ctx context.Context) time.Duration {
	if !s.reserve(ctx) {
		return negativeTTL
	}
	ip, location, retryAfter := s.query(ctx, "")
	delay := gatewayRefresh
	if ip == "" || location == nil {
		delay = negativeTTL
		s.failed.Add(1)
	}
	if retryAfter > 0 {
		s.blockedUntil = s.now().Add(retryAfter)
	}
	if ip != "" {
		snapshot := &Entry{IP: ip, Location: location, Expires: s.now().Add(delay)}
		s.gateway.Store(snapshot)
		// Also reuse this address for client/upstream queries; never store a
		// portable "self IP" record that would misidentify a relocated instance.
		entry := Entry{IP: ip, Location: location, Expires: s.now().Add(positiveTTL)}
		if location == nil {
			entry.Expires = s.now().Add(negativeTTL)
		}
		s.remember(entry)
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		if err := s.store.SaveGeoCache(bounded, entry); err != nil {
			s.failed.Add(1)
		}
		cancel()
	}
	return delay
}
