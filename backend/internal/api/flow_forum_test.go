package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"testing"
)

func TestForumDiscussionsTicketsAndAttachments(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	alice, _ := p.member(t, "alice")
	bob, _ := p.member(t, "bob")

	board := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/forum/boards", map[string]any{"name": "综合", "description": "闲聊", "sort_order": 2})
	boardID := board["id"].(string)
	if renamed := admin.expect(t, http.StatusOK, http.MethodPatch, "/api/forum/boards/"+boardID, map[string]any{"name": "综合讨论", "description": "", "sort_order": 1}); renamed["name"] != "综合讨论" {
		t.Fatalf("board = %v", renamed)
	}
	if recorder := alice.call(t, http.MethodPost, "/api/forum/boards", map[string]any{"name": "x"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("member creates board = %d", recorder.Code)
	}
	if recorder := admin.call(t, http.MethodPatch, "/api/forum/boards/30000000-0000-4000-8000-0000000000ff", map[string]any{"name": "x"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown board = %d", recorder.Code)
	}
	if boards := bob.expect(t, http.StatusOK, http.MethodGet, "/api/forum/boards", nil); len(boards["items"].([]any)) != 1 {
		t.Fatalf("boards = %v", boards)
	}

	// 附件：先上传，再随内容绑定；图片内联，其余强制下载。
	image := p.upload(t, alice, "pixel.png", tinyPNG(t))
	note := p.upload(t, alice, "notes.txt", []byte("hello"))
	if image["media_type"] != "image/png" || image["inline"] != true || note["media_type"] != "application/octet-stream" || note["inline"] != false {
		t.Fatalf("uploads = %v / %v", image, note)
	}
	for _, request := range []struct {
		client *client
		upload map[string]any
		want   string
	}{{alice, image, "image/png"}, {alice, note, "application/octet-stream"}} {
		recorder := request.client.call(t, http.MethodGet, request.upload["url"].(string), nil)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != request.want {
			t.Fatalf("download %v = %d %v", request.upload["url"], recorder.Code, recorder.Header())
		}
	}
	if recorder := bob.call(t, http.MethodGet, image["url"].(string), nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("stranger downloads an unbound upload = %d", recorder.Code)
	}

	topicBody := map[string]any{"kind": "discussion", "board_id": boardID, "title": "第一个主题", "body": "大家好", "attachment_ids": []string{image["id"].(string), note["id"].(string)}}
	topic := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/forum/topics", topicBody)
	topicID := topic["id"].(string)
	if topic["status"] != nil || len(topic["attachments"].([]any)) != 2 || topic["reply_count"] != float64(0) {
		t.Fatalf("topic = %v", topic)
	}
	if recorder := alice.call(t, http.MethodPost, "/api/forum/topics", topicBody); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reusing bound attachments = %d", recorder.Code)
	}
	if recorder := bob.call(t, http.MethodGet, image["url"].(string), nil); recorder.Code != http.StatusOK {
		t.Fatalf("bound attachment of a discussion = %d", recorder.Code)
	}
	listed := bob.expect(t, http.StatusOK, http.MethodGet, "/api/forum/topics?board_id="+boardID+"&q="+url.QueryEscape("第一")+"&limit=10", nil)
	if listed["total"] != float64(1) || len(listed["items"].([]any)) != 1 {
		t.Fatalf("topics = %v", listed)
	}
	if recorder := bob.call(t, http.MethodGet, "/api/forum/topics?kind=wiki", nil); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad topic kind = %d", recorder.Code)
	}
	topicPath := "/api/forum/topics/" + topicID
	edited := alice.expect(t, http.StatusOK, http.MethodPatch, topicPath, map[string]any{"title": "改过的主题", "body": "更新", "attachment_ids": []string{}})
	if edited["title"] != "改过的主题" || len(edited["attachments"].([]any)) != 0 {
		t.Fatalf("edited topic = %v", edited)
	}
	if recorder := bob.call(t, http.MethodPatch, topicPath, map[string]any{"title": "抢改", "body": "x", "attachment_ids": []string{}}); recorder.Code != http.StatusForbidden {
		t.Fatalf("edit by stranger = %d", recorder.Code)
	}
	if got := bob.expect(t, http.StatusOK, http.MethodGet, topicPath, nil); got["id"] != topicID {
		t.Fatalf("topic detail = %v", got)
	}

	// 回复。
	reply := bob.expect(t, http.StatusCreated, http.MethodPost, topicPath+"/replies", map[string]any{"body": "欢迎", "attachment_ids": []string{}})
	replyID := reply["id"].(string)
	replies := alice.expect(t, http.StatusOK, http.MethodGet, topicPath+"/replies", nil)
	if replies["total"] != float64(1) || replies["items"].([]any)[0].(map[string]any)["topic_id"] != topicID {
		t.Fatalf("replies = %v", replies)
	}
	if got := bob.expect(t, http.StatusOK, http.MethodPatch, "/api/forum/replies/"+replyID, map[string]any{"body": "欢迎加入", "attachment_ids": []string{}}); got["body"] != "欢迎加入" {
		t.Fatalf("edited reply = %v", got)
	}
	if recorder := alice.call(t, http.MethodPatch, "/api/forum/replies/"+replyID, map[string]any{"body": "x", "attachment_ids": []string{}}); recorder.Code != http.StatusForbidden {
		t.Fatalf("edit someone's reply = %d", recorder.Code)
	}
	bob.expect(t, http.StatusNoContent, http.MethodDelete, "/api/forum/replies/"+replyID, nil)

	// 工单：仅作者与管理员可见，作者只能关闭或重新打开。
	ticket := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/forum/topics", map[string]any{"kind": "ticket", "title": "充值问题", "body": "没到账", "attachment_ids": []string{}})
	ticketID := ticket["id"].(string)
	if ticket["status"] != "pending" || ticket["board_id"] != nil {
		t.Fatalf("ticket = %v", ticket)
	}
	if recorder := bob.call(t, http.MethodGet, "/api/forum/topics/"+ticketID, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("stranger reads a ticket = %d", recorder.Code)
	}
	if tickets := admin.expect(t, http.StatusOK, http.MethodGet, "/api/forum/topics?kind=ticket", nil); tickets["total"] != float64(1) {
		t.Fatalf("tickets = %v", tickets)
	}
	if recorder := alice.call(t, http.MethodPatch, "/api/forum/topics/"+ticketID+"/status", map[string]string{"status": "resolved"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("author resolves own ticket = %d", recorder.Code)
	}
	if status := admin.expect(t, http.StatusOK, http.MethodPatch, "/api/forum/topics/"+ticketID+"/status", map[string]string{"status": "in_progress"}); status["status"] != "in_progress" {
		t.Fatalf("ticket status = %v", status)
	}
	if status := alice.expect(t, http.StatusOK, http.MethodPatch, "/api/forum/topics/"+ticketID+"/status", map[string]string{"status": "closed"}); status["status"] != "closed" {
		t.Fatalf("closed ticket = %v", status)
	}

	// 删除：非空板块不能删，清空后可删。
	if recorder := admin.call(t, http.MethodDelete, "/api/forum/boards/"+boardID, nil); recorder.Code != http.StatusConflict {
		t.Fatalf("delete non-empty board = %d", recorder.Code)
	}
	alice.expect(t, http.StatusNoContent, http.MethodDelete, topicPath, nil)
	alice.expect(t, http.StatusNoContent, http.MethodDelete, "/api/forum/topics/"+ticketID, nil)
	admin.expect(t, http.StatusNoContent, http.MethodDelete, "/api/forum/boards/"+boardID, nil)
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// upload 以 multipart 上传论坛附件，按规范校验响应并返回附件。
func (p *platform) upload(t *testing.T, user *client, name string, data []byte) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "https://hub.example/api/forum/attachments", &body)
	request.Header.Set("Origin", "https://hub.example")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(user.cookie)
	recorder := httptest.NewRecorder()
	p.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload %s = %d %s", name, recorder.Code, recorder.Body.String())
	}
	p.spec.assertResponse(t, http.MethodPost, "/api/forum/attachments", recorder)
	var attachment map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}
