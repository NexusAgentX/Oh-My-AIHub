package contributionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/detectionpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/marketpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ contribution.Store = (*Store)(nil)

func domainError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return contribution.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.Code == "23514" && strings.Contains(pe.Message, "supplier still has groups") {
			return market.ErrHasGroups
		}
		switch pe.Code {
		case "23505", "23503", "40001", "40P01":
			return contribution.ErrConflict
		case "23514", "22P02", "23502", "22001":
			return contribution.ErrInvalid
		}
	}
	return err
}
func payload(c market.Changes) ([]byte, error) {
	data, err := json.Marshal(c)
	if err != nil || len(data) > 8<<20 {
		return nil, contribution.ErrInvalid
	}
	return data, nil
}
func value(row MarketContribution) (contribution.Draft, error) {
	d := contribution.Draft{ID: row.ID, OwnerID: row.OwnerID, Title: row.Title, Kind: row.Kind, TargetID: row.TargetID, State: row.State, Revision: row.Revision, ReportIDs: append([]string{}, row.ReportIds...), TaskIDs: append([]string{}, row.TaskIds...), PendingPrices: append([]string{}, row.PendingPrices...), ReviewReason: row.ReviewReason, ReviewedBy: row.ReviewedBy, SubmittedAt: row.SubmittedAt, PublishedAt: row.PublishedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if market.Decode(row.PriceBlanks, &d.PriceBlanks) != nil {
		return d, contribution.ErrInvalid
	}
	if market.Decode(row.Document, &d.Changes) != nil || market.Decode(row.BaseDocument, &d.Base) != nil {
		return d, contribution.ErrInvalid
	}
	return d, nil
}
func (s *Store) Create(ctx context.Context, d contribution.Draft) error {
	document, err := payload(d.Changes)
	if err != nil {
		return err
	}
	if d.PriceBlanks == nil {
		d.PriceBlanks = map[string][]string{}
	}
	blanks, err := json.Marshal(d.PriceBlanks)
	if err != nil {
		return contribution.ErrInvalid
	}
	base, err := payload(d.Base)
	if err != nil {
		return err
	}
	return domainError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := New(tx).InsertContribution(ctx, InsertContributionParams{PriceBlanks: blanks, ID: d.ID, OwnerID: d.OwnerID, Kind: d.Kind, TargetID: d.TargetID, Title: d.Title, State: d.State, Revision: d.Revision, Document: document, BaseDocument: base, ReportIds: append([]string{}, d.ReportIDs...), TaskIds: append([]string{}, d.TaskIDs...), PendingPrices: append([]string{}, d.PendingPrices...), ReviewReason: d.ReviewReason, ReviewedBy: d.ReviewedBy, SubmittedAt: d.SubmittedAt, PublishedAt: d.PublishedAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt})
		if err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: d.OwnerID, Action: "market.contribution.created", TargetType: "market_contribution", TargetID: d.ID, Detail: map[string]any{"revision": d.Revision, "kind": d.Kind}})
	}))
}
func (s *Store) Get(ctx context.Context, id string) (contribution.Draft, error) {
	row, err := New(s.pool).GetContribution(ctx, id)
	if err != nil {
		return contribution.Draft{}, domainError(err)
	}
	return value(row)
}
func (s *Store) List(ctx context.Context, actor contribution.Actor, queue bool, cursor *contribution.Cursor, limit int) ([]contribution.Summary, error) {
	if actor.ID == "" || queue && !actor.Admin || limit < 1 || limit > 101 {
		return nil, contribution.ErrInvalid
	}
	var at *time.Time
	var afterID *string
	if cursor != nil {
		at = &cursor.At
		afterID = &cursor.ID
	}
	rows, err := New(s.pool).ListContributions(ctx, ListContributionsParams{OwnerID: actor.ID, Queue: queue, AfterAt: at, AfterID: afterID, PageLimit: int32(limit)})
	if err != nil {
		return nil, domainError(err)
	}
	out := []contribution.Summary{}
	for _, row := range rows {
		out = append(out, contribution.Summary{ID: row.ID, Title: row.Title, Kind: row.Kind, State: row.State, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	return out, nil
}
func (s *Store) Edit(ctx context.Context, id string, fn func(*contribution.Draft, contribution.UnitOfWork) error) (contribution.Draft, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return contribution.Draft{}, domainError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	// A transaction-scoped fence survives a lost COMMIT acknowledgement. The
	// next publication/recovery cannot rebuild a directory while that COMMIT
	// is still in progress. Serializable conflicts remain explicit conflicts.
	if _, err = tx.Exec(ctx, publicationFenceSQL); err != nil {
		return contribution.Draft{}, domainError(err)
	}
	q := New(tx)
	row, err := q.LockContribution(ctx, id)
	if err != nil {
		return contribution.Draft{}, domainError(err)
	}
	d, err := value(row)
	if err != nil {
		return d, err
	}
	if err = fn(&d, &unit{tx: tx, id: id}); err != nil {
		return contribution.Draft{}, domainError(err)
	}
	if d.ID != row.ID || d.OwnerID != row.OwnerID || d.Kind != row.Kind || d.TargetID != row.TargetID || !d.CreatedAt.Equal(row.CreatedAt) {
		return contribution.Draft{}, contribution.ErrInvalid
	}
	if d.Revision != row.Revision {
		if d.Revision != row.Revision+1 {
			return contribution.Draft{}, contribution.ErrInvalid
		}
		document, err := payload(d.Changes)
		if err != nil {
			return d, err
		}
		if d.PriceBlanks == nil {
			d.PriceBlanks = map[string][]string{}
		}
		blanks, err := json.Marshal(d.PriceBlanks)
		if err != nil {
			return d, contribution.ErrInvalid
		}
		base, err := payload(d.Base)
		if err != nil {
			return d, err
		}
		count, err := q.UpdateContribution(ctx, UpdateContributionParams{PriceBlanks: blanks, ID: d.ID, BaseRevision: row.Revision, Title: d.Title, State: d.State, Revision: d.Revision, Document: document, BaseDocument: base, ReportIds: append([]string{}, d.ReportIDs...), TaskIds: append([]string{}, d.TaskIDs...), PendingPrices: append([]string{}, d.PendingPrices...), ReviewReason: d.ReviewReason, ReviewedBy: d.ReviewedBy, SubmittedAt: d.SubmittedAt, PublishedAt: d.PublishedAt, UpdatedAt: d.UpdatedAt})
		if err != nil {
			return d, domainError(err)
		}
		if count != 1 {
			return d, contribution.ErrConflict
		}
	}
	if err = tx.Commit(ctx); err != nil {
		// SQL serialization/constraint errors acknowledge rejection. A transport
		// failure while committing has an unknown outcome and must fail closed.
		var pe *pgconn.PgError
		if errors.As(err, &pe) || errors.Is(err, pgx.ErrTxCommitRollback) {
			return d, domainError(err)
		}
		return d, fmt.Errorf("%w: %w", contribution.ErrCommitUncertain, err)
	}
	return d, nil
}

type unit struct {
	tx pgx.Tx
	id string
}

func (u *unit) Directory(ctx context.Context) (market.Directory, error) {
	return marketpg.Read(ctx, u.tx)
}
func (u *unit) Report(ctx context.Context, id string) (detection.Job, error) {
	return detectionpg.ReadReport(ctx, u.tx, id)
}
func (u *unit) ApprovedReports(ctx context.Context, targets []string) ([]string, error) {
	if len(targets) == 0 {
		return []string{}, nil
	}
	return New(u.tx).ApprovedReportReferences(ctx, targets)
}
func (u *unit) Publish(ctx context.Context, actor string, c market.Changes) error {
	return marketpg.Apply(ctx, u.tx, actor, c)
}
func (u *unit) Audit(ctx context.Context, actor, action string, detail map[string]any) error {
	return auditpg.Record(ctx, u.tx, auditpg.Event{ActorID: actor, Action: "market.contribution." + action, TargetType: "market_contribution", TargetID: u.id, Detail: detail})
}
func (u *unit) RecordEvidence(ctx context.Context, report detection.Job) error {
	if report.ReportID == nil || !report.Complete || !detection.Complete(report) {
		return contribution.ErrReports
	}
	type fact struct {
		Capability string `json:"capability"`
		Layer      string `json:"layer"`
		State      string `json:"state"`
		Reason     string `json:"reason"`
	}
	facts := []fact{}
	for _, result := range report.Results {
		for _, assertion := range result.Assertions {
			state := "unknown"
			switch assertion.State {
			case "pass":
				state = "supported"
			case "unsupported":
				state = "unsupported"
			case "unobserved":
				state = "unobserved"
			}
			facts = append(facts, fact{Capability: assertion.ID, Layer: result.Layer, State: state, Reason: assertion.Reason})
		}
	}
	data, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	return New(u.tx).PublishEvidence(ctx, PublishEvidenceParams{ReportID: *report.ReportID, Evidence: data})
}

func (u *unit) AffectedChannels(ctx context.Context, c market.Changes) (int64, error) {
	groups := []string{}
	for _, v := range c.Groups {
		groups = append(groups, v.Value.ID)
	}
	for _, v := range c.Models {
		groups = append(groups, v.Value.GroupID)
	}
	if len(c.Suppliers) > 0 || len(c.Governance) > 0 {
		d, err := u.Directory(ctx)
		if err != nil {
			return 0, err
		}
		for _, g := range d.Groups {
			for _, v := range c.Suppliers {
				if g.SupplierID == v.Value.ID {
					groups = append(groups, g.ID)
				}
			}
		}
		for _, m := range d.Models {
			for _, v := range c.Governance {
				if m.ModelID == v.Value.ModelID {
					groups = append(groups, m.GroupID)
				}
			}
		}
	}
	if len(groups) == 0 {
		return 0, nil
	}
	return New(u.tx).LinkedChannelCount(ctx, groups)
}

func (u *unit) ActiveDetections(ctx context.Context, tasks []string) ([]market.EvidenceBinding, error) {
	out := []market.EvidenceBinding{}
	if len(tasks) == 0 {
		return out, nil
	}
	rows, err := New(u.tx).ActiveDetectionBindings(ctx, tasks)
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		out = append(out, market.EvidenceBinding{TargetKind: v.TargetKind, TargetID: v.TargetID, TargetRevision: v.TargetRevision, CapabilityDigest: v.CapabilityDigest})
	}
	return out, nil
}

// The key is reserved for market contribution publication in this database.
const publicationFenceSQL = `SELECT pg_advisory_xact_lock(4704367245194902356)`

// SyncPublications waits for any pre-restart COMMIT to finish before boot
// reads a new runtime snapshot. No durable or session-level lock is retained.
func (s *Store) SyncPublications(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, publicationFenceSQL); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Preview evaluates a proposal in a consistent read-only snapshot. It does not
// acquire the publication fence or a draft row lock; mutations still use Edit
// and revalidate their bases and reports in the publication transaction.
func (s *Store) Preview(ctx context.Context, id string, fn func(*contribution.Draft, contribution.UnitOfWork) error) (contribution.Draft, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return contribution.Draft{}, domainError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	row, err := New(tx).GetContribution(ctx, id)
	if err != nil {
		return contribution.Draft{}, domainError(err)
	}
	draft, err := value(row)
	if err != nil {
		return draft, err
	}
	if err = fn(&draft, &unit{tx: tx, id: id}); err != nil {
		return contribution.Draft{}, domainError(err)
	}
	// No write or commit is needed for a preview.
	return draft, nil
}
