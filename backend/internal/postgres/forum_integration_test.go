package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
)

func forumContent(body string, ids ...string) forum.ContentInput {
	if ids == nil {
		ids = []string{}
	}
	return forum.ContentInput{Body: body, AttachmentIDs: ids}
}
func TestForumContentPermissionsAndLifecycle(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, admin, members := accounts(t, store, "100", "100")
	s := forum.NewService(store.Forum)
	a := forum.Actor{ID: admin.ID, Admin: true}
	owner := forum.Actor{ID: members[0].ID}
	other := forum.Actor{ID: members[1].ID}
	b, err := s.SaveBoard(ctx, a, "", forum.BoardInput{Name: "技术交流"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveBoard(ctx, owner, "", forum.BoardInput{Name: "member"}); !errors.Is(err, forum.ErrForbidden) {
		t.Fatal(err)
	}
	b, err = s.SaveBoard(ctx, a, b.ID, forum.BoardInput{Name: "工具分享", Description: "工具", SortOrder: 2})
	if err != nil || b.Name != "工具分享" {
		t.Fatal(b, err)
	}
	publicFile, err := s.Upload(ctx, owner, "readme.txt", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, other, publicFile.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatalf("temporary visibility %v", err)
	}
	if _, err = s.Attachment(ctx, a, publicFile.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatalf("admin temporary visibility %v", err)
	}
	topic, err := s.CreateTopic(ctx, owner, forum.TopicInput{Kind: "discussion", BoardID: &b.ID, Title: "工具体验", ContentInput: forumContent("介绍", publicFile.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBoard(ctx, a, b.ID); !errors.Is(err, forum.ErrConflict) {
		t.Fatalf("nonempty board %v", err)
	}
	if _, err = s.Attachment(ctx, other, publicFile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditTopic(ctx, other, topic.ID, forum.TopicEdit{Title: "更改", ContentInput: forumContent("other")}); !errors.Is(err, forum.ErrForbidden) {
		t.Fatal(err)
	}
	replyFile, err := s.Upload(ctx, other, "reply.txt", []byte("reply"))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := s.SaveReply(ctx, other, topic.ID, "", forumContent("回复", replyFile.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditTopic(ctx, owner, topic.ID, forum.TopicEdit{Title: "工具体验", ContentInput: forumContent("介绍", replyFile.ID)}); !errors.Is(err, forum.ErrInvalidInput) {
		t.Fatalf("cross content binding %v", err)
	}
	if _, err = s.Topic(ctx, owner, topic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveReply(ctx, other, "", reply.ID, forumContent("修改后的回复", replyFile.ID)); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteReply(ctx, owner, reply.ID); !errors.Is(err, forum.ErrForbidden) {
		t.Fatal(err)
	}
	if err = s.DeleteReply(ctx, a, reply.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, other, replyFile.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatal(err)
	}
	page, err := s.Topics(ctx, other, forum.Filter{Kind: "discussion", BoardID: &b.ID, Search: "工具", Page: 1, Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	page, err = s.Topics(ctx, other, forum.Filter{Kind: "discussion", Page: 2, Limit: 1})
	if err != nil || len(page.Items) != 0 || page.Total != 1 {
		t.Fatal(page, err)
	}
	privateFile, err := s.Upload(ctx, owner, "ticket.txt", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.CreateTopic(ctx, owner, forum.TopicInput{Kind: "ticket", Title: "需要帮助", ContentInput: forumContent("工单", privateFile.ID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, get := range []func() error{func() error { _, e := s.Topic(ctx, other, ticket.ID); return e }, func() error { _, e := s.Replies(ctx, other, ticket.ID, forum.Filter{Page: 1, Limit: 20}); return e }, func() error { _, e := s.Attachment(ctx, other, privateFile.ID); return e }, func() error { _, e := s.SaveReply(ctx, other, ticket.ID, "", forumContent("hello")); return e }} {
		if e := get(); !errors.Is(e, forum.ErrNotFound) {
			t.Fatalf("private resource %v", e)
		}
	}
	page, err = s.Topics(ctx, other, forum.Filter{Kind: "ticket", Page: 1, Limit: 20})
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
	for _, actor := range []forum.Actor{owner, a} {
		if _, err = s.Attachment(ctx, actor, privateFile.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SetStatus(ctx, owner, ticket.ID, "resolved"); !errors.Is(err, forum.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, a, ticket.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, owner, ticket.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveReply(ctx, a, ticket.ID, "", forumContent("answer")); !errors.Is(err, forum.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, owner, ticket.ID, "pending"); err != nil {
		t.Fatal(err)
	}
	adminFile, err := s.Upload(ctx, a, "answer.txt", []byte("answer"))
	if err != nil {
		t.Fatal(err)
	}
	answer, err := s.SaveReply(ctx, a, ticket.ID, "", forumContent("answer", adminFile.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, owner, adminFile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, other, adminFile.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatal(err)
	}
	replies, err := s.Replies(ctx, owner, ticket.ID, forum.Filter{Page: 1, Limit: 20})
	if err != nil || replies.Total != 1 || replies.Items[0].ID != answer.ID {
		t.Fatal(replies, err)
	}
	if _, err = s.EditTopic(ctx, a, ticket.ID, forum.TopicEdit{Title: "已处理", ContentInput: forumContent("工单", privateFile.ID)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditTopic(ctx, owner, topic.ID, forum.TopicEdit{Title: "工具体验", ContentInput: forumContent("更新")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, owner, publicFile.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.DeleteTopic(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{privateFile.ID, adminFile.ID} {
		if _, err = s.Attachment(ctx, owner, id); !errors.Is(err, forum.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if err = s.DeleteTopic(ctx, a, topic.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBoard(ctx, a, b.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE 'forum.%'`).Scan(&count); err != nil || count < 8 {
		t.Fatalf("audit count %d %v", count, err)
	}
}
func TestForumAttachmentQuotaBindingAndCleanup(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, admin, members := accounts(t, store, "100")
	s := forum.NewService(store.Forum)
	owner := forum.Actor{ID: members[0].ID}
	a := forum.Actor{ID: admin.ID, Admin: true}
	first, err := s.Upload(ctx, owner, "old.txt", []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE forum_attachments SET created_at=now()-interval '25 hours' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attachment(ctx, owner, first.ID); !errors.Is(err, forum.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.CreateTopic(ctx, owner, forum.TopicInput{Kind: "ticket", Title: "old", ContentInput: forumContent("old", first.ID)}); !errors.Is(err, forum.ErrInvalidInput) {
		t.Fatal(err)
	}
	n, err := s.Cleanup(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	otherFile, err := s.Upload(ctx, a, "admin.txt", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTopic(ctx, owner, forum.TopicInput{Kind: "ticket", Title: "ownership", ContentInput: forumContent("body", otherFile.ID)}); !errors.Is(err, forum.ErrInvalidInput) {
		t.Fatal(err)
	}
	shared, err := s.Upload(ctx, owner, "one.txt", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.CreateTopic(ctx, owner, forum.TopicInput{Kind: "ticket", Title: "atomic", ContentInput: forumContent("body", shared.ID)})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, forum.ErrInvalidInput) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("binding commits=%d", success)
	}
	// Seed 90 MiB via the real upload path; simultaneous 10 MiB uploads share
	// the same account-row quota lock, so only one can commit.
	data := bytes.Repeat([]byte{'a'}, forum.MaxFileSize)
	if _, err = pool.Exec(ctx, `DELETE FROM forum_topics WHERE author_id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if _, err = s.Upload(ctx, owner, "quota.bin", data); err != nil {
			t.Fatal(err)
		}
	}
	errs = make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Upload(ctx, owner, "last.bin", data); errs <- e }()
	}
	wg.Wait()
	close(errs)
	success = 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, forum.ErrQuota) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("quota commits=%d", success)
	}
}
func TestForumHTTPContractAndGates(t *testing.T) {
	_, store := isolatedDatabase(t)
	ctx := context.Background()
	identity, admin, members := accounts(t, store, "100")
	handler := api.NewHandler(api.Dependencies{Identity: identity, Forum: forum.NewService(store.Forum)})
	login, err := identity.Login(ctx, "founder", "Founder-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	token := login.SessionToken
	call := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, "http://hub.example"+path, reader)
		req.Header.Set("Origin", "http://hub.example")
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "oma_session", Value: token})
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		assertContract(t, method, path, rec)
		return rec
	}
	rec := call("POST", "/api/forum/boards", forum.BoardInput{Name: "公告"}, 201)
	var b struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &b)
	call("GET", "/api/forum/boards", nil, 200)
	rec = call("POST", "/api/forum/topics", forum.TopicInput{Kind: "discussion", BoardID: &b.ID, Title: "公告", ContentInput: forumContent("Markdown")}, 201)
	var topic struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &topic)
	call("GET", "/api/forum/topics?kind=discussion&page=1&limit=20", nil, 200)
	call("GET", "/api/forum/topics/"+topic.ID, nil, 200)
	call("GET", "/api/forum/topics/"+topic.ID+"/replies", nil, 200)
	call("PATCH", "/api/forum/topics/"+topic.ID, map[string]any{"title": "missing attachments", "body": "body"}, 422)
	call("POST", "/api/forum/topics/"+topic.ID+"/replies", forumContent("reply"), 201)
	// Multipart file contract and forced download headers.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "note.txt")
	part.Write([]byte("attachment"))
	mw.Close()
	req := httptest.NewRequest("POST", "http://hub.example/api/forum/attachments", &buf)
	req.Header.Set("Origin", "http://hub.example")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "oma_session", Value: token})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	assertContract(t, "POST", "/api/forum/attachments", rec)
	var att struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &att)
	req = httptest.NewRequest("GET", "http://hub.example"+att.URL, nil)
	req.AddCookie(&http.Cookie{Name: "oma_session", Value: token})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "attachment" || rec.Header().Get("Content-Disposition") != "attachment; filename=note.txt" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Header(), rec.Body.String())
	}
	token = ""
	call("GET", "/api/forum/boards", nil, 401)
	call("GET", att.URL, nil, 401)
	password := mustInitialPassword(t, identity, admin, members[0].ID)
	memberLogin, err := identity.Login(ctx, members[0].Username, password)
	if err != nil {
		t.Fatal(err)
	}
	token = memberLogin.SessionToken
	call("GET", "/api/forum/boards", nil, 403)
	ready, err := identity.ChangePassword(ctx, members[0].ID, password, "Forum-new-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	token = ready.SessionToken
	call("GET", "/api/forum/boards", nil, 200)
	call("POST", "/api/forum/boards", forum.BoardInput{Name: "forbidden"}, 403)
	call("GET", "/api/forum/topics?page=-1", nil, 422)
}
