package api

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/forum"
)

// fakeForumStore is an in-memory forum.Store with the visibility, ownership
// and attachment-binding rules the PostgreSQL store enforces.
type fakeForumStore struct {
	mu          sync.Mutex
	accounts    *fakeStore
	clock       time.Time
	boards      []forum.Board
	topics      []*forum.Topic
	replies     []*forum.Reply
	attachments map[string]*fakeAttachment
}

type fakeAttachment struct {
	forum.File
	uploader string
	bound    bool
}

func newFakeForumStore(accounts *fakeStore) *fakeForumStore {
	return &fakeForumStore{accounts: accounts, clock: time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC), attachments: map[string]*fakeAttachment{}}
}

func (s *fakeForumStore) tick() time.Time {
	s.clock = s.clock.Add(time.Minute)
	return s.clock
}

func (s *fakeForumStore) author(id string) forum.Author {
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	return forum.Author{ID: id, DisplayName: s.accounts.accounts[id].DisplayName}
}

func (s *fakeForumStore) topic(id string) (*forum.Topic, bool) {
	for _, topic := range s.topics {
		if topic.ID == id {
			return topic, true
		}
	}
	return nil, false
}

func (s *fakeForumStore) readableTopic(actor forum.Actor, id string) (*forum.Topic, error) {
	topic, ok := s.topic(id)
	if !ok || !forum.CanRead(actor, topic.Kind, topic.Author.ID) {
		return nil, forum.ErrNotFound
	}
	return topic, nil
}

// bind claims the actor's unbound uploads for new content, or fails when one is missing.
func (s *fakeForumStore) bind(actor forum.Actor, ids []string) ([]forum.Attachment, error) {
	bound := []forum.Attachment{}
	for _, id := range ids {
		file, ok := s.attachments[id]
		if !ok || file.uploader != actor.ID || file.bound {
			return nil, forum.ErrInvalidInput
		}
		bound = append(bound, file.Attachment)
	}
	for _, id := range ids {
		s.attachments[id].bound = true
	}
	return bound, nil
}

func (s *fakeForumStore) Boards(context.Context) ([]forum.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boards := slices.Clone(s.boards)
	if boards == nil {
		boards = []forum.Board{}
	}
	sort.SliceStable(boards, func(i, j int) bool { return boards[i].SortOrder < boards[j].SortOrder })
	return boards, nil
}

func (s *fakeForumStore) SaveBoard(_ context.Context, _ forum.Actor, id string, in forum.BoardInput) (forum.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		board := forum.Board{ID: fakeUUID(), Name: in.Name, Description: in.Description, SortOrder: in.SortOrder, CreatedAt: s.tick()}
		s.boards = append(s.boards, board)
		return board, nil
	}
	for index := range s.boards {
		if s.boards[index].ID == id {
			s.boards[index].Name, s.boards[index].Description, s.boards[index].SortOrder = in.Name, in.Description, in.SortOrder
			return s.boards[index], nil
		}
	}
	return forum.Board{}, forum.ErrNotFound
}

func (s *fakeForumStore) DeleteBoard(_ context.Context, _ forum.Actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, topic := range s.topics {
		if topic.BoardID != nil && *topic.BoardID == id {
			return forum.ErrConflict
		}
	}
	before := len(s.boards)
	s.boards = slices.DeleteFunc(s.boards, func(board forum.Board) bool { return board.ID == id })
	if len(s.boards) == before {
		return forum.ErrNotFound
	}
	return nil
}

func page[T any](items []T, filter forum.Filter) forum.Page[T] {
	total := int64(len(items))
	start := min(int((filter.Page-1)*filter.Limit), len(items))
	end := min(start+int(filter.Limit), len(items))
	window := slices.Clone(items[start:end])
	if window == nil {
		window = []T{}
	}
	return forum.Page[T]{Items: window, Total: total, Page: filter.Page, Limit: filter.Limit}
}

func (s *fakeForumStore) Topics(_ context.Context, actor forum.Actor, filter forum.Filter) (forum.Page[forum.Topic], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var topics []forum.Topic
	for _, topic := range s.topics {
		if topic.Kind != filter.Kind || !forum.CanRead(actor, topic.Kind, topic.Author.ID) ||
			(filter.BoardID != nil && (topic.BoardID == nil || *topic.BoardID != *filter.BoardID)) ||
			(filter.Search != "" && !strings.Contains(topic.Title+" "+topic.Body, filter.Search)) {
			continue
		}
		topics = append(topics, *topic)
	}
	sort.SliceStable(topics, func(i, j int) bool { return topics[i].CreatedAt.After(topics[j].CreatedAt) })
	return page(topics, filter), nil
}

func (s *fakeForumStore) Topic(_ context.Context, actor forum.Actor, id string) (forum.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	topic, err := s.readableTopic(actor, id)
	if err != nil {
		return forum.Topic{}, err
	}
	return *topic, nil
}

func (s *fakeForumStore) CreateTopic(_ context.Context, actor forum.Actor, in forum.TopicInput) (forum.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Kind == "discussion" && !slices.ContainsFunc(s.boards, func(board forum.Board) bool { return board.ID == *in.BoardID }) {
		return forum.Topic{}, forum.ErrInvalidInput
	}
	attachments, err := s.bind(actor, in.AttachmentIDs)
	if err != nil {
		return forum.Topic{}, err
	}
	created := s.tick()
	topic := &forum.Topic{
		ID: fakeUUID(), Kind: in.Kind, BoardID: in.BoardID, Title: in.Title, Body: in.Body, Author: s.author(actor.ID),
		CreatedAt: created, UpdatedAt: created, Attachments: attachments,
	}
	if in.Kind == "ticket" {
		status := "pending"
		topic.Status = &status
	}
	s.topics = append(s.topics, topic)
	return *topic, nil
}

func (s *fakeForumStore) EditTopic(_ context.Context, actor forum.Actor, id string, in forum.TopicEdit) (forum.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	topic, err := s.readableTopic(actor, id)
	if err != nil {
		return forum.Topic{}, err
	}
	if !forum.CanEdit(actor, topic.Author.ID) {
		return forum.Topic{}, forum.ErrForbidden
	}
	attachments, err := s.bind(actor, in.AttachmentIDs)
	if err != nil {
		return forum.Topic{}, err
	}
	topic.Title, topic.Body, topic.Attachments, topic.UpdatedAt = in.Title, in.Body, attachments, s.tick()
	return *topic, nil
}

func (s *fakeForumStore) DeleteTopic(_ context.Context, actor forum.Actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	topic, err := s.readableTopic(actor, id)
	if err != nil {
		return err
	}
	if !forum.CanEdit(actor, topic.Author.ID) {
		return forum.ErrForbidden
	}
	s.topics = slices.DeleteFunc(s.topics, func(candidate *forum.Topic) bool { return candidate == topic })
	s.replies = slices.DeleteFunc(s.replies, func(reply *forum.Reply) bool { return reply.TopicID == id })
	return nil
}

func (s *fakeForumStore) SetStatus(_ context.Context, actor forum.Actor, id, status string) (forum.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	topic, err := s.readableTopic(actor, id)
	if err != nil {
		return forum.Topic{}, err
	}
	if topic.Status == nil {
		return forum.Topic{}, forum.ErrInvalidInput
	}
	if err := forum.ValidateStatus(actor, topic.Author.ID, *topic.Status, status); err != nil {
		return forum.Topic{}, err
	}
	topic.Status, topic.UpdatedAt = &status, s.tick()
	return *topic, nil
}

func (s *fakeForumStore) Replies(_ context.Context, actor forum.Actor, topicID string, filter forum.Filter) (forum.Page[forum.Reply], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readableTopic(actor, topicID); err != nil {
		return forum.Page[forum.Reply]{}, err
	}
	var replies []forum.Reply
	for _, reply := range s.replies {
		if reply.TopicID == topicID {
			replies = append(replies, *reply)
		}
	}
	return page(replies, filter), nil
}

func (s *fakeForumStore) SaveReply(_ context.Context, actor forum.Actor, topicID, id string, in forum.ContentInput) (forum.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		topic, err := s.readableTopic(actor, topicID)
		if err != nil {
			return forum.Reply{}, err
		}
		attachments, err := s.bind(actor, in.AttachmentIDs)
		if err != nil {
			return forum.Reply{}, err
		}
		created := s.tick()
		reply := &forum.Reply{ID: fakeUUID(), TopicID: topic.ID, Body: in.Body, Author: s.author(actor.ID), CreatedAt: created, UpdatedAt: created, Attachments: attachments}
		s.replies = append(s.replies, reply)
		topic.ReplyCount++
		return *reply, nil
	}
	for _, reply := range s.replies {
		if reply.ID != id {
			continue
		}
		if !forum.CanEdit(actor, reply.Author.ID) {
			return forum.Reply{}, forum.ErrForbidden
		}
		attachments, err := s.bind(actor, in.AttachmentIDs)
		if err != nil {
			return forum.Reply{}, err
		}
		reply.Body, reply.Attachments, reply.UpdatedAt = in.Body, attachments, s.tick()
		return *reply, nil
	}
	return forum.Reply{}, forum.ErrNotFound
}

func (s *fakeForumStore) DeleteReply(_ context.Context, actor forum.Actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, reply := range s.replies {
		if reply.ID != id {
			continue
		}
		if !forum.CanEdit(actor, reply.Author.ID) {
			return forum.ErrForbidden
		}
		s.replies = slices.DeleteFunc(s.replies, func(candidate *forum.Reply) bool { return candidate == reply })
		if topic, ok := s.topic(reply.TopicID); ok {
			topic.ReplyCount--
		}
		return nil
	}
	return forum.ErrNotFound
}

func (s *fakeForumStore) Upload(_ context.Context, actor forum.Actor, file forum.File) (forum.Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file.ID = fakeUUID()
	file.URL = "/api/forum/attachments/" + file.ID
	s.attachments[file.ID] = &fakeAttachment{File: file, uploader: actor.ID}
	return file.Attachment, nil
}

func (s *fakeForumStore) Attachment(_ context.Context, actor forum.Actor, id string) (forum.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.attachments[id]
	if !ok || (!file.bound && file.uploader != actor.ID) {
		return forum.File{}, forum.ErrNotFound
	}
	return file.File, nil
}

func (s *fakeForumStore) Cleanup(context.Context) (int64, error) { return 0, nil }
