package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func (a *app) registerDetectionRoutes(r *router) {
	r.implement("490", "GET /api/detection/reports/{reportID}", accessReady, a.getDetectionReport)
	r.implement("490", "GET /api/detection/jobs/{taskID}/reports", accessReady, a.detectionReports)
	r.implement("490", "GET /api/detection/jobs", accessReady, a.listDetectionJobs)
	r.implement("490", "POST /api/detection/jobs", accessReady, a.createDetectionJob)
	r.implement("490", "GET /api/detection/jobs/{taskID}", accessReady, a.getDetectionJob)
	r.implement("490", "POST /api/detection/jobs/{taskID}/cancel", accessReady, a.cancelDetectionJob)
	r.implement("490", "POST /api/detection/jobs/{taskID}/resume", accessReady, a.resumeDetectionJob)
	r.implement("490", "POST /api/detection/jobs/{taskID}/retest", accessReady, a.retestDetectionJob)
	r.implement("490", "GET /api/detection/jobs/{taskID}/attempts", accessReady, a.detectionAttempts)
	r.implement("490", "GET /api/detection/jobs/{taskID}/artifacts", accessReady, a.detectionArtifacts)
	r.implement("490", "GET /api/admin/detection/search", accessAdmin, a.getDetectionParameters)
	r.implement("490", "PUT /api/admin/detection/search/{target}", accessAdmin, a.setDetectionSearch)
}
func (a *app) detectionReady(w http.ResponseWriter) bool {
	if a.detection == nil {
		writeError(w, 503, "detection_unavailable", "检测服务暂不可用")
		return false
	}
	return true
}
func detectionActor(r *http.Request) detection.Actor {
	account := accountFromContext(r.Context())
	return detection.Actor{ID: account.ID, Admin: account.IsAdmin}
}
func writeDetectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, detection.ErrInvalid):
		writeError(w, 422, "invalid_detection", "检测参数无效")
	case errors.Is(err, detection.ErrNotFound):
		writeError(w, 404, "not_found", "资源不存在")
	case errors.Is(err, detection.ErrConflict):
		writeError(w, 409, "detection_inputs_changed", "目标或检测版本已变化，请新建检测")
	case errors.Is(err, detection.ErrBusy):
		writeError(w, 429, "detection_busy", "检测任务已满，请稍后重试")
	case errors.Is(err, detection.ErrClosed):
		writeError(w, 503, "detection_unavailable", "检测服务暂不可用")
	default:
		writeDomainError(w, err)
	}
}
func detectionCursor(r *http.Request) (*detection.Cursor, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return nil, true
	}
	decoded, ok := decodeTextCursor(raw)
	if !ok {
		return nil, false
	}
	at, id, ok := strings.Cut(decoded, "|")
	if !ok || !uuidPattern.MatchString(id) {
		return nil, false
	}
	stamp, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, false
	}
	return &detection.Cursor{At: stamp, ID: id}, true
}
func nextDetectionCursor(at time.Time, id string) *string {
	v := encodeTextCursor(at.UTC().Format(time.RFC3339Nano) + "|" + id)
	return &v
}

type detectionSummary struct {
	ExecutionActive   bool             `json:"execution_active"`
	ID                string           `json:"id"`
	OwnerID           string           `json:"owner_id"`
	Target            detection.Target `json:"target"`
	State             string           `json:"state"`
	Complete          bool             `json:"complete"`
	Scenarios         int              `json:"scenarios"`
	FinishedScenarios int              `json:"finished_scenarios"`
	Usage             detection.Usage  `json:"usage"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

func (a *app) listDetectionJobs(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	limit, ok := pageLimit(r)
	cursor, cursorOK := detectionCursor(r)
	if !ok || !cursorOK {
		writeBadCursor(w)
		return
	}
	jobs, err := a.detection.List(r.Context(), detectionActor(r), cursor, limit+1)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	more := len(jobs) > limit
	if more {
		jobs = jobs[:limit]
	}
	items := []detectionSummary{}
	for _, job := range jobs {
		items = append(items, detectionSummary{ExecutionActive: job.ExecutionActive, ID: job.ID, OwnerID: job.OwnerID, Target: job.Target, State: job.State, Complete: job.Complete, Scenarios: len(job.Plan.Items), FinishedScenarios: len(job.Results), Usage: job.Usage, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt})
	}
	var next *string
	if more && len(jobs) > 0 {
		last := jobs[len(jobs)-1]
		next = nextDetectionCursor(last.CreatedAt, last.ID)
	}
	writeJSON(w, 200, pageJSON[detectionSummary]{Items: items, NextCursor: next})
}
func (a *app) createDetectionJob(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	var input struct {
		DraftID    string `json:"draft_id"`
		TargetKind string `json:"target_kind"`
		TargetID   string `json:"target_id"`
		APIKey     string `json:"api_key"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	if input.DraftID != "" && !uuidPattern.MatchString(input.DraftID) || !uuidPattern.MatchString(input.TargetID) || (input.TargetKind != "group" && input.TargetKind != "group_model") {
		writeDetectionError(w, detection.ErrInvalid)
		return
	}
	actor := detectionActor(r)
	if !a.detectionStarts.take(actor.ID) {
		writeDetectionError(w, detection.ErrBusy)
		return
	}
	job, err := a.detection.Start(r.Context(), actor, detection.Target{DraftID: input.DraftID, Binding: market.EvidenceBinding{TargetKind: input.TargetKind, TargetID: input.TargetID}}, input.APIKey)
	input.APIKey = ""
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	if input.DraftID != "" && a.contributions != nil {
		if err := a.contributions.TrackTask(r.Context(), contributionActor(r), job); err != nil {
			// A durable task remains discoverable even if concurrent editing prevented
			// attaching it. Do not claim that an already running task was rolled back.
			writeJSON(w, 202, map[string]any{"job": job, "draft_linked": false})
			return
		}
	}
	writeJSON(w, 202, map[string]any{"job": job})
}
func (a *app) getDetectionJob(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	job, err := a.detection.Get(r.Context(), detectionActor(r), id)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": job})
}
func (a *app) cancelDetectionJob(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	job, err := a.detection.Cancel(r.Context(), detectionActor(r), id)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": job})
}
func (a *app) resumeDetectionJob(w http.ResponseWriter, r *http.Request) {
	a.rerunDetection(w, r, false)
}
func (a *app) retestDetectionJob(w http.ResponseWriter, r *http.Request) {
	a.rerunDetection(w, r, true)
}
func (a *app) rerunDetection(w http.ResponseWriter, r *http.Request, partial bool) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	var input struct {
		APIKey    string   `json:"api_key"`
		Scenarios []string `json:"scenarios"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	if !partial && len(input.Scenarios) > 0 {
		writeDetectionError(w, detection.ErrInvalid)
		return
	}
	actor := detectionActor(r)
	if !a.detectionStarts.take(actor.ID) {
		writeDetectionError(w, detection.ErrBusy)
		return
	}
	var job detection.Job
	var err error
	if partial {
		job, err = a.detection.Retest(r.Context(), actor, id, input.APIKey, input.Scenarios)
	} else {
		job, err = a.detection.Resume(r.Context(), actor, id, input.APIKey)
	}
	input.APIKey = ""
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"job": job})
}
func (a *app) detectionAttempts(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	limit, ok := pageLimit(r)
	cursor, cursorOK := detectionCursor(r)
	if !ok || !cursorOK {
		writeBadCursor(w)
		return
	}
	items, err := a.detection.AttemptPage(r.Context(), detectionActor(r), id, cursor, limit+1)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next *string
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next = nextDetectionCursor(last.At, last.ID)
	}
	writeJSON(w, 200, pageJSON[detection.Attempt]{Items: items, NextCursor: next})
}
func (a *app) detectionArtifacts(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	items, err := a.detection.Artifacts(r.Context(), detectionActor(r), id)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (a *app) getDetectionParameters(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	value, err := a.detection.Parameters(r.Context())
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"parameters": value})
}
func (a *app) setDetectionSearch(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	var value gatewaycontract.SearchSettings
	if decodeJSON(w, r, &value) != nil {
		writeInvalidJSON(w)
		return
	}
	updated, err := a.detection.SetSearch(r.Context(), detectionActor(r), r.PathValue("target"), value)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"settings": updated})
}

func (a *app) getDetectionReport(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "reportID")
	if !ok {
		return
	}
	job, err := a.detection.GetReport(r.Context(), detectionActor(r), id)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": job})
}
func (a *app) detectionReports(w http.ResponseWriter, r *http.Request) {
	if !a.detectionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	limit, ok := pageLimit(r)
	cursor, cursorOK := detectionCursor(r)
	if !ok || !cursorOK {
		writeBadCursor(w)
		return
	}
	items, err := a.detection.ReportPage(r.Context(), detectionActor(r), id, cursor, limit+1)
	if err != nil {
		writeDetectionError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next *string
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next = nextDetectionCursor(last.CreatedAt, last.ID)
	}
	writeJSON(w, 200, pageJSON[detection.ReportSummary]{Items: items, NextCursor: next})
}
