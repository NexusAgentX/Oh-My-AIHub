package detectionpg

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Use a separate small pool when wiring the service: detection must not occupy
// forwarding/settlement connection slots, even while a report is being saved.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ detection.Store = (*Store)(nil)

func taskData(job detection.Job) ([]byte, error) {
	if job.ID == "" || job.OwnerID == "" || len(job.Plan.Items) > 512 || len(job.Results) > 512 || job.Parameters.Context.Validate() != nil || job.Parameters.MaxOutput.Validate() != nil {
		return nil, detection.ErrInvalid
	}
	b, err := json.Marshal(job)
	if err != nil || len(b) > 3<<20 {
		return nil, detection.ErrInvalid
	}
	return b, nil
}
func (s *Store) Create(ctx context.Context, job detection.Job) error {
	data, err := taskData(job)
	if err != nil {
		return err
	}
	b := job.Target.Binding
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.InsertBinding(ctx, InsertBindingParams{ID: job.ID, OwnerID: job.OwnerID, TargetKind: b.TargetKind, TargetID: b.TargetID, TargetRevision: b.TargetRevision, CapabilityDigest: b.CapabilityDigest, ManifestVersion: b.ManifestVersion, CodeVersion: b.CodeVersion, ParameterVersion: b.ParameterVersion, CredentialScope: b.CredentialScope}); err != nil {
			return err
		}
		return q.InsertTask(ctx, InsertTaskParams{ID: job.ID, OwnerID: job.OwnerID, State: job.State, Complete: job.Complete, Metadata: data, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt})
	})
}
func decodeTask(data []byte, id, owner, state, scope string, complete bool) (detection.Job, error) {
	var job detection.Job
	if market.Decode(data, &job) != nil || job.ID != id || job.OwnerID != owner || job.State != state || job.Complete != complete {
		return job, detection.ErrInvalid
	}
	job.Target.Binding.CredentialScope = scope
	return job, nil
}
func (s *Store) Get(ctx context.Context, id string) (detection.Job, error) {
	r, err := New(s.pool).GetTask(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return detection.Job{}, detection.ErrNotFound
	}
	if err != nil {
		return detection.Job{}, err
	}
	return decodeTask(r.Metadata, r.ID, r.OwnerID, r.State, r.CredentialScope, r.Complete)
}
func (s *Store) List(ctx context.Context, owner string, admin bool, cursor *detection.Cursor, limit int) ([]detection.Job, error) {
	if limit < 1 || limit > 101 {
		return nil, detection.ErrInvalid
	}
	var at *time.Time
	var afterID *string
	if cursor != nil {
		at = &cursor.At
		afterID = &cursor.ID
	}
	rows, err := New(s.pool).ListTasks(ctx, ListTasksParams{OwnerID: owner, IsAdmin: admin, PageLimit: int32(limit), AfterAt: at, AfterID: afterID})
	if err != nil {
		return nil, err
	}
	out := []detection.Job{}
	for _, r := range rows {
		job, err := decodeTask(r.Metadata, r.ID, r.OwnerID, r.State, r.CredentialScope, r.Complete)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, nil
}
func (s *Store) Save(ctx context.Context, job detection.Job) error {
	data, err := taskData(job)
	if err != nil {
		return err
	}
	count, err := New(s.pool).UpdateTask(ctx, UpdateTaskParams{ID: job.ID, State: job.State, Complete: job.Complete, Metadata: data, UpdatedAt: job.UpdatedAt})
	if err == nil && count != 1 {
		return detection.ErrNotFound
	}
	return err
}
func (s *Store) Interrupt(ctx context.Context) error { return New(s.pool).InterruptTasks(ctx) }
func (s *Store) RecordAttempt(ctx context.Context, jobID string, a detection.Attempt) error {
	return New(s.pool).InsertAttempt(ctx, InsertAttemptParams{ID: a.ID, TaskID: jobID, EndpointID: a.EndpointID, Method: a.Method, PathTemplate: a.Path, Status: int32(a.Status), Reason: a.Reason, Category: a.Category, DurationMs: a.DurationMS, Sent: a.Sent, ResponseReceived: a.ResponseReceived, CreatedAt: a.At})
}
func attemptValue(a DetectionAttempt) detection.Attempt {
	return detection.Attempt{ID: a.ID, EndpointID: a.EndpointID, Method: a.Method, Path: a.PathTemplate, Status: int(a.Status), Reason: a.Reason, Category: a.Category, DurationMS: a.DurationMs, Sent: a.Sent, ResponseReceived: a.ResponseReceived, At: a.CreatedAt}
}
func (s *Store) Attempts(ctx context.Context, jobID string, ids []string) ([]detection.Attempt, error) {
	if len(ids) > 1024 {
		return nil, detection.ErrInvalid
	}
	rows, err := New(s.pool).FindAttempts(ctx, FindAttemptsParams{TaskID: jobID, Column2: ids})
	if err != nil {
		return nil, err
	}
	out := []detection.Attempt{}
	for _, a := range rows {
		out = append(out, attemptValue(a))
	}
	return out, nil
}
func (s *Store) AttemptPage(ctx context.Context, jobID string, cursor *detection.Cursor, limit int) ([]detection.Attempt, error) {
	if limit < 1 || limit > 101 {
		return nil, detection.ErrInvalid
	}
	var at *time.Time
	var afterID *string
	if cursor != nil {
		at = &cursor.At
		afterID = &cursor.ID
	}
	rows, err := New(s.pool).PageAttempts(ctx, PageAttemptsParams{TaskID: jobID, AfterAt: at, AfterID: afterID, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := []detection.Attempt{}
	for _, a := range rows {
		out = append(out, attemptValue(a))
	}
	return out, nil
}

func searchValue(row MarketSearchSetting) gatewaycontract.SearchSettings {
	return gatewaycontract.SearchSettings{Version: row.Target + ":" + strconv.FormatInt(row.Revision, 10), Method: row.Method, Low: row.Low, High: row.High, HardMaximum: row.HardMaximum, PrecisionMode: row.PrecisionMode, Precision: row.Precision, Rounds: int(row.Rounds), Rechecks: int(row.Rechecks)}
}

// Defaults describe a finite interval search, not a cumulative test budget.
func DefaultParameters() detection.Parameters {
	return detection.Parameters{Context: gatewaycontract.SearchSettings{Version: "default.context.v1", Method: "binary", Low: 0, High: 131072, HardMaximum: 2097152, PrecisionMode: "absolute", Precision: 1024, Rounds: 24, Rechecks: 1}, MaxOutput: gatewaycontract.SearchSettings{Version: "default.max-output.v1", Method: "binary", Low: 0, High: 16384, HardMaximum: 131072, PrecisionMode: "absolute", Precision: 256, Rounds: 24, Rechecks: 1}}
}
func (s *Store) Parameters(ctx context.Context) (detection.Parameters, error) {
	out := DefaultParameters()
	rows, err := New(s.pool).LatestSearchSettings(ctx)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		v := searchValue(row)
		if err := v.Validate(); err != nil {
			return out, err
		}
		if row.Target == "context" {
			out.Context = v
		} else {
			out.MaxOutput = v
		}
	}
	return out, nil
}
func (s *Store) SetSearch(ctx context.Context, actor, target string, value gatewaycontract.SearchSettings) (gatewaycontract.SearchSettings, error) {
	value.Version = "pending"
	if actor == "" || (target != "context" && target != "max_output") || value.Validate() != nil || value.Rounds > 128 || value.Rechecks > 8 {
		return value, detection.ErrInvalid
	}
	id, err := ids.NewUUID()
	if err != nil {
		return value, err
	}
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.LockSearchSetting(ctx, &target); err != nil {
			return err
		}
		row, err := q.InsertSearchSetting(ctx, InsertSearchSettingParams{ID: id, Target: target, Method: value.Method, Low: value.Low, High: value.High, HardMaximum: value.HardMaximum, PrecisionMode: value.PrecisionMode, Precision: value.Precision, Rounds: int32(value.Rounds), Rechecks: int32(value.Rechecks), ActorID: actor})
		if err != nil {
			return err
		}
		value = searchValue(row)
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "detection.search.updated", TargetType: "detection_search", TargetID: id, Detail: map[string]any{"target": target, "revision": row.Revision}})
	})
	return value, err
}

func (s *Store) AddArtifact(ctx context.Context, jobID string, a detection.Artifact) error {
	return New(s.pool).InsertArtifact(ctx, InsertArtifactParams{ID: a.ID, TaskID: jobID, Kind: a.Kind, NativeID: a.NativeID, DeleteEndpoint: a.DeleteEndpoint, DeletePath: a.DeletePath, ProducerAttemptID: a.ProducerAttemptID, Removed: a.Removed, Reason: a.Reason})
}
func (s *Store) Artifacts(ctx context.Context, jobID string) ([]detection.Artifact, error) {
	rows, err := New(s.pool).ListArtifacts(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := []detection.Artifact{}
	for _, a := range rows {
		out = append(out, detection.Artifact{ID: a.ID, Kind: a.Kind, NativeID: a.NativeID, DeleteEndpoint: a.DeleteEndpoint, DeletePath: a.DeletePath, ProducerAttemptID: a.ProducerAttemptID, Removed: a.Removed, Reason: a.Reason})
	}
	return out, nil
}
func (s *Store) SaveArtifact(ctx context.Context, jobID string, a detection.Artifact) error {
	count, err := New(s.pool).UpdateArtifact(ctx, UpdateArtifactParams{ID: a.ID, TaskID: jobID, Removed: a.Removed, Reason: a.Reason})
	if err == nil && count != 1 {
		return detection.ErrNotFound
	}
	return err
}

func (s *Store) RecordResult(ctx context.Context, jobID string, result detection.Result) error {
	data, err := json.Marshal(result)
	if err != nil || len(data) > 1<<20 {
		return detection.ErrInvalid
	}
	return New(s.pool).InsertResult(ctx, InsertResultParams{ID: result.ID, TaskID: jobID, ScenarioID: result.ScenarioID, State: result.State, Result: data})
}

// Finish atomically appends an immutable report and advances the task's latest
// view. F2 references ReportID, so a later retest cannot rewrite its evidence.
func (s *Store) Finish(ctx context.Context, job detection.Job) (detection.Job, error) {
	reportID, err := ids.NewUUID()
	if err != nil {
		return job, err
	}
	job.ReportID = &reportID
	job.PendingCleanup = append([]detection.Artifact{}, job.PendingCleanup...)
	job.ExecutionActive = false
	data, err := taskData(job)
	if err != nil {
		return job, err
	}
	b := job.Target.Binding
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.InsertBinding(ctx, InsertBindingParams{ID: reportID, OwnerID: job.OwnerID, TargetKind: b.TargetKind, TargetID: b.TargetID, TargetRevision: b.TargetRevision, CapabilityDigest: b.CapabilityDigest, ManifestVersion: b.ManifestVersion, CodeVersion: b.CodeVersion, ParameterVersion: b.ParameterVersion, CredentialScope: b.CredentialScope}); err != nil {
			return err
		}
		if err := q.InsertReport(ctx, InsertReportParams{ID: reportID, TaskID: job.ID, OwnerID: job.OwnerID, State: job.State, Complete: job.Complete, Metadata: data, CreatedAt: job.UpdatedAt}); err != nil {
			return err
		}
		count, err := q.UpdateTask(ctx, UpdateTaskParams{ID: job.ID, State: job.State, Complete: job.Complete, Metadata: data, UpdatedAt: job.UpdatedAt})
		if err == nil && count != 1 {
			return detection.ErrNotFound
		}
		return err
	})
	return job, err
}
func (s *Store) GetReport(ctx context.Context, id string) (detection.Job, error) {
	row, err := New(s.pool).GetReport(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return detection.Job{}, detection.ErrNotFound
	}
	if err != nil {
		return detection.Job{}, err
	}
	return decodeTask(row.Metadata, row.TaskID, row.OwnerID, row.State, row.CredentialScope, row.Complete)
}
func (s *Store) ReportPage(ctx context.Context, taskID string, cursor *detection.Cursor, limit int) ([]detection.ReportSummary, error) {
	if limit < 1 || limit > 101 {
		return nil, detection.ErrInvalid
	}
	var at *time.Time
	var id *string
	if cursor != nil {
		at = &cursor.At
		id = &cursor.ID
	}
	rows, err := New(s.pool).PageReports(ctx, PageReportsParams{TaskID: taskID, AfterAt: at, AfterID: id, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := []detection.ReportSummary{}
	for _, row := range rows {
		out = append(out, detection.ReportSummary{ID: row.ID, TaskID: row.TaskID, State: row.State, Complete: row.Complete, CreatedAt: row.CreatedAt})
	}
	return out, nil
}
