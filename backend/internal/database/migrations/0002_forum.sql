-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Forum content and bounded private attachments (ADR-0031).
CREATE TABLE forum_boards (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 name TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 80),
 description TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE forum_topics (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 kind TEXT NOT NULL CHECK (kind IN ('discussion','ticket')),
 board_id UUID REFERENCES forum_boards(id) ON DELETE RESTRICT,
 author_id UUID NOT NULL REFERENCES accounts(id),
 title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
 body TEXT NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 262144),
 status TEXT CHECK (status IN ('pending','in_progress','resolved','closed')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK ((kind='discussion' AND board_id IS NOT NULL AND status IS NULL) OR (kind='ticket' AND board_id IS NULL AND status IS NOT NULL))
);
CREATE INDEX forum_topics_board_created ON forum_topics(board_id, created_at DESC, id);
CREATE INDEX forum_topics_author_created ON forum_topics(author_id, created_at DESC, id);
CREATE TABLE forum_replies (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 topic_id UUID NOT NULL REFERENCES forum_topics(id) ON DELETE CASCADE,
 author_id UUID NOT NULL REFERENCES accounts(id),
 body TEXT NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 262144),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX forum_replies_topic_created ON forum_replies(topic_id, created_at, id);
CREATE TABLE forum_attachments (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id UUID NOT NULL REFERENCES accounts(id),
 topic_id UUID REFERENCES forum_topics(id) ON DELETE CASCADE,
 reply_id UUID REFERENCES forum_replies(id) ON DELETE CASCADE,
 name TEXT NOT NULL, media_type TEXT NOT NULL, inline BOOLEAN NOT NULL DEFAULT false,
 size BIGINT NOT NULL CHECK (size BETWEEN 1 AND 10485760),
 data BYTEA NOT NULL CHECK (octet_length(data)=size),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (reply_id IS NULL OR topic_id IS NOT NULL)
);
CREATE INDEX forum_attachments_owner ON forum_attachments(owner_id);
CREATE INDEX forum_attachments_topic ON forum_attachments(topic_id);
CREATE INDEX forum_attachments_reply ON forum_attachments(reply_id);
CREATE INDEX forum_attachments_unbound ON forum_attachments(created_at) WHERE topic_id IS NULL;

-- +goose Down
DROP TABLE forum_attachments, forum_replies, forum_topics, forum_boards;
