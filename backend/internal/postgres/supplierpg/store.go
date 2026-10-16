package supplierpg

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool, New(pool)} }
func (s *Store) List(ctx context.Context) ([]supplier.Supplier, error) {
	// Repeatable read keeps supplier fields and endpoint ownership coherent.
	var items []supplier.Supplier
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
			return err
		}
		q := New(tx)
		rows, err := q.ListSuppliers(ctx)
		if err != nil {
			return err
		}
		endpoints, err := q.ListEndpoints(ctx)
		if err != nil {
			return err
		}
		items = make([]supplier.Supplier, 0, len(rows))
		for _, row := range rows {
			value := supplier.Supplier{ID: row.ID, Name: row.Name, Kind: row.Kind, Summary: row.Summary, Description: row.Description, WebsiteURL: row.WebsiteUrl, DocsURL: row.DocsUrl, StatusURL: row.StatusUrl, IconData: row.IconData, IconSource: row.IconSource, Visible: row.Visible, UpdatedAt: row.UpdatedAt, Endpoints: []supplier.Endpoint{}}
			for _, endpoint := range endpoints {
				if endpoint.SupplierID == row.ID {
					value.Endpoints = append(value.Endpoints, supplier.Endpoint{BaseURL: endpoint.BaseUrl, Label: endpoint.Label})
				}
			}
			items = append(items, value)
		}
		return nil
	})
	return items, err
}
func (s *Store) Save(ctx context.Context, actor string, value supplier.Supplier, create bool) (supplier.Supplier, error) {
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		params := InsertSupplierParams{ID: value.ID, Name: value.Name, Kind: value.Kind, Summary: value.Summary, Description: value.Description, WebsiteUrl: value.WebsiteURL, DocsUrl: value.DocsURL, StatusUrl: value.StatusURL, IconData: value.IconData, IconSource: value.IconSource, Visible: value.Visible}
		if create {
			if err := q.InsertSupplier(ctx, params); err != nil {
				return err
			}
		} else {
			count, err := q.UpdateSupplier(ctx, UpdateSupplierParams(params))
			if err != nil {
				return err
			}
			if count == 0 {
				return supplier.ErrNotFound
			}
		}
		if err := q.DeleteEndpoints(ctx, value.ID); err != nil {
			return err
		}
		for _, endpoint := range value.Endpoints {
			if err := q.InsertEndpoint(ctx, InsertEndpointParams{SupplierID: value.ID, BaseUrl: endpoint.BaseURL, Label: endpoint.Label}); err != nil {
				return err
			}
		}
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "supplier.saved", TargetType: "supplier", TargetID: value.ID, Detail: map[string]any{"name": value.Name, "visible": value.Visible, "endpoints": value.Endpoints}})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		err = supplier.ErrConflict
	}
	value.UpdatedAt = time.Now()
	return value, err
}
func (s *Store) Listings(ctx context.Context) ([]supplier.Listing, error) {
	rows, err := s.q.ListChannelListings(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]supplier.Listing, 0, len(rows))
	for _, row := range rows {
		values = append(values, supplier.Listing{ID: row.ID, Name: row.Name, Summary: row.Summary, Description: row.Description, BaseURL: row.BaseUrl, OwnerID: row.OwnerID, OwnerName: row.OwnerName, Advanced: channel.Advanced{Capabilities: row.Capabilities, ConcurrencyLimit: row.ConcurrencyLimit, RPMLimit: row.RpmLimit, DailyRevenueCap: row.DailyRevenueCapNano, CooldownSeconds: row.CooldownSeconds}})
	}
	return values, nil
}
