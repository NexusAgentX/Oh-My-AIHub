package forum

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestContentRequiresExplicitAttachmentSet(t *testing.T) {
	for _, in := range []ContentInput{{Body: "hello"}, {Body: "hello", AttachmentIDs: []string{"bad"}}, {Body: " ", AttachmentIDs: []string{}}, {Body: strings.Repeat("x", MaxBodyBytes+1), AttachmentIDs: []string{}}} {
		if validateContent(in) == nil {
			t.Fatalf("accepted invalid input: %d bytes, %v", len(in.Body), in.AttachmentIDs)
		}
	}
	if err := validateContent(ContentInput{Body: "正文", AttachmentIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = "00000000-0000-0000-0000-000000000000"
	}
	if validateContent(ContentInput{Body: "hello", AttachmentIDs: ids}) == nil {
		t.Fatal("accepted attachment overflow")
	}
}
func TestPrivateVisibilityAndTicketState(t *testing.T) {
	owner, other, admin := Actor{ID: "owner"}, Actor{ID: "other"}, Actor{ID: "admin", Admin: true}
	if CanRead(other, "ticket", "owner") || !CanRead(owner, "ticket", "owner") || !CanRead(admin, "ticket", "owner") || !CanRead(other, "discussion", "owner") {
		t.Fatal("visibility policy")
	}
	cases := []struct {
		actor         Actor
		current, next string
		valid         bool
	}{{owner, "pending", "closed", true}, {owner, "closed", "pending", true}, {owner, "resolved", "pending", true}, {owner, "pending", "in_progress", false}, {owner, "in_progress", "resolved", false}, {other, "closed", "pending", false}, {admin, "pending", "resolved", true}, {admin, "pending", "unknown", false}}
	for _, c := range cases {
		if (ValidateStatus(c.actor, "owner", c.current, c.next) == nil) != c.valid {
			t.Errorf("transition %+v", c)
		}
	}
}
func TestPrepareFile(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	f, err := PrepareFile("图.png", buf.Bytes())
	if err != nil || !f.Inline || f.MediaType != "image/png" {
		t.Fatalf("image %v %v", f.Attachment, err)
	}
	f, err = PrepareFile("../文档.html", []byte("<html>local attachment</html>"))
	if err != nil || f.Inline || f.MediaType != "application/octet-stream" || strings.Contains(f.Name, "/") {
		t.Fatalf("download %v %v", f.Attachment, err)
	}
	for _, data := range [][]byte{nil, make([]byte, MaxFileSize+1), buf.Bytes()[:24]} {
		if _, err = PrepareFile("a.png", data); err == nil {
			t.Fatal("accepted invalid/oversized file")
		}
	}
}
