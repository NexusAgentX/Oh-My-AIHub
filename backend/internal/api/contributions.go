package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func (a *app) registerContributionRoutes(r *router) {
	bounded := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			next(w, r.WithContext(ctx))
		}
	}
	r.implement("492", "GET /api/contributions/targets", accessReady, bounded(a.contributionTargets))
	r.implement("492", "GET /api/contributions", accessReady, bounded(a.listContributions))
	r.implement("492", "POST /api/contributions", accessReady, bounded(a.newContribution))
	r.implement("492", "GET /api/contributions/{draftID}", accessReady, bounded(a.getContribution))
	r.implement("492", "PUT /api/contributions/{draftID}", accessReady, bounded(a.saveContribution))
	r.implement("492", "POST /api/contributions/{draftID}/children", accessReady, bounded(a.addContributionChild))
	r.implement("492", "POST /api/contributions/{draftID}/submit", accessReady, bounded(a.submitContribution))
	r.implement("492", "POST /api/contributions/{draftID}/withdraw", accessReady, bounded(a.withdrawContribution))
	r.implement("492", "GET /api/admin/contributions", accessAdmin, bounded(a.reviewContributions))
	r.implement("492", "POST /api/admin/contributions/{draftID}/approve", accessAdmin, bounded(a.approveContribution))
	r.implement("492", "POST /api/admin/contributions/{draftID}/reject", accessAdmin, bounded(a.rejectContribution))
	r.implement("492", "POST /api/contributions/icon", accessReady, bounded(a.fetchSupplierIcon))
}
func contributionActor(r *http.Request) contribution.Actor {
	actor := accountFromContext(r.Context())
	return contribution.Actor{ID: actor.ID, Admin: actor.IsAdmin}
}
func (a *app) contributionReady(w http.ResponseWriter) bool {
	if a.contributions == nil {
		writeError(w, 503, "contributions_unavailable", "贡献服务暂不可用")
		return false
	}
	return true
}
func writeContributionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contribution.ErrNotFound):
		writeError(w, 404, "not_found", "资源不存在")
	case errors.Is(err, contribution.ErrConflict), errors.Is(err, market.ErrConflict):
		writeError(w, 409, "contribution_conflict", "修订或生效版本已变化，请重新载入")
	case errors.Is(err, market.ErrHasGroups):
		writeError(w, 409, "supplier_has_groups", "供应商仍有分组，请先逐个处理并删除分组")
	case errors.Is(err, contribution.ErrReports):
		writeError(w, 409, "reports_required", "需要当前草稿的完整检测报告；套件待实现或未执行完时不能提交")
	case errors.Is(err, contribution.ErrInvalid), errors.Is(err, market.ErrInvalid):
		writeError(w, 422, "invalid_contribution", "贡献内容不完整或格式无效，请检查价格及必填信息")
	default:
		writeDomainError(w, err)
	}
}
func (a *app) listContributions(w http.ResponseWriter, r *http.Request) {
	a.contributionList(w, r, false)
}
func (a *app) reviewContributions(w http.ResponseWriter, r *http.Request) {
	a.contributionList(w, r, true)
}
func (a *app) contributionList(w http.ResponseWriter, r *http.Request, queue bool) {
	if !a.contributionReady(w) {
		return
	}
	limit, ok := pageLimit(r)
	after, valid := detectionCursor(r)
	if !ok || !valid {
		writeBadCursor(w)
		return
	}
	var cursor *contribution.Cursor
	if after != nil {
		cursor = &contribution.Cursor{At: after.At, ID: after.ID}
	}
	items, err := a.contributions.List(r.Context(), contributionActor(r), queue, cursor, limit+1)
	if err != nil {
		writeContributionError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	if items == nil {
		items = []contribution.Summary{}
	}
	var next *string
	if more {
		v := items[len(items)-1]
		next = nextDetectionCursor(v.CreatedAt, v.ID)
	}
	writeJSON(w, 200, pageJSON[contribution.Summary]{Items: items, NextCursor: next})
}
func (a *app) newContribution(w http.ResponseWriter, r *http.Request) {
	if !a.contributionReady(w) {
		return
	}
	var input struct {
		Kind      string `json:"kind"`
		TargetID  string `json:"target_id"`
		ParentID  string `json:"parent_id"`
		Action    string `json:"action"`
		Starter   bool   `json:"starter"`
		ModelType string `json:"model_type"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	if input.Kind != "model_id" && input.TargetID != "" && !uuidPattern.MatchString(input.TargetID) || input.ParentID != "" && !uuidPattern.MatchString(input.ParentID) {
		writeContributionError(w, contribution.ErrInvalid)
		return
	}
	d, err := a.contributions.New(r.Context(), contributionActor(r), contribution.NewInput{Kind: input.Kind, TargetID: input.TargetID, ParentID: input.ParentID, Action: input.Action, Starter: input.Starter, ModelType: input.ModelType})
	if err != nil {
		writeContributionError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"draft": draftJSON(d)})
}
func (a *app) getContribution(w http.ResponseWriter, r *http.Request) {
	if !a.contributionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	detail, err := a.contributions.Detail(r.Context(), contributionActor(r), id)
	if err != nil {
		writeContributionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft": draftJSON(detail.Draft), "evaluation": detail.Evaluation, "stage": detail.Stage, "linked_channels": detail.LinkedChannels})
}
func (a *app) saveContribution(w http.ResponseWriter, r *http.Request) {
	if !a.contributionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input struct {
		Revision  int64               `json:"revision"`
		Title     string              `json:"title"`
		Changes   contributionChanges `json:"changes"`
		ReportIDs []string            `json:"report_ids"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	for _, v := range input.ReportIDs {
		if !uuidPattern.MatchString(v) {
			writeContributionError(w, contribution.ErrInvalid)
			return
		}
	}
	changes, declared, err := input.Changes.domain()
	if err != nil {
		writeContributionError(w, err)
		return
	}
	d, err := a.contributions.Save(r.Context(), contributionActor(r), id, input.Revision, input.Title, changes, input.ReportIDs, declared, input.Changes.priceBlanks())
	if err != nil {
		writeContributionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft": draftJSON(d)})
}
func (a *app) addContributionChild(w http.ResponseWriter, r *http.Request) {
	if !a.contributionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input struct {
		Revision  int64  `json:"revision"`
		Kind      string `json:"kind"`
		ParentID  string `json:"parent_id"`
		ModelType string `json:"model_type"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	if !uuidPattern.MatchString(input.ParentID) {
		writeContributionError(w, contribution.ErrInvalid)
		return
	}
	d, err := a.contributions.AddChild(r.Context(), contributionActor(r), id, input.Revision, input.Kind, input.ParentID, input.ModelType)
	if err != nil {
		writeContributionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft": draftJSON(d)})
}
func (a *app) submitContribution(w http.ResponseWriter, r *http.Request) {
	a.changeContributionState(w, r, "submit")
}
func (a *app) withdrawContribution(w http.ResponseWriter, r *http.Request) {
	a.changeContributionState(w, r, "withdraw")
}
func (a *app) approveContribution(w http.ResponseWriter, r *http.Request) {
	a.changeContributionState(w, r, "approve")
}
func (a *app) rejectContribution(w http.ResponseWriter, r *http.Request) {
	a.changeContributionState(w, r, "reject")
}
func (a *app) changeContributionState(w http.ResponseWriter, r *http.Request, action string) {
	if !a.contributionReady(w) {
		return
	}
	id, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input struct {
		Revision int64  `json:"revision"`
		Reason   string `json:"reason"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	actor := contributionActor(r)
	var d contribution.Draft
	var err error
	switch action {
	case "submit":
		d, err = a.contributions.Submit(r.Context(), actor, id, input.Revision)
	case "withdraw":
		d, err = a.contributions.Withdraw(r.Context(), actor, id, input.Revision)
	case "approve":
		d, err = a.contributions.Approve(r.Context(), actor, id, input.Revision, input.Reason)
	case "reject":
		d, err = a.contributions.Reject(r.Context(), actor, id, input.Revision, input.Reason)
	}
	if errors.Is(err, contribution.ErrPublication) || errors.Is(err, contribution.ErrCommitUncertain) {
		code := "publication_pending"
		message := "数据已发布，快照刷新尚未完成，请重试批准"
		if errors.Is(err, contribution.ErrCommitUncertain) {
			code = "publication_unknown"
			message = "发布提交结果暂未确认，请重新载入后重试"
		}
		writeJSON(w, 503, map[string]any{"error": code, "message": message, "draft": draftJSON(d)})
		return
	}
	if err != nil {
		writeContributionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"draft": draftJSON(d)})
}

func (a *app) contributionTargets(w http.ResponseWriter, r *http.Request) {
	if !a.contributionReady(w) {
		return
	}
	limit, ok := pageLimit(r)
	cursor := r.URL.Query().Get("cursor")
	after := ""
	if cursor != "" {
		var valid bool
		after, valid = decodeTextCursor(cursor)
		if !valid || !uuidPattern.MatchString(after) {
			writeBadCursor(w)
			return
		}
	}
	if !ok {
		writeBadCursor(w)
		return
	}
	items, more, err := a.contributions.Choices(r.Context(), contributionActor(r), r.URL.Query().Get("kind"), after, limit)
	if err != nil {
		writeContributionError(w, err)
		return
	}
	var next *string
	if more {
		v := encodeTextCursor(items[len(items)-1].ID)
		next = &v
	}
	writeJSON(w, 200, pageJSON[contribution.TargetChoice]{Items: items, NextCursor: next})
}
