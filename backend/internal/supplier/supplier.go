// Package supplier owns the platform's API supplier directory, separate from model manufacturers.
package supplier

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

var (
	ErrInvalid  = errors.New("invalid supplier")
	ErrConflict = errors.New("supplier endpoint already assigned")
	ErrNotFound = errors.New("supplier not found")
)

type Endpoint struct{ BaseURL, Label string }
type Supplier struct {
	ID, Name, Kind, Summary, Description, WebsiteURL, DocsURL, StatusURL, IconData, IconSource string
	Visible                                                                                    bool
	Endpoints                                                                                  []Endpoint
	UpdatedAt                                                                                  time.Time
}

// Listing contains only the channel metadata needed to build a public view.
// BaseURL is used internally for recognition and must never be serialized.
type Listing struct {
	ID, Name, Summary, Description, BaseURL, OwnerID, OwnerName string
	Advanced                                                    channel.Advanced
}

type Store interface {
	List(context.Context) ([]Supplier, error)
	Save(context.Context, string, Supplier, bool) (Supplier, error)
	Listings(context.Context) ([]Listing, error)
}
type Service struct {
	Store    Store
	Outbound channel.Outbound
}

func WebURL(raw string, optional bool) bool {
	if raw == "" {
		return optional
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(raw) <= 2048
}

func (s *Service) Normalize(value Supplier) (Supplier, error) {
	value.Name = strings.TrimSpace(value.Name)
	value.Summary = strings.TrimSpace(value.Summary)
	value.Description = strings.TrimSpace(value.Description)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > 64 || (value.Kind != "official" && value.Kind != "relay") ||
		utf8.RuneCountInString(value.Summary) > 160 || utf8.RuneCountInString(value.Description) > 8000 ||
		!WebURL(value.WebsiteURL, false) || !WebURL(value.DocsURL, true) || !WebURL(value.StatusURL, true) || !WebURL(value.IconSource, true) || len(value.Endpoints) > 32 {
		return value, ErrInvalid
	}
	seen := map[string]bool{}
	for i, endpoint := range value.Endpoints {
		base, err := s.Outbound.NormalizeBaseURL(endpoint.BaseURL)
		if err != nil || seen[base] || utf8.RuneCountInString(endpoint.Label) > 64 {
			return value, ErrInvalid
		}
		seen[base] = true
		value.Endpoints[i] = Endpoint{BaseURL: base, Label: strings.TrimSpace(endpoint.Label)}
	}
	if value.IconData != "" {
		data, err := NormalizeIcon(value.IconData)
		if err != nil {
			return value, ErrInvalid
		}
		value.IconData = data
	}
	return value, nil
}
func (s *Service) Save(ctx context.Context, actor string, value Supplier, create bool) (Supplier, error) {
	value, err := s.Normalize(value)
	if err != nil {
		return value, err
	}
	if create {
		value.ID, err = ids.NewUUID()
		if err != nil {
			return value, err
		}
	}
	return s.Store.Save(ctx, actor, value, create)
}

// Match uses exact canonical addresses; no suffix or guessed domain matching.
func Match(suppliers []Supplier, base string) *Supplier {
	for _, item := range suppliers {
		if !item.Visible {
			continue
		}
		for _, endpoint := range item.Endpoints {
			if endpoint.BaseURL == base {
				return &item
			}
		}
	}
	return nil
}
