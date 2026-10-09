// Package forumpg persists forum content and private attachments with sqlc.
package forumpg

import (
	"context"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"time"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool, New(pool)} }

var _ forum.Store = (*Store)(nil)

func mapError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return forum.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505", "23503", "23001":
			return forum.ErrConflict
		case "23514", "22P02":
			return forum.ErrInvalidInput
		}
	}
	return err
}
func board(r ForumBoard) forum.Board {
	return forum.Board{ID: r.ID, Name: r.Name, Description: r.Description, SortOrder: r.SortOrder, CreatedAt: r.CreatedAt}
}
func attachment(id, name, media string, size int64, inline bool) forum.Attachment {
	return forum.Attachment{ID: id, Name: name, MediaType: media, Size: size, Inline: inline, URL: "/api/forum/attachments/" + id}
}
func attachments(ctx context.Context, q *Queries, topicID string, replyID *string) ([]forum.Attachment, error) {
	rows, err := q.ListAttachments(ctx, ListAttachmentsParams{TopicID: &topicID, ReplyID: replyID})
	out := []forum.Attachment{}
	for _, r := range rows {
		out = append(out, attachment(r.ID, r.Name, r.MediaType, r.Size, r.Inline))
	}
	return out, err
}
func topic(ctx context.Context, q *Queries, r ForumTopic, name string, count int64) (forum.Topic, error) {
	atts, err := attachments(ctx, q, r.ID, nil)
	return forum.Topic{ID: r.ID, Kind: r.Kind, BoardID: r.BoardID, Title: r.Title, Body: r.Body, Author: forum.Author{ID: r.AuthorID, DisplayName: name}, Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Attachments: atts, ReplyCount: count}, err
}
func reply(ctx context.Context, q *Queries, r ForumReply, name string) (forum.Reply, error) {
	atts, err := attachments(ctx, q, r.TopicID, &r.ID)
	return forum.Reply{ID: r.ID, TopicID: r.TopicID, Body: r.Body, Author: forum.Author{ID: r.AuthorID, DisplayName: name}, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Attachments: atts}, err
}
func visible(a forum.Actor, t ForumTopic) error {
	if !forum.CanRead(a, t.Kind, t.AuthorID) {
		return forum.ErrNotFound
	}
	return nil
}
func editable(a forum.Actor, t ForumTopic) error {
	if err := visible(a, t); err != nil {
		return err
	}
	if !forum.CanEdit(a, t.AuthorID) {
		return forum.ErrForbidden
	}
	return nil
}
func (s *Store) Boards(ctx context.Context) ([]forum.Board, error) {
	rows, err := s.q.ListBoards(ctx)
	out := []forum.Board{}
	for _, r := range rows {
		out = append(out, board(r))
	}
	return out, err
}
func (s *Store) SaveBoard(ctx context.Context, a forum.Actor, id string, in forum.BoardInput) (forum.Board, error) {
	var r ForumBoard
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		action := "created"
		if id == "" {
			r, err = q.CreateBoard(ctx, CreateBoardParams{Name: in.Name, Description: in.Description, SortOrder: in.SortOrder})
		} else {
			action = "updated"
			r, err = q.UpdateBoard(ctx, UpdateBoardParams{ID: id, Name: in.Name, Description: in.Description, SortOrder: in.SortOrder})
		}
		if err != nil {
			return err
		}
		return record(ctx, tx, a, "board", r.ID, action)
	})
	return board(r), mapError(err)
}
func (s *Store) DeleteBoard(ctx context.Context, a forum.Actor, id string) error {
	return mapError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := s.q.WithTx(tx).DeleteBoard(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return forum.ErrNotFound
		}
		return record(ctx, tx, a, "board", id, "deleted")
	}))
}
func record(ctx context.Context, tx pgx.Tx, a forum.Actor, kind, id, action string) error {
	return auditpg.Record(ctx, tx, auditpg.Event{ActorID: a.ID, Action: "forum." + kind + "." + action, TargetType: "forum_" + kind, TargetID: id})
}
func (s *Store) Topics(ctx context.Context, a forum.Actor, f forum.Filter) (forum.Page[forum.Topic], error) {
	out := forum.Page[forum.Topic]{Items: []forum.Topic{}, Page: f.Page, Limit: f.Limit}
	rows, err := s.q.ListTopics(ctx, ListTopicsParams{Kind: f.Kind, ActorID: a.ID, IsAdmin: a.Admin, BoardID: f.BoardID, Search: f.Search, PageOffset: (f.Page - 1) * f.Limit, PageLimit: f.Limit})
	if err != nil {
		return out, mapError(err)
	}
	for _, r := range rows {
		v, e := topic(ctx, s.q, r.ForumTopic, r.DisplayName, r.ReplyCount)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	out.Total, err = s.q.CountTopics(ctx, CountTopicsParams{Kind: f.Kind, ActorID: a.ID, IsAdmin: a.Admin, BoardID: f.BoardID, Search: f.Search})
	return out, err
}
func readTopic(ctx context.Context, q *Queries, a forum.Actor, id string) (forum.Topic, error) {
	r, err := q.GetTopic(ctx, id)
	if err != nil {
		return forum.Topic{}, mapError(err)
	}
	if err = visible(a, r.ForumTopic); err != nil {
		return forum.Topic{}, err
	}
	return topic(ctx, q, r.ForumTopic, r.DisplayName, r.ReplyCount)
}
func (s *Store) Topic(ctx context.Context, a forum.Actor, id string) (forum.Topic, error) {
	return readTopic(ctx, s.q, a, id)
}

// Binding is immutable across contents. An editor may retain existing attachments,
// but new bindings must be fresh uploads owned by that editor. Sort locks to avoid
// deadlocks when concurrent requests contain overlapping attachment sets.
func bind(ctx context.Context, q *Queries, a forum.Actor, topicID string, replyID *string, ids []string) error {
	ids = append([]string{}, ids...)
	sort.Strings(ids)
	for _, id := range ids {
		r, err := q.LockAttachment(ctx, id)
		if err != nil {
			return mapError(err)
		}
		if r.TopicID != nil {
			if *r.TopicID != topicID || !sameID(r.ReplyID, replyID) {
				return forum.ErrInvalidInput
			}
		} else {
			if r.OwnerID != a.ID || r.CreatedAt.Before(time.Now().Add(-24*time.Hour)) {
				return forum.ErrInvalidInput
			}
		}
		if err = q.BindAttachment(ctx, BindAttachmentParams{ID: id, TopicID: &topicID, ReplyID: replyID}); err != nil {
			return err
		}
	}
	return q.DeleteRemovedAttachments(ctx, DeleteRemovedAttachmentsParams{TopicID: &topicID, ReplyID: replyID, KeepIds: ids})
}
func sameID(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (s *Store) CreateTopic(ctx context.Context, a forum.Actor, in forum.TopicInput) (forum.Topic, error) {
	var out forum.Topic
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var status *string
		if in.Kind == "ticket" {
			v := "pending"
			status = &v
		}
		r, err := q.CreateTopic(ctx, CreateTopicParams{Kind: in.Kind, BoardID: in.BoardID, AuthorID: a.ID, Title: in.Title, Body: in.Body, Status: status})
		if err != nil {
			return err
		}
		if err = bind(ctx, q, a, r.ID, nil, in.AttachmentIDs); err != nil {
			return err
		}
		out, err = readTopic(ctx, q, a, r.ID)
		return err
	})
	return out, mapError(err)
}
func (s *Store) EditTopic(ctx context.Context, a forum.Actor, id string, in forum.TopicEdit) (forum.Topic, error) {
	var out forum.Topic
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		r, err := q.LockTopic(ctx, id)
		if err != nil {
			return err
		}
		if err = editable(a, r); err != nil {
			return err
		}
		if err = q.UpdateTopic(ctx, UpdateTopicParams{ID: id, Title: in.Title, Body: in.Body}); err != nil {
			return err
		}
		if err = bind(ctx, q, a, id, nil, in.AttachmentIDs); err != nil {
			return err
		}
		out, err = readTopic(ctx, q, a, id)
		if err != nil {
			return err
		}
		return record(ctx, tx, a, "topic", id, "updated")
	})
	return out, mapError(err)
}
func (s *Store) DeleteTopic(ctx context.Context, a forum.Actor, id string) error {
	return mapError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		r, err := q.LockTopic(ctx, id)
		if err != nil {
			return err
		}
		if err = editable(a, r); err != nil {
			return err
		}
		if err := q.DeleteTopic(ctx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "topic", id, "deleted")
	}))
}
func (s *Store) SetStatus(ctx context.Context, a forum.Actor, id, status string) (forum.Topic, error) {
	var out forum.Topic
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		r, err := q.LockTopic(ctx, id)
		if err != nil {
			return err
		}
		if err = visible(a, r); err != nil {
			return err
		}
		if r.Kind != "ticket" {
			return forum.ErrInvalidInput
		}
		if err = forum.ValidateStatus(a, r.AuthorID, *r.Status, status); err != nil {
			return err
		}
		if err = q.UpdateStatus(ctx, UpdateStatusParams{ID: id, Status: &status}); err != nil {
			return err
		}
		out, err = readTopic(ctx, q, a, id)
		if err != nil {
			return err
		}
		return record(ctx, tx, a, "ticket", id, "status_changed")
	})
	return out, mapError(err)
}
func (s *Store) Replies(ctx context.Context, a forum.Actor, id string, f forum.Filter) (forum.Page[forum.Reply], error) {
	out := forum.Page[forum.Reply]{Items: []forum.Reply{}, Page: f.Page, Limit: f.Limit}
	if _, err := readTopic(ctx, s.q, a, id); err != nil {
		return out, err
	}
	rows, err := s.q.ListReplies(ctx, ListRepliesParams{TopicID: id, PageOffset: (f.Page - 1) * f.Limit, PageLimit: f.Limit})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		v, e := reply(ctx, s.q, r.ForumReply, r.DisplayName)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	out.Total, err = s.q.CountReplies(ctx, id)
	return out, err
}
func (s *Store) SaveReply(ctx context.Context, a forum.Actor, topicID, id string, in forum.ContentInput) (forum.Reply, error) {
	var out forum.Reply
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var existing GetReplyRow
		var err error
		if id != "" {
			existing, err = q.GetReply(ctx, id)
			if err != nil {
				return err
			}
			topicID = existing.ForumReply.TopicID
		}
		t, err := q.LockTopic(ctx, topicID)
		if err != nil {
			return err
		}
		if err = visible(a, t); err != nil {
			return err
		}
		if id == "" {
			if t.Status != nil && *t.Status == "closed" {
				return forum.ErrConflict
			}
			r, e := q.CreateReply(ctx, CreateReplyParams{TopicID: topicID, AuthorID: a.ID, Body: in.Body})
			if e != nil {
				return e
			}
			id = r.ID
		} else {
			if !forum.CanEdit(a, existing.ForumReply.AuthorID) {
				return forum.ErrForbidden
			}
			if err = q.UpdateReply(ctx, UpdateReplyParams{ID: id, Body: in.Body}); err != nil {
				return err
			}
		}
		if err = bind(ctx, q, a, topicID, &id, in.AttachmentIDs); err != nil {
			return err
		}
		r, err := q.GetReply(ctx, id)
		if err != nil {
			return err
		}
		out, err = reply(ctx, q, r.ForumReply, r.DisplayName)
		return err
	})
	return out, mapError(err)
}
func (s *Store) DeleteReply(ctx context.Context, a forum.Actor, id string) error {
	return mapError(pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		r, err := q.GetReply(ctx, id)
		if err != nil {
			return err
		}
		t, err := q.LockTopic(ctx, r.ForumReply.TopicID)
		if err != nil {
			return err
		}
		if err = visible(a, t); err != nil {
			return err
		}
		if !forum.CanEdit(a, r.ForumReply.AuthorID) {
			return forum.ErrForbidden
		}
		if err := q.DeleteReply(ctx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "reply", id, "deleted")
	}))
}
func (s *Store) Upload(ctx context.Context, a forum.Actor, f forum.File) (forum.Attachment, error) {
	var out forum.Attachment
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockOwner(ctx, a.ID); err != nil {
			return err
		}
		used, err := q.UsedBytes(ctx, a.ID)
		if err != nil {
			return err
		}
		if used+f.Size > forum.MaxAccountBytes {
			return forum.ErrQuota
		}
		r, err := q.CreateAttachment(ctx, CreateAttachmentParams{OwnerID: a.ID, Name: f.Name, MediaType: f.MediaType, Inline: f.Inline, Size: f.Size, Data: f.Data})
		if err == nil {
			out = attachment(r.ID, r.Name, r.MediaType, r.Size, r.Inline)
		}
		return err
	})
	return out, mapError(err)
}
func (s *Store) Attachment(ctx context.Context, a forum.Actor, id string) (forum.File, error) {
	r, err := s.q.GetAttachment(ctx, id)
	if err != nil {
		return forum.File{}, mapError(err)
	}
	if r.TopicID == nil {
		if r.OwnerID != a.ID || r.CreatedAt.Before(time.Now().Add(-24*time.Hour)) {
			return forum.File{}, forum.ErrNotFound
		}
	} else {
		t, e := s.q.GetTopic(ctx, *r.TopicID)
		if e != nil {
			return forum.File{}, mapError(e)
		}
		if e = visible(a, t.ForumTopic); e != nil {
			return forum.File{}, e
		}
	}
	return forum.File{Attachment: attachment(r.ID, r.Name, r.MediaType, r.Size, r.Inline), Data: r.Data}, nil
}
func (s *Store) Cleanup(ctx context.Context) (int64, error) { return s.q.CleanupAttachments(ctx) }
