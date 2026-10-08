-- +goose Up
-- 渠道评分被删除（Feature #160）；评分数据随表一并删除。
DROP TABLE channel_ratings;

-- +goose Down
CREATE TABLE channel_ratings (
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    score SMALLINT NOT NULL CHECK (score BETWEEN 1 AND 5),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, account_id)
);

CREATE INDEX channel_ratings_channel_idx ON channel_ratings(channel_id);
