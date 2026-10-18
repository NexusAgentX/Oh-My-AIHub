package marketpg

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool} }

var _ market.Store = (*Store)(nil)

func domainError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return market.ErrConflict
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.Code == "23514" && strings.Contains(pe.Message, "supplier still has groups") {
			return market.ErrHasGroups
		}
		switch pe.Code {
		case "23505", "23503", "40001", "40P01":
			return market.ErrConflict
		case "23514", "22P02", "22001", "23502":
			return market.ErrInvalid
		}
	}
	return err
}
func allocate(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return ids.NewUUID()
}

func (s *Store) Apply(ctx context.Context, actor string, changes market.Changes) error {
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error { return Apply(ctx, tx, actor, changes) }))
}

// Apply composes into the contribution approval transaction. It is not an HTTP
// approval bypass; the caller checks current actor/report/draft permission first.
func Apply(ctx context.Context, tx pgx.Tx, actor string, c market.Changes) error {
	if actor == "" || len(c.Suppliers)+len(c.Groups)+len(c.Addresses)+len(c.Models)+len(c.Quotes)+len(c.GroupAddresses) > 1024 {
		return market.ErrInvalid
	}
	q := New(tx)
	changedGroups := map[string]bool{}
	audit := func(kind, id string, revision int64) error {
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "market." + kind + ".published", TargetType: kind, TargetID: id, Detail: map[string]any{"revision": revision}})
	}
	for _, change := range c.Suppliers {
		v := change.Value
		if change.BaseRevision < 0 || market.ValidateSupplier(v) != nil {
			return market.ErrInvalid
		}
		var err error
		v.ID, err = allocate(v.ID)
		if err != nil {
			return err
		}
		v.Revision = change.BaseRevision + 1
		p := InsertSupplierParams{ID: v.ID, ContributorID: actor, Name: v.Name, Kind: v.Kind, WebsiteUrl: v.WebsiteURL, IconData: v.IconData, IconSource: v.IconSource, Summary: v.Summary, Description: v.Description, DocsUrl: v.DocsURL, Enabled: v.Enabled, Revision: v.Revision, DeletedAt: v.DeletedAt}
		if change.BaseRevision == 0 {
			_, err = q.InsertSupplier(ctx, p)
		} else {
			_, err = q.UpdateSupplier(ctx, UpdateSupplierParams{ID: v.ID, Name: v.Name, Kind: v.Kind, WebsiteUrl: v.WebsiteURL, IconData: v.IconData, IconSource: v.IconSource, Summary: v.Summary, Description: v.Description, DocsUrl: v.DocsURL, Enabled: v.Enabled, Revision: v.Revision, DeletedAt: v.DeletedAt, BaseRevision: change.BaseRevision})
		}
		if err != nil {
			return domainError(err)
		}
		if err = audit("supplier", v.ID, v.Revision); err != nil {
			return err
		}
	}
	for _, change := range c.Groups {
		v := change.Value
		if change.BaseRevision < 0 || market.ValidateGroup(v) != nil {
			return market.ErrInvalid
		}
		var err error
		v.ID, err = allocate(v.ID)
		if err != nil {
			return err
		}
		v.Revision = change.BaseRevision + 1
		v.CapabilityDigest = market.GroupCapabilityDigest(v)
		if change.BaseRevision == 0 {
			v.ConnectionVersion = 1
		} else {
			prior, err := q.GetGroup(ctx, v.ID)
			if err != nil {
				return domainError(err)
			}
			v.ConnectionVersion = prior.ConnectionVersion + 1
		}
		var headers []byte
		if v.Defaults.HeaderRules != nil {
			headers, err = json.Marshal(v.Defaults.HeaderRules)
			if err != nil {
				return err
			}
		}
		d := v.Defaults
		p := InsertGroupParams{ID: v.ID, SupplierID: v.SupplierID, Name: v.Name, Description: v.Description, StatusUrl: v.StatusURL, UserAgent: d.UserAgent, HeaderRules: headers, ConcurrencyLimit: d.ConcurrencyLimit, RpmLimit: d.RPMLimit, TtftTimeoutMs: d.TTFTTimeoutMS, RequestTimeoutMs: d.RequestTimeoutMS, IdleTimeoutMs: d.IdleTimeoutMS, CooldownFailures: d.CooldownFailures, CooldownSeconds: d.CooldownSeconds, Enabled: v.Enabled, Revision: v.Revision, ConnectionVersion: v.ConnectionVersion, CapabilityDigest: v.CapabilityDigest, DeletedAt: v.DeletedAt}
		if change.BaseRevision == 0 {
			_, err = q.InsertGroup(ctx, p)
		} else {
			_, err = q.UpdateGroup(ctx, UpdateGroupParams{ID: p.ID, SupplierID: p.SupplierID, Name: p.Name, Description: p.Description, StatusUrl: p.StatusUrl, UserAgent: p.UserAgent, HeaderRules: p.HeaderRules, ConcurrencyLimit: p.ConcurrencyLimit, RpmLimit: p.RpmLimit, TtftTimeoutMs: p.TtftTimeoutMs, RequestTimeoutMs: p.RequestTimeoutMs, IdleTimeoutMs: p.IdleTimeoutMs, CooldownFailures: p.CooldownFailures, CooldownSeconds: p.CooldownSeconds, Enabled: p.Enabled, Revision: p.Revision, ConnectionVersion: p.ConnectionVersion, CapabilityDigest: p.CapabilityDigest, DeletedAt: p.DeletedAt, BaseRevision: change.BaseRevision})
		}
		if err != nil {
			return domainError(err)
		}
		if err = audit("group", v.ID, v.Revision); err != nil {
			return err
		}
		changedGroups[v.ID] = true
	}
	for _, change := range c.Addresses {
		v := change.Value
		if change.BaseRevision < 0 || v.SupplierID == "" || len(v.BaseURL) == 0 || len(v.BaseURL) > 2048 || utf8.RuneCountInString(v.Label) > 64 {
			return market.ErrInvalid
		}
		canonical, err := canonicalAddress(v.BaseURL)
		if err != nil {
			return market.ErrInvalid
		}
		v.BaseURL = canonical
		v.Revision = change.BaseRevision + 1
		v.ID, err = allocate(v.ID)
		if err != nil {
			return err
		}
		linked, err := q.GroupsForAddress(ctx, v.ID)
		if err != nil {
			return err
		}
		p := InsertAddressParams{ID: v.ID, SupplierID: v.SupplierID, BaseUrl: v.BaseURL, Label: v.Label, Revision: v.Revision, DeletedAt: v.DeletedAt}
		if change.BaseRevision == 0 {
			_, err = q.InsertAddress(ctx, p)
		} else {
			_, err = q.UpdateAddress(ctx, UpdateAddressParams{ID: p.ID, SupplierID: p.SupplierID, BaseUrl: p.BaseUrl, Label: p.Label, Revision: p.Revision, DeletedAt: p.DeletedAt, BaseRevision: change.BaseRevision})
		}
		if err != nil {
			return domainError(err)
		}
		if err = audit("address", v.ID, v.Revision); err != nil {
			return err
		}
		if v.DeletedAt != nil {
			if err = q.RetireAddressLinks(ctx, v.ID); err != nil {
				return err
			}
		}
		for _, groupID := range linked {
			if _, err = q.BumpGroupConnection(ctx, groupID); err != nil {
				return err
			}
		}
	}
	for _, set := range c.GroupAddresses {
		if set.GroupID == "" || len(set.Links) > 32 {
			return market.ErrInvalid
		}
		if err := q.RetireGroupAddresses(ctx, set.GroupID); err != nil {
			return err
		}
		for i, link := range set.Links {
			if link.GroupID != set.GroupID || link.SupplierID == "" || link.AddressID == "" {
				return market.ErrInvalid
			}
			id, err := allocate(link.ID)
			if err != nil {
				return err
			}
			count, err := q.UpsertGroupAddress(ctx, UpsertGroupAddressParams{ID: id, GroupID: link.GroupID, AddressID: link.AddressID, SupplierID: link.SupplierID, Position: int32(i), DeletedAt: link.DeletedAt})
			if err != nil {
				return domainError(err)
			}
			if count != 1 {
				return market.ErrConflict
			}
		}
		if count, err := q.BumpGroupConnection(ctx, set.GroupID); err != nil {
			return domainError(err)
		} else if count != 1 {
			return market.ErrConflict
		}
	}
	for _, change := range c.Models {
		v := change.Value
		if v.UpstreamModel == "" {
			v.UpstreamModel = v.ModelID
		}
		if v.DisplayName == "" {
			v.DisplayName = v.ModelID
		}
		if change.BaseRevision < 0 || market.ValidateGroupModel(v) != nil {
			return market.ErrInvalid
		}
		var err error
		v.ID, err = allocate(v.ID)
		if err != nil {
			return err
		}
		v.Revision = change.BaseRevision + 1
		group, err := q.GetGroup(ctx, v.GroupID)
		if err != nil {
			return domainError(err)
		}
		v.CapabilityDigest = market.ModelCapabilityDigest(group.CapabilityDigest, v)
		p := InsertGroupModelParams{ID: v.ID, GroupID: v.GroupID, ModelID: v.ModelID, DisplayName: v.DisplayName, UpstreamModel: v.UpstreamModel, ModelType: v.ModelType, ContextWindow: v.ContextWindow, MaxOutputTokens: v.MaxOutputTokens, Notes: v.Notes, Enabled: v.Enabled, IsReference: v.IsReference, Revision: v.Revision, CapabilityDigest: v.CapabilityDigest, DeletedAt: v.DeletedAt}
		if change.BaseRevision == 0 {
			_, err = q.InsertGroupModel(ctx, p)
		} else {
			_, err = q.UpdateGroupModel(ctx, UpdateGroupModelParams{ID: p.ID, GroupID: p.GroupID, ModelID: p.ModelID, DisplayName: p.DisplayName, UpstreamModel: p.UpstreamModel, ModelType: p.ModelType, ContextWindow: p.ContextWindow, MaxOutputTokens: p.MaxOutputTokens, Notes: p.Notes, Enabled: p.Enabled, IsReference: p.IsReference, Revision: p.Revision, CapabilityDigest: p.CapabilityDigest, DeletedAt: p.DeletedAt, BaseRevision: change.BaseRevision})
		}
		if err != nil {
			return domainError(err)
		}
		if err = audit("group_model", v.ID, v.Revision); err != nil {
			return err
		}
	}
	for _, quote := range c.Quotes {
		if err := appendQuote(ctx, q, quote); err != nil {
			return domainError(err)
		}
	}
	// Header/UA changes affect inherited model evidence. Prices/addresses do
	// not. Refresh after explicit child edits so one atomic bundle can update
	// both parent and child without invalidating its own base-revision check.
	for groupID := range changedGroups {
		g, err := q.GetGroup(ctx, groupID)
		if err != nil {
			return err
		}
		models, err := q.ModelsForGroup(ctx, groupID)
		if err != nil {
			return err
		}
		for _, m := range models {
			digest := market.ModelCapabilityDigest(g.CapabilityDigest, ModelValue(m))
			if err = q.RefreshModelCapability(ctx, RefreshModelCapabilityParams{ID: m.ID, CapabilityDigest: digest}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Canonical identity uses the same normalizer as forwarding. Allowing a port
// here does not authorize egress: F2 validates the configured policy before
// publication and every actual outbound call still uses the fixed policy.
func canonicalAddress(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	ports := []string{}
	if port := u.Port(); port != "" {
		ports = append(ports, port)
	}
	policy, err := channel.NewOutboundPolicy(ports, nil)
	if err != nil {
		return "", err
	}
	if u.Scheme == "http" && u.Hostname() == "localhost" {
		policy = policy.WithLocalhostForDevelopment()
	}
	return policy.NormalizeBaseURL(raw)
}

func appendQuote(ctx context.Context, q *Queries, v market.Quote) error {
	if market.ValidateQuote(v) != nil {
		return market.ErrInvalid
	}
	if _, err := q.LockModel(ctx, v.GroupModelID); err != nil {
		return err
	}
	version, err := q.QuoteVersion(ctx, v.GroupModelID)
	if err != nil {
		return err
	}
	v.ID, err = allocate(v.ID)
	if err != nil {
		return err
	}
	_, err = q.InsertQuote(ctx, InsertQuoteParams{ID: v.ID, GroupModelID: v.GroupModelID, Version: version + 1, Currency: v.Currency, BillingUnit: v.BillingUnit, InputPriceNano: v.Prices.Input, OutputPriceNano: v.Prices.Output, CacheWritePriceNano: v.Prices.CacheWrite, CacheReadPriceNano: v.Prices.CacheRead})
	if err != nil {
		return err
	}
	for key, price := range v.Prices.Details {
		if err = q.InsertTokenPrice(ctx, InsertTokenPriceParams{QuoteID: v.ID, Dimension: key, PriceNano: price}); err != nil {
			return err
		}
	}
	for i, t := range v.Tiers {
		c := t.Conditions
		days := make([]int32, len(c.Weekdays))
		for j, day := range c.Weekdays {
			days[j] = int32(day)
		}
		if err = q.InsertTier(ctx, InsertTierParams{QuoteID: v.ID, Seq: int16(i + 1), Name: t.Name, ServiceTier: c.ServiceTier, MinPromptTokens: c.MinPromptTokens, MaxPromptTokens: c.MaxPromptTokens, Timezone: c.Timezone, Weekdays: days, StartMinute: c.StartMinute, EndMinute: c.EndMinute, InputPriceNano: t.Prices.Input, OutputPriceNano: t.Prices.Output, CacheWritePriceNano: t.Prices.CacheWrite, CacheReadPriceNano: t.Prices.CacheRead}); err != nil {
			return err
		}
		for key, price := range t.Prices.Details {
			if err = q.InsertTierTokenPrice(ctx, InsertTierTokenPriceParams{QuoteID: v.ID, Seq: int16(i + 1), Dimension: key, PriceNano: price}); err != nil {
				return err
			}
		}
	}
	for i, p := range v.UnitPrices {
		if err = q.InsertUnitPrice(ctx, InsertUnitPriceParams{QuoteID: v.ID, Seq: int16(i + 1), Unit: p.Unit, Tool: p.Tool, ImageSize: p.ImageSize, ImageQuality: p.ImageQuality, ImageBackground: p.ImageBackground, ServiceTier: p.ServiceTier, PriceNano: p.Price}); err != nil {
			return err
		}
	}
	_, err = q.SetCurrentQuote(ctx, SetCurrentQuoteParams{ID: v.GroupModelID, CurrentQuoteID: &v.ID})
	return err
}
