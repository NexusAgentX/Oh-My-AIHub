package api

import (
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"io"
	"mime"
	"net/http"
	"strconv"
)

func (a *app) registerForumRoutes(r *router) {
	r.implement("239", "GET /api/forum/boards", accessReady, a.forumBoards)
	r.implement("239", "POST /api/forum/boards", accessAdmin, a.forumSaveBoard)
	r.implement("239", "PATCH /api/forum/boards/{id}", accessAdmin, a.forumSaveBoard)
	r.implement("239", "DELETE /api/forum/boards/{id}", accessAdmin, a.forumDeleteBoard)
	r.implement("239", "GET /api/forum/topics", accessReady, a.forumTopics)
	r.implement("239", "POST /api/forum/topics", accessReady, a.forumCreateTopic)
	r.implement("239", "GET /api/forum/topics/{id}", accessReady, a.forumTopic)
	r.implement("239", "PATCH /api/forum/topics/{id}", accessReady, a.forumEditTopic)
	r.implement("239", "DELETE /api/forum/topics/{id}", accessReady, a.forumDeleteTopic)
	r.implement("239", "PATCH /api/forum/topics/{id}/status", accessReady, a.forumSetStatus)
	r.implement("239", "GET /api/forum/topics/{id}/replies", accessReady, a.forumReplies)
	r.implement("239", "POST /api/forum/topics/{id}/replies", accessReady, a.forumSaveReply)
	r.implement("239", "PATCH /api/forum/replies/{id}", accessReady, a.forumSaveReply)
	r.implement("239", "DELETE /api/forum/replies/{id}", accessReady, a.forumDeleteReply)
	r.implement("239", "POST /api/forum/attachments", accessReady, a.forumUpload)
	r.implement("239", "GET /api/forum/attachments/{id}", accessReady, a.forumAttachment)
}
func forumActor(r *http.Request) forum.Actor {
	a := accountFromContext(r.Context())
	return forum.Actor{ID: a.ID, Admin: a.IsAdmin}
}
func writeForumError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, forum.ErrNotFound):
		writeError(w, 404, "not_found", "资源不存在")
	case errors.Is(err, forum.ErrForbidden):
		writeError(w, 403, "forbidden", "没有执行该操作的权限")
	case errors.Is(err, forum.ErrInvalidInput):
		writeError(w, 422, "invalid_input", "请检查内容、附件格式和归属；单文件不超过 10 MiB，每篇最多 20 个附件")
	case errors.Is(err, forum.ErrConflict):
		writeError(w, 409, "conflict", "资源状态冲突；非空板块不能删除，已关闭工单需先重新打开")
	case errors.Is(err, forum.ErrQuota):
		writeError(w, 422, "attachment_quota_exceeded", "附件总量不能超过 100 MiB")
	default:
		writeDomainError(w, err)
	}
}
func forumJSON(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		writeForumError(w, err)
		return
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, v)
}
func forumFilter(r *http.Request) (forum.Filter, error) {
	limit, ok := pageLimit(r)
	if !ok {
		return forum.Filter{}, forum.ErrInvalidInput
	}
	page := 1
	var err error
	if v := r.URL.Query().Get("page"); v != "" {
		page, err = strconv.Atoi(v)
	}
	if err != nil || page < 1 || page > 100000 {
		return forum.Filter{}, forum.ErrInvalidInput
	}
	f := forum.Filter{Page: int32(page), Limit: int32(limit), Kind: r.URL.Query().Get("kind"), Search: r.URL.Query().Get("q")}
	if f.Kind == "" {
		f.Kind = "discussion"
	}
	if v := r.URL.Query().Get("board_id"); v != "" {
		f.BoardID = &v
	}
	return f, forum.ValidateFilter(f)
}
func (a *app) forumBoards(w http.ResponseWriter, r *http.Request) {
	v, e := a.forum.Boards(r.Context())
	forumJSON(w, 200, map[string]any{"items": v}, e)
}
func (a *app) forumSaveBoard(w http.ResponseWriter, r *http.Request) {
	var in forum.BoardInput
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.SaveBoard(r.Context(), forumActor(r), r.PathValue("id"), in)
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	forumJSON(w, status, v, e)
}
func (a *app) forumDeleteBoard(w http.ResponseWriter, r *http.Request) {
	forumJSON(w, 204, nil, a.forum.DeleteBoard(r.Context(), forumActor(r), r.PathValue("id")))
}
func (a *app) forumTopics(w http.ResponseWriter, r *http.Request) {
	f, e := forumFilter(r)
	if e != nil {
		writeForumError(w, e)
		return
	}
	v, e := a.forum.Topics(r.Context(), forumActor(r), f)
	forumJSON(w, 200, v, e)
}
func (a *app) forumTopic(w http.ResponseWriter, r *http.Request) {
	v, e := a.forum.Topic(r.Context(), forumActor(r), r.PathValue("id"))
	forumJSON(w, 200, v, e)
}
func (a *app) forumCreateTopic(w http.ResponseWriter, r *http.Request) {
	var in forum.TopicInput
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.CreateTopic(r.Context(), forumActor(r), in)
	forumJSON(w, 201, v, e)
}
func (a *app) forumEditTopic(w http.ResponseWriter, r *http.Request) {
	var in forum.TopicEdit
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.EditTopic(r.Context(), forumActor(r), r.PathValue("id"), in)
	forumJSON(w, 200, v, e)
}
func (a *app) forumDeleteTopic(w http.ResponseWriter, r *http.Request) {
	forumJSON(w, 204, nil, a.forum.DeleteTopic(r.Context(), forumActor(r), r.PathValue("id")))
}
func (a *app) forumSetStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.SetStatus(r.Context(), forumActor(r), r.PathValue("id"), in.Status)
	forumJSON(w, 200, v, e)
}
func (a *app) forumReplies(w http.ResponseWriter, r *http.Request) {
	f, e := forumFilter(r)
	if e != nil {
		writeForumError(w, e)
		return
	}
	v, e := a.forum.Replies(r.Context(), forumActor(r), r.PathValue("id"), f)
	forumJSON(w, 200, v, e)
}
func (a *app) forumSaveReply(w http.ResponseWriter, r *http.Request) {
	var in forum.ContentInput
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	topicID, id, status := "", r.PathValue("id"), 200
	if r.Method == "POST" {
		topicID, id, status = id, "", 201
	}
	v, e := a.forum.SaveReply(r.Context(), forumActor(r), topicID, id, in)
	forumJSON(w, status, v, e)
}
func (a *app) forumDeleteReply(w http.ResponseWriter, r *http.Request) {
	forumJSON(w, 204, nil, a.forum.DeleteReply(r.Context(), forumActor(r), r.PathValue("id")))
}
func (a *app) forumUpload(w http.ResponseWriter, r *http.Request) {
	if !a.forumUploads.take(forumActor(r).ID) {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "rate_limited", "上传过于频繁，请稍后重试")
		return
	}
	select {
	case a.forumUploadSlots <- struct{}{}:
		defer func() { <-a.forumUploadSlots }()
	default:
		w.Header().Set("Retry-After", "2")
		writeError(w, 429, "upload_busy", "上传繁忙，请稍后重试")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		writeInvalidJSON(w)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeInvalidJSON(w)
		return
	}
	name := part.FileName()
	data, err := io.ReadAll(io.LimitReader(part, forum.MaxFileSize+1))
	part.Close()
	if err != nil || len(data) > forum.MaxFileSize {
		writeError(w, 413, "file_too_large", "单文件不能超过 10 MiB")
		return
	}
	if _, err = reader.NextPart(); err != io.EOF {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.Upload(r.Context(), forumActor(r), name, data)
	forumJSON(w, 201, v, e)
}
func (a *app) forumAttachment(w http.ResponseWriter, r *http.Request) {
	f, err := a.forum.Attachment(r.Context(), forumActor(r), r.PathValue("id"))
	if err != nil {
		writeForumError(w, err)
		return
	}
	disposition := "attachment"
	if f.Inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", f.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Length", strconv.Itoa(len(f.Data)))
	w.WriteHeader(200)
	_, _ = w.Write(f.Data)
}
