package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/detectionpg"
)

func TestDetectionMetadataRestartAndAppendOnlyEvidence(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	marketApply(t, s, admin.ID, marketBundle(t, "relay"))
	d := marketSnapshot(t, s)
	store := detectionpg.NewStore(pool)
	parameters := detectionpg.DefaultParameters()
	now := time.Now().UTC()
	job := detection.Job{ID: marketID(t), OwnerID: admin.ID, Target: detection.Target{GroupID: d.Groups[0].ID, Binding: market.EvidenceBinding{TargetKind: "group_model", TargetID: d.Models[0].ID, TargetRevision: d.Models[0].Revision, CapabilityDigest: d.Models[0].CapabilityDigest, ManifestVersion: "synthetic.manifest.v1", CodeVersion: "synthetic.code.v1", ParameterVersion: "synthetic.params.v1", CredentialScope: "protected-equality-label"}, ModelType: "text", Model: d.Models[0].UpstreamModel}, Parameters: parameters, Plan: detection.Plan{ManifestVersion: "synthetic.manifest.v1", RegistryVersion: "synthetic.code.v1", Items: []detection.Planned{{ID: "scenario", SuiteID: "synthetic", Requirements: []string{"req"}, Assertions: []string{"req/a1"}, Implemented: true, Layer: "upstream_protocol", CodeVersion: "synthetic.v1"}}}, State: "running", Results: []detection.Result{}, CreatedAt: now, UpdatedAt: now}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	attempt := detection.Attempt{ID: marketID(t), EndpointID: "responses.create", Method: "POST", Path: "/v1/responses", Status: 200, Reason: "ok", Category: "generate", DurationMS: 3, Sent: true, ResponseReceived: true, At: now}
	if err := store.RecordAttempt(ctx, job.ID, attempt); err != nil {
		t.Fatal(err)
	}
	result := detection.Result{ID: marketID(t), ScenarioID: "scenario", State: "pass", Reason: "ok", Layer: "upstream_protocol", Source: "synthetic", Assertions: []detection.Assertion{{ID: "req/a1", State: "pass", Reason: "ok", AttemptIDs: []string{attempt.ID}}}, Attempts: []detection.Attempt{attempt}, Usage: detection.Usage{HTTP: 1, Generate: 1}, StartedAt: now, FinishedAt: now.Add(time.Millisecond)}
	if err := store.RecordResult(ctx, job.ID, result); err != nil {
		t.Fatal(err)
	}
	job.Results = append(job.Results, result)
	job.Usage = result.Usage
	if err := store.Save(ctx, job); err != nil {
		t.Fatal(err)
	}
	artifact := detection.Artifact{ID: marketID(t), Kind: "response", NativeID: "synthetic-owned-response", DeleteEndpoint: "responses.delete", DeletePath: "/v1/responses/synthetic-owned-response", ProducerAttemptID: attempt.ID, Reason: "ok"}
	if err := store.AddArtifact(ctx, job.ID, artifact); err != nil {
		t.Fatal(err)
	}
	other := job
	other.ID = marketID(t)
	other.Results = []detection.Result{}
	other.State = "queued"
	if err := store.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	forged := artifact
	forged.ID = marketID(t)
	if err := store.AddArtifact(ctx, other.ID, forged); err == nil {
		t.Fatal("foreign task resource accepted")
	}
	if err := store.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != "interrupted" || loaded.Complete || loaded.Target.Binding.CredentialScope != "protected-equality-label" || loaded.Usage.HTTP != 1 || len(loaded.Results) != 1 {
		t.Fatalf("restart lost provenance: %+v", loaded)
	}
	pending, err := store.Artifacts(ctx, job.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("cleanup metadata lost: %v %v", pending, err)
	}
	artifact.Removed = true
	if err := store.SaveArtifact(ctx, job.ID, artifact); err != nil {
		t.Fatal(err)
	}
	pending, err = store.Artifacts(ctx, job.ID)
	if err != nil || len(pending) != 0 {
		t.Fatal("cleaned artifact stays pending")
	}
	for _, sql := range []string{`UPDATE detection_attempts SET status=404 WHERE id=$1`, `DELETE FROM detection_attempts WHERE id=$1`} {
		if _, err := pool.Exec(ctx, sql, attempt.ID); err == nil {
			t.Fatal("attempt history mutable")
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE detection_results SET state='unsupported' WHERE id=$1`, result.ID); err == nil {
		t.Fatal("result history mutable")
	}
	page, err := store.AttemptPage(ctx, job.ID, "", 10)
	if err != nil || len(page) != 1 || page[0].Path != "/v1/responses" {
		t.Fatalf("attempt metadata page: %+v %v", page, err)
	}
	encoded, _ := json.Marshal(loaded)
	if strings.Contains(string(encoded), "protected-equality-label") {
		t.Fatal("credential equality label exposed in report JSON")
	}
}

func TestDetectionSearchSettingsVersionAuditAndRootStability(t *testing.T) {
	pool, s := isolatedDatabase(t)
	_, admin, _ := accounts(t, s)
	ctx := context.Background()
	store := detectionpg.NewStore(pool)
	before, err := store.Parameters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v := before.Context
	v.Method = "ternary"
	v.Precision = 0.02
	v.PrecisionMode = "relative"
	first, err := store.SetSearch(ctx, admin.ID, "context", v)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != "context:1" {
		t.Fatalf("parameter version: %+v", first)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := store.SetSearch(ctx, admin.ID, "context", v); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Parameters(ctx)
	if err != nil || after.Context.Version != "context:3" || after.MaxOutput.Version != before.MaxOutput.Version {
		t.Fatalf("version or unrelated setting lost: %+v %v", after, err)
	}
	if before.Context.Method != "binary" {
		t.Fatal("pinned task parameters changed")
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='detection.search.updated'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("settings audit: %d %v", audits, err)
	}
	old := upstreamKeyringOf(t, "v1", "v1")
	root, err := s.Market.ProtectedRoot(ctx, old, "credential-scope-root-v1")
	if err != nil || len(root) != 32 {
		t.Fatalf("root: %v", err)
	}
	rotated := upstreamKeyringOf(t, "v2", "v1", "v2")
	same, err := s.Market.ProtectedRoot(ctx, rotated, "credential-scope-root-v1")
	if err != nil || !bytes.Equal(root, same) {
		t.Fatal("wrapping key rotation changed identity root")
	}
	if err := s.Market.VerifyProtectedRoots(ctx, upstreamKeyringOf(t, "v2", "v2")); err == nil {
		t.Fatal("missing historical wrapping key did not fail startup")
	}
	var cipher, nonce []byte
	if err := pool.QueryRow(ctx, `SELECT ciphertext,nonce FROM market_protected_keys WHERE purpose='credential-scope-root-v1'`).Scan(&cipher, &nonce); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, root) || bytes.Equal(nonce, root) {
		t.Fatal("namespace root stored in plaintext")
	}
}
