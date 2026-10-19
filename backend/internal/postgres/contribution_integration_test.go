package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/detectionpg"
	"github.com/jackc/pgx/v5/pgxpool"
)

type reviewPlanner struct{}

func (reviewPlanner) Plan(detection.Target) (detection.Plan, error) {
	return detection.Plan{ManifestVersion: "isolated.fixture.v1", RegistryVersion: "isolated.fixture.code", Items: []detection.Planned{{ID: "isolated.fixture", SuiteID: "fixture", Layer: "upstream_protocol", Implemented: true, Assertions: []string{"isolated.fixture/a1"}}}}, nil
}

type reviewAddressPolicy struct{}

func (reviewAddressPolicy) ValidateBaseURL(_ context.Context, v string) (string, error) {
	return v, nil
}

// Trusted-storage mechanism fixtures exercise complete NEGATIVE evidence only.
// They are not real supplier calls, support claims or a runtime test override.
func persistReviewFixture(t *testing.T, pool *pgxpool.Pool, owner string, target detection.Target) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	plan, _ := (reviewPlanner{}).Plan(target)
	params := detectionpg.DefaultParameters()
	data, _ := json.Marshal(params)
	hash := sha256.Sum256(data)
	target.Binding.ManifestVersion = plan.ManifestVersion
	target.Binding.CodeVersion = plan.RegistryVersion
	target.Binding.ParameterVersion = hex.EncodeToString(hash[:])
	target.Binding.CredentialScope = strings.Repeat("a", 64)
	attempt := detection.Attempt{ID: marketID(t), Method: "POST", Path: "/v1/responses", EndpointID: "responses.create", Category: "generate", Status: 400, Sent: true, ResponseReceived: true, Reason: "unsupported", At: now}
	result := detection.Result{ID: marketID(t), ScenarioID: "isolated.fixture", Layer: "upstream_protocol", Source: "real_upstream", State: "unsupported", Reason: "unsupported", Assertions: []detection.Assertion{{ID: "isolated.fixture/a1", State: "unsupported", Reason: "unsupported", AttemptIDs: []string{attempt.ID}}}, Attempts: []detection.Attempt{attempt}, StartedAt: now, FinishedAt: now}
	job := detection.Job{ID: marketID(t), OwnerID: owner, Target: target, Parameters: params, Plan: plan, State: "finished", Complete: true, Results: []detection.Result{result}, CreatedAt: now, UpdatedAt: now}
	if !detection.Complete(job) {
		t.Fatal("negative fixture incomplete")
	}
	store := detectionpg.NewStore(pool)
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, job.ID, attempt); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordResult(ctx, job.ID, result); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Finish(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	return *saved.ReportID
}
func TestContributionPrivateBundleNegativeReviewAndAtomicRetry(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, members := accounts(t, s, "0", "0")
	ctx := context.Background()
	owner := contribution.Actor{ID: members[0].ID}
	reviewer := contribution.Actor{ID: admin.ID, Admin: true}
	installs := 0
	fail := false
	runtime := contribution.NewRuntime(s.Market, func(context.Context, market.Directory) error {
		installs++
		if fail {
			return errors.New("synthetic cache failure")
		}
		return nil
	})
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	service := &contribution.Service{Store: s.Contributions, Live: s.Market, Planner: reviewPlanner{}, Publisher: runtime, Outbound: reviewAddressPolicy{}}
	draft, err := service.New(ctx, owner, contribution.NewInput{Kind: "supplier", Starter: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Get(ctx, contribution.Actor{ID: members[1].ID}, draft.ID); !errors.Is(err, contribution.ErrNotFound) {
		t.Fatal("foreign private draft visible", err)
	}
	changes := draft.Changes
	changes.Suppliers[0].Value.Name = "合成贡献"
	changes.Suppliers[0].Value.WebsiteURL = "https://synthetic.example"
	changes.Groups[0].Value.Name = "合成分组"
	changes.Addresses[0].Value.BaseURL = "https://synthetic.example"
	changes.Models[0].Value.ModelID = "fixture/model:1"
	changes.Quotes[0].Prices = market.TokenPrices{Details: map[string]money.Amount{}}
	draft, err = service.Save(ctx, owner, draft.ID, draft.Revision, "合成贡献", changes, nil, []string{changes.Models[0].Value.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(marketSnapshot(t, s).Suppliers) != 0 {
		t.Fatal("private proposal published early")
	}
	if _, err = service.Submit(ctx, owner, draft.ID, draft.Revision); !errors.Is(err, contribution.ErrReports) {
		t.Fatal("missing reports accepted", err)
	}
	_, targets, err := contribution.Overlay(draft, market.Directory{})
	if err != nil {
		t.Fatal(err)
	}
	reports := []string{}
	for _, target := range targets {
		target.DraftID = draft.ID
		reports = append(reports, persistReviewFixture(t, pool, owner.ID, target))
	}
	draft, err = service.Save(ctx, owner, draft.ID, draft.Revision, draft.Title, draft.Changes, reports, []string{changes.Models[0].Value.ID})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Submit(ctx, owner, draft.ID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	pending, err := service.Approve(ctx, reviewer, draft.ID, submitted.Revision, "合成审核")
	if !errors.Is(err, contribution.ErrPublication) || pending.State != "publication_pending" {
		t.Fatal("commit incorrectly reported rollback", pending, err)
	}
	if _, err = runtime.Current(); !errors.Is(err, contribution.ErrPublication) {
		t.Fatal("stale runtime served after committed publication")
	}
	live := marketSnapshot(t, s)
	if len(live.Quotes) != 1 || live.Suppliers[0].ContributorID != owner.ID {
		t.Fatal("bundle publication or attribution failed")
	}
	fail = false
	approved, err := service.Approve(ctx, reviewer, draft.ID, pending.Revision, "")
	if err != nil || approved.State != "approved" {
		t.Fatal(approved, err)
	}
	if _, err = service.Approve(ctx, reviewer, draft.ID, approved.Revision, ""); err != nil {
		t.Fatal("idempotent review failed", err)
	}
	var quotes, evidence int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM market_quotes`).Scan(&quotes); err != nil || quotes != 1 {
		t.Fatal("duplicate price version", quotes, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM market_capability_evidence`).Scan(&evidence); err != nil || evidence != 2 {
		t.Fatal("approved negative evidence missing", evidence, err)
	}
	if _, err = service.New(ctx, reviewer, contribution.NewInput{Kind: "supplier", TargetID: live.Suppliers[0].ID, Action: "delete"}); !errors.Is(err, market.ErrHasGroups) {
		t.Fatal("supplier deletion bypassed group guard", err)
	}
	if installs != 3 {
		t.Fatal("unexpected install or duplicate publish", installs)
	}
	// Existing trusted negative evidence is reused for pure quote changes.
	edit, err := service.New(ctx, owner, contribution.NewInput{Kind: "group_model", TargetID: live.Models[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	edit, err = service.Submit(ctx, owner, edit.ID, edit.Revision)
	if err != nil {
		t.Fatal("valid approved evidence not reusable", err)
	}
	// Two reviews of the same revision cannot both publish.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Approve(ctx, reviewer, edit.ID, edit.Revision, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, contribution.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("double review lost update", success, conflicts)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM market_quotes`).Scan(&quotes); err != nil || quotes != 2 {
		t.Fatal("concurrent review produced duplicate version", quotes, err)
	}
	// Delete through the full review path with real financial/Key rows and an
	// attached channel. The config cascade must preserve protected history.
	channelID := marketID(t)
	sealed, err := upstreamKeyringOf(t, "v1", "v1").Encrypt(channelID, 1, "synthetic-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channels(id,owner_id,name,api_key_ciphertext,api_key_nonce,api_key_key_id,market_group_id,status,suspended_reason) VALUES($1,$2,'合成渠道',$3,$4,$5,$6,'suspended','合成暂停')`, channelID, owner.ID, sealed.Ciphertext, sealed.Nonce, sealed.KeyID, live.Groups[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Ledger.Adjust(ctx, ledger.Adjustment{ActorID: admin.ID, AccountID: owner.ID, Amount: mustAmount(t, "25"), Reason: "合成保全样本", IdempotencyKey: "contribution-preserve"}); err != nil {
		t.Fatal(err)
	}
	keys := apikey.NewService(s.Keys, upstreamKeyringOf(t, "v1", "v1"), func(context.Context) ([]string, error) { return nil, nil })
	if _, _, err = keys.Create(ctx, owner.ID, apikey.CreateInput{Name: "合成平台Key", Settings: apikey.Settings{Status: apikey.StatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	beforeProtected := marketProtectedRows(t, pool)
	removal, err := service.New(ctx, owner, contribution.NewInput{Kind: "group", TargetID: live.Groups[0].ID, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.Detail(ctx, reviewer, removal.ID)
	if err != nil || detail.LinkedChannels != 1 {
		t.Fatal("cascade impact absent", detail.LinkedChannels, err)
	}
	removal, err = service.Submit(ctx, owner, removal.ID, removal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(ctx, reviewer, removal.ID, removal.Revision, "合成删除审核"); err != nil {
		t.Fatal(err)
	}
	removal, err = service.New(ctx, owner, contribution.NewInput{Kind: "supplier", TargetID: live.Suppliers[0].ID, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	removal, err = service.Submit(ctx, owner, removal.ID, removal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(ctx, reviewer, removal.ID, removal.Revision, ""); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	if err = pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND status='unlisted' AND suspended_reason IS NULL FROM channels WHERE id=$1`, channelID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("review did not cascade channel business deletion", err)
	}
	if after := marketProtectedRows(t, pool); after != beforeProtected {
		t.Fatal("review deletion changed protected history, balances, accounts or Keys")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM market_quotes`).Scan(&quotes); err != nil || quotes != 2 {
		t.Fatal("historical quotes lost during deletion", quotes, err)
	}

}
func TestContributionModelGovernanceNeedsAdminAndPreservesExactID(t *testing.T) {
	_, s := isolatedDatabase(t)
	_, admin, members := accounts(t, s, "0", "0")
	ctx := context.Background()
	runtime := contribution.NewRuntime(s.Market, nil)
	service := &contribution.Service{Store: s.Contributions, Live: s.Market, Planner: reviewPlanner{}, Publisher: runtime}
	input := contribution.NewInput{Kind: "model_id", TargetID: "Vendor/Model:V1", Action: "disable"}
	if _, err := service.New(ctx, contribution.Actor{ID: members[0].ID}, input); !errors.Is(err, contribution.ErrInvalid) {
		t.Fatal("member governs ID", err)
	}
	actor := contribution.Actor{ID: admin.ID, Admin: true}
	d, err := service.New(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.Submit(ctx, actor, d.ID, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(ctx, actor, d.ID, d.Revision, "合成治理"); err != nil {
		t.Fatal(err)
	}
	live := marketSnapshot(t, s)
	if len(live.Governance) != 1 || live.Governance[0].ModelID != input.TargetID || !live.Governance[0].Disabled || len(live.Models) != 0 {
		t.Fatal("governance created catalog or changed ID", live)
	}
}

func TestContributionRecoveryFenceWaitsForEarlierTransaction(t *testing.T) {
	pool, s := isolatedDatabase(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4704367245194902356)`); err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = s.Contributions.SyncPublications(waiting)
	cancel()
	if err == nil {
		t.Fatal("boot passed an outstanding publication")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	released, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = s.Contributions.SyncPublications(released); err != nil {
		t.Fatal("publication fence leaked after transaction", err)
	}
}

func TestContributionPreviewHasNoPublicationFenceAndCannotWrite(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, _, members := accounts(t, s, "0", "0")
	ctx := context.Background()
	service := &contribution.Service{Store: s.Contributions, Live: s.Market}
	draft, err := service.New(ctx, contribution.Actor{ID: members[0].ID}, contribution.NewInput{Kind: "supplier"})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, "SELECT pg_advisory_xact_lock(4704367245194902356)"); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err = s.Contributions.Preview(bounded, draft.ID, func(d *contribution.Draft, u contribution.UnitOfWork) error {
		_, err := u.Directory(bounded)
		return err
	}); err != nil {
		t.Fatal("read-only preview waited for publication fence", err)
	}
	if _, err = s.Contributions.Preview(ctx, draft.ID, func(d *contribution.Draft, u contribution.UnitOfWork) error {
		return u.Audit(ctx, members[0].ID, "must-not-write", nil)
	}); err == nil {
		t.Fatal("read-only preview wrote an audit record")
	}
	got, err := service.Get(ctx, contribution.Actor{ID: members[0].ID}, draft.ID)
	if err != nil || got.Revision != draft.Revision {
		t.Fatal("preview changed draft", err)
	}
}
