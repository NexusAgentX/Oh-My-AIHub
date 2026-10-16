package gatewaypg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/geolocation"
	"github.com/jackc/pgx/v5"
)

func (s *Store) LoadGeoCache(ctx context.Context) ([]geolocation.Entry, error) {
	if err := s.q.TrimGeoUsage(ctx); err != nil {
		return nil, err
	}
	rows, err := s.q.LoadGeoCache(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]geolocation.Entry, 0, len(rows))
	for _, row := range rows {
		var location *geolocation.Location
		if len(row.Location) > 0 && json.Unmarshal(row.Location, &location) != nil {
			continue
		}
		entries = append(entries, geolocation.Entry{IP: row.Ip, Location: location, Expires: row.ExpiresAt})
	}
	return entries, nil
}
func (s *Store) SaveGeoCache(ctx context.Context, e geolocation.Entry) error {
	raw, err := json.Marshal(e.Location)
	if err != nil {
		return err
	}
	if err = s.q.SaveGeoCache(ctx, SaveGeoCacheParams{Ip: e.IP, Location: raw, ExpiresAt: e.Expires}); err != nil {
		return err
	}
	return s.q.TrimGeoCache(ctx)
}
func (s *Store) ReserveGeoLookup(ctx context.Context, day time.Time, limit int) (bool, error) {
	_, err := s.q.ReserveGeoLookup(ctx, ReserveGeoLookupParams{Day: day, DailyLimit: int32(limit)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
func (s *Store) EnrichCallLocations(ctx context.Context, id string, locations map[string]*geolocation.Location) error {
	raw, err := json.Marshal(locations)
	if err != nil {
		return err
	}
	return s.q.EnrichCallLocations(ctx, EnrichCallLocationsParams{ID: id, Locations: raw})
}
