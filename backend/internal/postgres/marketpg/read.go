package marketpg

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Snapshot(ctx context.Context) (market.Directory, error) {
	var directory market.Directory
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
			return err
		}
		var err error
		directory, err = Read(ctx, tx)
		return err
	})
	return directory, domainError(err)
}

func SupplierValue(r MarketSupplier) market.Supplier {
	return market.Supplier{ID: r.ID, ContributorID: r.ContributorID, Name: r.Name, Kind: r.Kind, WebsiteURL: r.WebsiteUrl, IconData: r.IconData, IconSource: r.IconSource, Summary: r.Summary, Description: r.Description, DocsURL: r.DocsUrl, Enabled: r.Enabled, Revision: r.Revision, DeletedAt: r.DeletedAt, UpdatedAt: r.UpdatedAt}
}
func GroupValue(r MarketGroup) (market.Group, error) {
	d := market.Defaults{UserAgent: r.UserAgent, ConcurrencyLimit: r.ConcurrencyLimit, RPMLimit: r.RpmLimit, TTFTTimeoutMS: r.TtftTimeoutMs, RequestTimeoutMS: r.RequestTimeoutMs, IdleTimeoutMS: r.IdleTimeoutMs, CooldownFailures: r.CooldownFailures, CooldownSeconds: r.CooldownSeconds}
	if len(r.HeaderRules) > 0 && string(r.HeaderRules) != "null" {
		d.HeaderRules = &market.HeaderRules{}
		if market.Decode(r.HeaderRules, d.HeaderRules) != nil {
			return market.Group{}, market.ErrInvalid
		}
	}
	v := market.Group{ID: r.ID, SupplierID: r.SupplierID, Name: r.Name, Description: r.Description, StatusURL: r.StatusUrl, Defaults: d, Enabled: r.Enabled, Revision: r.Revision, ConnectionVersion: r.ConnectionVersion, CapabilityDigest: r.CapabilityDigest, DeletedAt: r.DeletedAt, UpdatedAt: r.UpdatedAt}
	return v, market.ValidateGroup(v)
}
func ModelValue(r MarketGroupModel) market.GroupModel {
	return market.GroupModel{ID: r.ID, GroupID: r.GroupID, ModelID: r.ModelID, DisplayName: r.DisplayName, UpstreamModel: r.UpstreamModel, ModelType: r.ModelType, ContextWindow: r.ContextWindow, MaxOutputTokens: r.MaxOutputTokens, Notes: r.Notes, Enabled: r.Enabled, IsReference: r.IsReference, QuoteID: r.CurrentQuoteID, Revision: r.Revision, CapabilityDigest: r.CapabilityDigest, DeletedAt: r.DeletedAt, UpdatedAt: r.UpdatedAt}
}

// Read builds a consistent directory using fixed bulk queries, never a query
// per model. The caller supplies a repeatable-read transaction for publication.
// F5 turns this into the runtime snapshot; forwarding performs no price JOIN.
func Read(ctx context.Context, db DBTX) (market.Directory, error) {
	q := New(db)
	d := market.Directory{Suppliers: []market.Supplier{}, Groups: []market.Group{}, Addresses: []market.Address{}, GroupAddresses: []market.GroupAddress{}, Models: []market.GroupModel{}, Quotes: []market.Quote{}}
	suppliers, err := q.ListSuppliers(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range suppliers {
		v := SupplierValue(r)
		if market.ValidateSupplier(v) != nil {
			return d, market.ErrInvalid
		}
		d.Suppliers = append(d.Suppliers, v)
	}
	groups, err := q.ListGroups(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range groups {
		v, err := GroupValue(r)
		if err != nil {
			return d, err
		}
		d.Groups = append(d.Groups, v)
	}
	addresses, err := q.ListAddresses(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range addresses {
		d.Addresses = append(d.Addresses, market.Address{ID: r.ID, SupplierID: r.SupplierID, BaseURL: r.BaseUrl, Label: r.Label, Revision: r.Revision, DeletedAt: r.DeletedAt})
	}
	links, err := q.ListGroupAddresses(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range links {
		d.GroupAddresses = append(d.GroupAddresses, market.GroupAddress{ID: r.ID, GroupID: r.GroupID, AddressID: r.AddressID, SupplierID: r.SupplierID, Position: r.Position, DeletedAt: r.DeletedAt})
	}
	models, err := q.ListGroupModels(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range models {
		v := ModelValue(r)
		if market.ValidateGroupModel(v) != nil {
			return d, market.ErrInvalid
		}
		d.Models = append(d.Models, v)
	}
	quotes, err := q.CurrentQuotes(ctx)
	if err != nil {
		return d, err
	}
	index := map[string]int{}
	for _, r := range quotes {
		index[r.ID] = len(d.Quotes)
		d.Quotes = append(d.Quotes, market.Quote{ID: r.ID, GroupModelID: r.GroupModelID, Version: r.Version, Currency: r.Currency, BillingUnit: r.BillingUnit, Prices: market.TokenPrices{Input: r.InputPriceNano, Output: r.OutputPriceNano, CacheWrite: r.CacheWritePriceNano, CacheRead: r.CacheReadPriceNano, Details: map[string]money.Amount{}}, Tiers: []market.Tier{}, UnitPrices: []market.UnitPrice{}, CreatedAt: r.CreatedAt})
	}
	tokens, err := q.CurrentTokenPrices(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range tokens {
		i, ok := index[r.QuoteID]
		if !ok {
			return d, market.ErrInvalid
		}
		d.Quotes[i].Prices.Details[r.Dimension] = r.PriceNano
	}
	tiers, err := q.CurrentTiers(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range tiers {
		days := make([]int, len(r.Weekdays))
		for i, day := range r.Weekdays {
			days[i] = int(day)
		}
		t := market.Tier{Name: r.Name, Conditions: market.Conditions{ServiceTier: r.ServiceTier, MinPromptTokens: r.MinPromptTokens, MaxPromptTokens: r.MaxPromptTokens, Timezone: r.Timezone, Weekdays: days, StartMinute: r.StartMinute, EndMinute: r.EndMinute}, Prices: market.TokenPrices{Input: r.InputPriceNano, Output: r.OutputPriceNano, CacheWrite: r.CacheWritePriceNano, CacheRead: r.CacheReadPriceNano, Details: map[string]money.Amount{}}}
		i, ok := index[r.QuoteID]
		if !ok || int(r.Seq) != len(d.Quotes[i].Tiers)+1 {
			return d, market.ErrInvalid
		}
		d.Quotes[i].Tiers = append(d.Quotes[i].Tiers, t)
	}
	tierTokens, err := q.CurrentTierTokenPrices(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range tierTokens {
		i, ok := index[r.QuoteID]
		if !ok {
			return d, market.ErrInvalid
		}
		v := &d.Quotes[i]
		if int(r.Seq) > len(v.Tiers) || r.Seq < 1 {
			return d, market.ErrInvalid
		}
		v.Tiers[int(r.Seq)-1].Prices.Details[r.Dimension] = r.PriceNano
	}
	units, err := q.CurrentUnitPrices(ctx)
	if err != nil {
		return d, err
	}
	for _, r := range units {
		i, ok := index[r.QuoteID]
		if !ok {
			return d, market.ErrInvalid
		}
		v := &d.Quotes[i]
		v.UnitPrices = append(v.UnitPrices, market.UnitPrice{Unit: r.Unit, Tool: r.Tool, ImageSize: r.ImageSize, ImageQuality: r.ImageQuality, ImageBackground: r.ImageBackground, ServiceTier: r.ServiceTier, Price: r.PriceNano})
	}
	for _, v := range d.Quotes {
		if market.ValidateQuote(v) != nil {
			return d, market.ErrInvalid
		}
	}
	return d, nil
}
