package contribution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type memoryReview struct {
	mu        sync.Mutex
	draft     Draft
	live      market.Directory
	reports   map[string]detection.Job
	publishes int
	audits    []string
	uncertain bool
}

func clone[T any](value T) T {
	encoded, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(encoded, &out)
	return out
}
func (m *memoryReview) Create(_ context.Context, d Draft) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draft = clone(d)
	return nil
}
func (m *memoryReview) Get(_ context.Context, id string) (Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.draft.ID != id {
		return Draft{}, ErrNotFound
	}
	return clone(m.draft), nil
}
func (m *memoryReview) List(context.Context, Actor, bool, *Cursor, int) ([]Summary, error) {
	return nil, nil
}
func (m *memoryReview) Edit(ctx context.Context, id string, fn func(*Draft, UnitOfWork) error) (Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.draft.ID {
		return Draft{}, ErrNotFound
	}
	before, publishes, audits := clone(m.live), m.publishes, len(m.audits)
	next := clone(m.draft)
	if err := fn(&next, m); err != nil {
		m.live = before
		m.publishes = publishes
		m.audits = m.audits[:audits]
		return Draft{}, err
	}
	m.draft = clone(next)
	if m.uncertain && next.State == "publication_pending" {
		m.uncertain = false
		return next, ErrCommitUncertain
	}
	return next, nil
}
func (m *memoryReview) Directory(context.Context) (market.Directory, error) {
	return clone(m.live), nil
}
func (m *memoryReview) Report(_ context.Context, id string) (detection.Job, error) {
	r, ok := m.reports[id]
	if !ok {
		return detection.Job{}, detection.ErrNotFound
	}
	return r, nil
}
func (*memoryReview) ApprovedReports(context.Context, []string) ([]string, error) {
	return []string{}, nil
}
func (m *memoryReview) Publish(_ context.Context, actor string, c market.Changes) error {
	next, _, err := Overlay(Draft{OwnerID: m.draft.OwnerID, Changes: c}, m.live)
	if err != nil {
		return err
	}
	m.live = next
	m.publishes++
	return nil
}
func (*memoryReview) RecordEvidence(context.Context, detection.Job) error { return nil }
func (m *memoryReview) Audit(_ context.Context, actor, action string, _ map[string]any) error {
	m.audits = append(m.audits, action)
	return nil
}

type fixturePlanner struct{}

func (fixturePlanner) Plan(detection.Target) (detection.Plan, error) { return detection.Plan{}, nil }

type memoryPublisher struct {
	blocked, prior, fail bool
	restored             int
}

func (p *memoryPublisher) Block()   { p.prior = p.blocked; p.blocked = true }
func (p *memoryPublisher) Restore() { p.blocked = p.prior; p.restored++ }
func (p *memoryPublisher) Refresh(context.Context) error {
	if p.fail {
		return errors.New("synthetic refresh failure")
	}
	p.blocked = false
	return nil
}
func metadataService(t *testing.T) (*Service, *memoryReview, *memoryPublisher) {
	t.Helper()
	starter := starterDraft()
	starter.Changes = market.Changes{Suppliers: starter.Changes.Suppliers}
	starter.State = "draft"
	starter.Revision = 1
	starter.Title = "Synthetic metadata"
	repo := &memoryReview{draft: starter, reports: map[string]detection.Job{}}
	publisher := &memoryPublisher{}
	return &Service{Store: repo, Planner: fixturePlanner{}, Publisher: publisher}, repo, publisher
}
func TestCommittedRefreshFailureRetriesWithoutRepublishing(t *testing.T) {
	s, repo, publisher := metadataService(t)
	ctx := context.Background()
	owner := Actor{ID: "member"}
	admin := Actor{ID: "admin", Admin: true}
	submitted, err := s.Submit(ctx, owner, "draft", 1)
	if err != nil {
		t.Fatal(err)
	}
	publisher.fail = true
	pending, err := s.Approve(ctx, admin, "draft", submitted.Revision, "")
	if !errors.Is(err, ErrPublication) || pending.State != "publication_pending" || repo.publishes != 1 || !publisher.blocked || repo.live.Suppliers[0].ContributorID != "member" {
		t.Fatalf("committed state hidden or creator lost: %+v %v", pending, err)
	}
	publisher.fail = false
	approved, err := s.Approve(ctx, admin, "draft", pending.Revision, "")
	if err != nil || approved.State != "approved" || repo.publishes != 1 || publisher.blocked {
		t.Fatalf("retry duplicated publication: %+v %v", approved, err)
	}
}
func TestConcurrentReviewAndAdminEditCannotOverwriteAnotherRevision(t *testing.T) {
	s, repo, _ := metadataService(t)
	ctx := context.Background()
	submitted, err := s.Submit(ctx, Actor{ID: "member"}, "draft", 1)
	if err != nil {
		t.Fatal(err)
	}
	errorsSeen := make(chan error, 2)
	var wg sync.WaitGroup
	for _, admin := range []string{"admin-a", "admin-b"} {
		wg.Go(func() {
			_, err := s.Approve(ctx, Actor{ID: admin, Admin: true}, "draft", submitted.Revision, "")
			errorsSeen <- err
		})
	}
	wg.Wait()
	close(errorsSeen)
	completed, conflicts := 0, 0
	for err := range errorsSeen {
		if err == nil {
			completed++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if completed != 1 || conflicts != 1 || repo.publishes != 1 {
		t.Fatal("two approvers published twice")
	}
	s, repo, _ = metadataService(t)
	submitted, err = s.Submit(ctx, Actor{ID: "member"}, "draft", 1)
	if err != nil {
		t.Fatal(err)
	}
	changes := clone(submitted.Changes)
	changes.Suppliers[0].Value.Name = "Admin revision"
	edited, err := s.Save(ctx, Actor{ID: "admin", Admin: true}, "draft", submitted.Revision, "Admin edit", changes, nil)
	if err != nil || edited.State != "draft" || edited.SubmittedAt != nil {
		t.Fatal("admin edit retained obsolete submission", err)
	}
	if _, err = s.Approve(ctx, Actor{ID: "admin", Admin: true}, "draft", submitted.Revision, ""); !errors.Is(err, ErrConflict) || repo.publishes != 0 {
		t.Fatal("late review overwrote edited draft", err)
	}
}
func TestDraftPermissionsIdentityAndCommitAcknowledgement(t *testing.T) {
	s, repo, publisher := metadataService(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, Actor{ID: "other"}, "draft"); !errors.Is(err, ErrNotFound) {
		t.Fatal("private draft exposed", err)
	}
	changes := clone(repo.draft.Changes)
	changes.Suppliers[0].Value.ID = "foreign"
	if _, err := s.Save(ctx, Actor{ID: "member"}, "draft", 1, "Injected", changes, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign object injected", err)
	}
	if _, err := s.Save(ctx, Actor{ID: "member"}, "draft", 1, "Empty", market.Changes{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty proposal became ready", err)
	}
	submitted, err := s.Submit(ctx, Actor{ID: "member"}, "draft", 1)
	if err != nil {
		t.Fatal(err)
	}
	repo.uncertain = true
	approved, err := s.Approve(ctx, Actor{ID: "admin", Admin: true}, "draft", submitted.Revision, "")
	if err != nil || approved.State != "approved" || repo.publishes != 1 || publisher.restored != 0 {
		t.Fatal("lost COMMIT acknowledgement treated as rollback", err)
	}
}
