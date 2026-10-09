// Package forum defines member discussions, private support tickets and attachments.
package forum

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxFileSize     = 10 << 20
	MaxAccountBytes = 100 << 20
	MaxAttachments  = 20
	MaxBodyBytes    = 256 << 10
	MaxImagePixels  = 20_000_000
)

var (
	ErrInvalidInput = errors.New("invalid forum input")
	ErrNotFound     = errors.New("forum resource not found")
	ErrForbidden    = errors.New("forum action forbidden")
	ErrConflict     = errors.New("forum resource conflict")
	ErrQuota        = errors.New("forum attachment quota exceeded")
)

type Actor struct {
	ID    string
	Admin bool
}
type Author struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type Board struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int32     `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}
type BoardInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int32  `json:"sort_order"`
}
type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	URL       string `json:"url"`
	Inline    bool   `json:"inline"`
}
type File struct {
	Attachment
	Data []byte `json:"-"`
}
type Topic struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	BoardID     *string      `json:"board_id"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	Author      Author       `json:"author"`
	Status      *string      `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Attachments []Attachment `json:"attachments"`
	ReplyCount  int64        `json:"reply_count"`
}
type Reply struct {
	ID          string       `json:"id"`
	TopicID     string       `json:"topic_id"`
	Body        string       `json:"body"`
	Author      Author       `json:"author"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Attachments []Attachment `json:"attachments"`
}
type ContentInput struct {
	Body          string   `json:"body"`
	AttachmentIDs []string `json:"attachment_ids"`
}
type TopicInput struct {
	Kind    string  `json:"kind"`
	BoardID *string `json:"board_id"`
	Title   string  `json:"title"`
	ContentInput
}
type TopicEdit struct {
	Title string `json:"title"`
	ContentInput
}
type Filter struct {
	Kind    string
	BoardID *string
	Search  string
	Page    int32
	Limit   int32
}
type Page[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
}
type Store interface {
	Boards(context.Context) ([]Board, error)
	SaveBoard(context.Context, Actor, string, BoardInput) (Board, error)
	DeleteBoard(context.Context, Actor, string) error
	Topics(context.Context, Actor, Filter) (Page[Topic], error)
	Topic(context.Context, Actor, string) (Topic, error)
	CreateTopic(context.Context, Actor, TopicInput) (Topic, error)
	EditTopic(context.Context, Actor, string, TopicEdit) (Topic, error)
	DeleteTopic(context.Context, Actor, string) error
	SetStatus(context.Context, Actor, string, string) (Topic, error)
	Replies(context.Context, Actor, string, Filter) (Page[Reply], error)
	SaveReply(context.Context, Actor, string, string, ContentInput) (Reply, error)
	DeleteReply(context.Context, Actor, string) error
	Upload(context.Context, Actor, File) (Attachment, error)
	Attachment(context.Context, Actor, string) (File, error)
	Cleanup(context.Context) (int64, error)
}
type Service struct{ store Store }

func NewService(s Store) *Service { return &Service{store: s} }

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidID(id string) bool { return uuidPattern.MatchString(id) }
func CanRead(a Actor, kind, author string) bool {
	return kind == "discussion" || a.Admin || a.ID == author
}
func CanEdit(a Actor, author string) bool { return a.Admin || a.ID == author }
func ValidateStatus(a Actor, author, current, next string) error {
	if next != "pending" && next != "in_progress" && next != "resolved" && next != "closed" {
		return ErrInvalidInput
	}
	if a.Admin {
		return nil
	}
	if a.ID != author {
		return ErrNotFound
	}
	if next == "closed" || (next == "pending" && (current == "closed" || current == "resolved")) {
		return nil
	}
	return ErrForbidden
}
func validText(s string, max int) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && len(strings.TrimSpace(s)) > 0 && utf8.RuneCountInString(s) <= max
}
func validateContent(in ContentInput) error {
	if in.AttachmentIDs == nil || len(in.Body) > MaxBodyBytes || !validText(in.Body, MaxBodyBytes) || len(in.AttachmentIDs) > MaxAttachments {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, id := range in.AttachmentIDs {
		if !ValidID(id) || seen[id] {
			return ErrInvalidInput
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) Boards(ctx context.Context) ([]Board, error) { return s.store.Boards(ctx) }
func (s *Service) SaveBoard(ctx context.Context, a Actor, id string, in BoardInput) (Board, error) {
	if !a.Admin {
		return Board{}, ErrForbidden
	}
	in.Name = strings.TrimSpace(in.Name)
	if (id != "" && !ValidID(id)) || !validText(in.Name, 80) || len(in.Description) > 2000 || !utf8.ValidString(in.Description) || strings.ContainsRune(in.Description, 0) {
		return Board{}, ErrInvalidInput
	}
	return s.store.SaveBoard(ctx, a, id, in)
}
func (s *Service) DeleteBoard(ctx context.Context, a Actor, id string) error {
	if !a.Admin {
		return ErrForbidden
	}
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.store.DeleteBoard(ctx, a, id)
}
func ValidateFilter(f Filter) error {
	if f.Page < 1 || f.Page > 100000 || f.Limit < 1 || f.Limit > 100 || len(f.Search) > 200 || !utf8.ValidString(f.Search) || strings.ContainsRune(f.Search, 0) || (f.BoardID != nil && !ValidID(*f.BoardID)) {
		return ErrInvalidInput
	}
	return nil
}
func (s *Service) Topics(ctx context.Context, a Actor, f Filter) (Page[Topic], error) {
	if (f.Kind != "discussion" && f.Kind != "ticket") || ValidateFilter(f) != nil {
		return Page[Topic]{}, ErrInvalidInput
	}
	return s.store.Topics(ctx, a, f)
}
func (s *Service) Topic(ctx context.Context, a Actor, id string) (Topic, error) {
	if !ValidID(id) {
		return Topic{}, ErrNotFound
	}
	return s.store.Topic(ctx, a, id)
}
func (s *Service) CreateTopic(ctx context.Context, a Actor, in TopicInput) (Topic, error) {
	in.Title = strings.TrimSpace(in.Title)
	if !validText(in.Title, 200) || validateContent(in.ContentInput) != nil {
		return Topic{}, ErrInvalidInput
	}
	if !((in.Kind == "discussion" && in.BoardID != nil && ValidID(*in.BoardID)) || (in.Kind == "ticket" && in.BoardID == nil)) {
		return Topic{}, ErrInvalidInput
	}
	return s.store.CreateTopic(ctx, a, in)
}
func (s *Service) EditTopic(ctx context.Context, a Actor, id string, in TopicEdit) (Topic, error) {
	in.Title = strings.TrimSpace(in.Title)
	if !ValidID(id) || !validText(in.Title, 200) || validateContent(in.ContentInput) != nil {
		return Topic{}, ErrInvalidInput
	}
	return s.store.EditTopic(ctx, a, id, in)
}
func (s *Service) DeleteTopic(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.store.DeleteTopic(ctx, a, id)
}
func (s *Service) SetStatus(ctx context.Context, a Actor, id, status string) (Topic, error) {
	if !ValidID(id) {
		return Topic{}, ErrNotFound
	}
	return s.store.SetStatus(ctx, a, id, status)
}
func (s *Service) Replies(ctx context.Context, a Actor, id string, f Filter) (Page[Reply], error) {
	if !ValidID(id) {
		return Page[Reply]{}, ErrNotFound
	}
	if ValidateFilter(f) != nil {
		return Page[Reply]{}, ErrInvalidInput
	}
	return s.store.Replies(ctx, a, id, f)
}
func (s *Service) SaveReply(ctx context.Context, a Actor, topicID, id string, in ContentInput) (Reply, error) {
	if (!ValidID(topicID) && id == "") || (id != "" && !ValidID(id)) || validateContent(in) != nil {
		return Reply{}, ErrInvalidInput
	}
	return s.store.SaveReply(ctx, a, topicID, id, in)
}
func (s *Service) DeleteReply(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.store.DeleteReply(ctx, a, id)
}
func PrepareFile(name string, data []byte) (File, error) {
	if len(data) == 0 || len(data) > MaxFileSize {
		return File{}, ErrInvalidInput
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name))
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 {
		return File{}, ErrInvalidInput
	}
	media := http.DetectContentType(data)
	inline := media == "image/png" || media == "image/jpeg" || media == "image/gif"
	if inline {
		c, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || c.Width < 1 || c.Height < 1 || int64(c.Width)*int64(c.Height) > MaxImagePixels {
			return File{}, ErrInvalidInput
		}
		if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return File{}, ErrInvalidInput
		}
	}
	if !inline {
		media = "application/octet-stream"
	}
	return File{Attachment: Attachment{Name: name, MediaType: media, Size: int64(len(data)), Inline: inline}, Data: data}, nil
}
func (s *Service) Upload(ctx context.Context, a Actor, name string, data []byte) (Attachment, error) {
	f, err := PrepareFile(name, data)
	if err != nil {
		return Attachment{}, err
	}
	return s.store.Upload(ctx, a, f)
}
func (s *Service) Attachment(ctx context.Context, a Actor, id string) (File, error) {
	if !ValidID(id) {
		return File{}, ErrNotFound
	}
	return s.store.Attachment(ctx, a, id)
}
func (s *Service) Cleanup(ctx context.Context) (int64, error) { return s.store.Cleanup(ctx) }
