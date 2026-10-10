package api

import (
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
)

// forumAuthorJSON is the OpenAPI ForumAuthor schema.
type forumAuthorJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// forumBoardJSON is the OpenAPI ForumBoard schema.
type forumBoardJSON struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int32     `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

// forumBoardListJSON is the OpenAPI ForumBoardList schema.
type forumBoardListJSON struct {
	Items []forumBoardJSON `json:"items"`
}

// forumAttachmentJSON is the OpenAPI ForumAttachment schema.
type forumAttachmentJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
	Inline    bool   `json:"inline"`
}

// forumTopicJSON is the OpenAPI ForumTopic schema.
type forumTopicJSON struct {
	ID          string                `json:"id"`
	Kind        string                `json:"kind"`
	BoardID     *string               `json:"board_id"`
	Title       string                `json:"title"`
	Body        string                `json:"body"`
	Author      forumAuthorJSON       `json:"author"`
	Status      *string               `json:"status"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	Attachments []forumAttachmentJSON `json:"attachments"`
	ReplyCount  int64                 `json:"reply_count"`
}

// forumReplyJSON is the OpenAPI ForumReply schema.
type forumReplyJSON struct {
	ID          string                `json:"id"`
	TopicID     string                `json:"topic_id"`
	Body        string                `json:"body"`
	Author      forumAuthorJSON       `json:"author"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	Attachments []forumAttachmentJSON `json:"attachments"`
}

// forumTopicPageJSON is the OpenAPI ForumTopicPage schema.
type forumTopicPageJSON struct {
	Items []forumTopicJSON `json:"items"`
	Total int64            `json:"total"`
	Page  int32            `json:"page"`
	Limit int32            `json:"limit"`
}

// forumReplyPageJSON is the OpenAPI ForumReplyPage schema.
type forumReplyPageJSON struct {
	Items []forumReplyJSON `json:"items"`
	Total int64            `json:"total"`
	Page  int32            `json:"page"`
	Limit int32            `json:"limit"`
}

func newForumBoardJSON(board forum.Board) forumBoardJSON {
	return forumBoardJSON{ID: board.ID, Name: board.Name, Description: board.Description, SortOrder: board.SortOrder, CreatedAt: board.CreatedAt}
}

func newForumBoardListJSON(boards []forum.Board) forumBoardListJSON {
	items := make([]forumBoardJSON, 0, len(boards))
	for _, board := range boards {
		items = append(items, newForumBoardJSON(board))
	}
	return forumBoardListJSON{Items: items}
}

func newForumAttachmentJSON(attachment forum.Attachment) forumAttachmentJSON {
	return forumAttachmentJSON{
		ID: attachment.ID, Name: attachment.Name, MediaType: attachment.MediaType, Size: attachment.Size, URL: attachment.URL, Inline: attachment.Inline,
	}
}

func newForumAttachmentsJSON(attachments []forum.Attachment) []forumAttachmentJSON {
	items := make([]forumAttachmentJSON, 0, len(attachments))
	for _, attachment := range attachments {
		items = append(items, newForumAttachmentJSON(attachment))
	}
	return items
}

func newForumTopicJSON(topic forum.Topic) forumTopicJSON {
	return forumTopicJSON{
		ID:          topic.ID,
		Kind:        topic.Kind,
		BoardID:     topic.BoardID,
		Title:       topic.Title,
		Body:        topic.Body,
		Author:      forumAuthorJSON{ID: topic.Author.ID, DisplayName: topic.Author.DisplayName},
		Status:      topic.Status,
		CreatedAt:   topic.CreatedAt,
		UpdatedAt:   topic.UpdatedAt,
		Attachments: newForumAttachmentsJSON(topic.Attachments),
		ReplyCount:  topic.ReplyCount,
	}
}

func newForumReplyJSON(reply forum.Reply) forumReplyJSON {
	return forumReplyJSON{
		ID:          reply.ID,
		TopicID:     reply.TopicID,
		Body:        reply.Body,
		Author:      forumAuthorJSON{ID: reply.Author.ID, DisplayName: reply.Author.DisplayName},
		CreatedAt:   reply.CreatedAt,
		UpdatedAt:   reply.UpdatedAt,
		Attachments: newForumAttachmentsJSON(reply.Attachments),
	}
}

func newForumTopicPageJSON(page forum.Page[forum.Topic]) forumTopicPageJSON {
	items := make([]forumTopicJSON, 0, len(page.Items))
	for _, topic := range page.Items {
		items = append(items, newForumTopicJSON(topic))
	}
	return forumTopicPageJSON{Items: items, Total: page.Total, Page: page.Page, Limit: page.Limit}
}

func newForumReplyPageJSON(page forum.Page[forum.Reply]) forumReplyPageJSON {
	items := make([]forumReplyJSON, 0, len(page.Items))
	for _, reply := range page.Items {
		items = append(items, newForumReplyJSON(reply))
	}
	return forumReplyPageJSON{Items: items, Total: page.Total, Page: page.Page, Limit: page.Limit}
}

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

// writeForum writes the response form of a forum call's result, or the error the call failed with.
func writeForum[T, J any](w http.ResponseWriter, status int, value T, err error, convert func(T) J) {
	if err != nil {
		writeForumError(w, err)
		return
	}
	writeJSON(w, status, convert(value))
}

// writeForumDeleted answers a successful delete with 204, or with the error the call failed with.
func writeForumDeleted(w http.ResponseWriter, err error) {
	if err != nil {
		writeForumError(w, err)
		return
	}
	w.WriteHeader(204)
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
	writeForum(w, 200, v, e, newForumBoardListJSON)
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
	writeForum(w, status, v, e, newForumBoardJSON)
}
func (a *app) forumDeleteBoard(w http.ResponseWriter, r *http.Request) {
	writeForumDeleted(w, a.forum.DeleteBoard(r.Context(), forumActor(r), r.PathValue("id")))
}
func (a *app) forumTopics(w http.ResponseWriter, r *http.Request) {
	f, e := forumFilter(r)
	if e != nil {
		writeForumError(w, e)
		return
	}
	v, e := a.forum.Topics(r.Context(), forumActor(r), f)
	writeForum(w, 200, v, e, newForumTopicPageJSON)
}
func (a *app) forumTopic(w http.ResponseWriter, r *http.Request) {
	v, e := a.forum.Topic(r.Context(), forumActor(r), r.PathValue("id"))
	writeForum(w, 200, v, e, newForumTopicJSON)
}
func (a *app) forumCreateTopic(w http.ResponseWriter, r *http.Request) {
	var in forum.TopicInput
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.CreateTopic(r.Context(), forumActor(r), in)
	writeForum(w, 201, v, e, newForumTopicJSON)
}
func (a *app) forumEditTopic(w http.ResponseWriter, r *http.Request) {
	var in forum.TopicEdit
	if decodeJSON(w, r, &in) != nil {
		writeInvalidJSON(w)
		return
	}
	v, e := a.forum.EditTopic(r.Context(), forumActor(r), r.PathValue("id"), in)
	writeForum(w, 200, v, e, newForumTopicJSON)
}
func (a *app) forumDeleteTopic(w http.ResponseWriter, r *http.Request) {
	writeForumDeleted(w, a.forum.DeleteTopic(r.Context(), forumActor(r), r.PathValue("id")))
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
	writeForum(w, 200, v, e, newForumTopicJSON)
}
func (a *app) forumReplies(w http.ResponseWriter, r *http.Request) {
	f, e := forumFilter(r)
	if e != nil {
		writeForumError(w, e)
		return
	}
	v, e := a.forum.Replies(r.Context(), forumActor(r), r.PathValue("id"), f)
	writeForum(w, 200, v, e, newForumReplyPageJSON)
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
	writeForum(w, status, v, e, newForumReplyJSON)
}
func (a *app) forumDeleteReply(w http.ResponseWriter, r *http.Request) {
	writeForumDeleted(w, a.forum.DeleteReply(r.Context(), forumActor(r), r.PathValue("id")))
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
	writeForum(w, 201, v, e, newForumAttachmentJSON)
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
