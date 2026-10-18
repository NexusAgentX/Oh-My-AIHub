// Package groupchannel owns channel choices over approved supplier groups.
// It never writes a global model or copies group defaults/prices into choices.
package groupchannel

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

var (
	ErrInvalid     = errors.New("invalid group channel configuration")
	ErrNotFound    = errors.New("group channel not found")
	ErrConflict    = errors.New("group channel configuration changed")
	ErrUnavailable = errors.New("group channel snapshot unavailable")
	ErrSuspended   = errors.New("group channel suspended by administrator")
)

type Actor struct {
	ID    string
	Admin bool
}
type Record struct {
	ID                                       string
	Owner                                    channel.Owner
	OwnerActive                              bool
	Name, Summary, Description               string
	BaseURL                                  string // transitional storage only; runtime uses selected group addresses
	Status                                   channel.Status
	SuspendedReason                          *string
	GroupID                                  string
	Revision, CredentialRevision, ProbeEpoch int64
	AddressIDs                               []string
	Overrides                                market.Defaults
	Credential                               channel.EncryptedCredential
	CreatedAt, UpdatedAt                     time.Time
}
type Choice struct {
	ChannelID    string
	GroupModelID string
	Excluded     bool
	DiscountNano int64
}
type Probe struct {
	ChannelID    string
	GroupModelID string
	Binding      string
	Tests        map[channel.Format]channel.FormatTest
	TestedAt     time.Time
}
type Data struct {
	Channels []Record
	Choices  []Choice
	Probes   []Probe
	Reports  []detection.Job
}
type CreateInput struct {
	GroupID                    string
	APIKey                     string
	Name, Summary, Description string
	AddressIDs                 []string // nil selects the first current address; [] is invalid
	Overrides                  market.Defaults
	Models                     []Choice
}
type UpdateInput struct {
	Revision                   int64
	GroupID                    *string
	APIKey                     *string
	Name, Summary, Description *string
	Status                     *channel.Status
	AddressIDs                 *[]string
	Overrides                  *market.Defaults
	Models                     *[]Choice // nil preserves choices; [] restores defaults, not probe data
}
type Mutation struct {
	Record            Record
	ExpectedRevision  int64
	ReplaceChoices    bool
	ClearChoices      bool
	Choices           []Choice
	ReplaceCredential bool
}
type Repository interface {
	Events(context.Context, string) ([]channel.Event, error)
	Get(context.Context, string) (Record, error)
	List(context.Context, string, bool) ([]Record, error)
	Create(context.Context, Record, []Choice) error
	Update(context.Context, Mutation) error
	Delete(context.Context, string, int64) error
	Moderate(context.Context, string, string, string) error
	Snapshot(context.Context) (Data, error)
	SaveProbe(context.Context, Record, market.Group, market.GroupModel, Probe) (bool, error)
}

// View is owner/admin-only management data. Credential bytes and scope tags
// are deliberately absent. Public catalog/hall responses use separate DTOs.
type View struct {
	ID                 string            `json:"id"`
	Owner              OwnerView         `json:"owner"`
	Name               string            `json:"name"`
	Summary            string            `json:"summary"`
	Description        string            `json:"description"`
	Status             channel.Status    `json:"status"`
	SuspendedReason    *string           `json:"suspended_reason"`
	GroupID            string            `json:"group_id"`
	GroupName          string            `json:"group_name"`
	SupplierName       string            `json:"supplier_name"`
	Revision           int64             `json:"revision"`
	GroupRevision      int64             `json:"group_revision"`
	Addresses          []AddressView     `json:"addresses"`
	SelectedAddressIDs []string          `json:"selected_address_ids"`
	Overrides          market.Defaults   `json:"overrides"`
	Effective          market.Defaults   `json:"effective"`
	Sources            map[string]string `json:"sources"`
	Models             []ModelView       `json:"models"`
	Available          bool              `json:"available"`
	Reason             string            `json:"reason"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}
type OwnerView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}
type AddressView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}
type ModelView struct {
	GroupModelID string                                `json:"group_model_id"`
	ModelID      string                                `json:"model_id"`
	DisplayName  string                                `json:"display_name"`
	ModelType    string                                `json:"model_type"`
	Excluded     bool                                  `json:"excluded"`
	Discount     string                                `json:"discount"`
	ProbeState   string                                `json:"probe_state"`
	Formats      []channel.Format                      `json:"formats"`
	FormatTests  map[channel.Format]channel.FormatTest `json:"format_tests"`
	QuoteID      *string                               `json:"quote_id"`
	Enabled      bool                                  `json:"enabled"`
}
