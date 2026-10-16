-- +goose Up
CREATE TABLE gateway_background_jobs (
 call_id uuid PRIMARY KEY REFERENCES calls(id),
 account_id uuid NOT NULL REFERENCES accounts(id),
 key_id uuid NOT NULL,
 channel_id uuid NOT NULL REFERENCES channels(id),
 response_id text NOT NULL CHECK (length(response_id) BETWEEN 1 AND 256),
 metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
 next_poll_at timestamptz NOT NULL DEFAULT now(),
 deadline_at timestamptz NOT NULL,
 settled_at timestamptz,
 UNIQUE (channel_id, response_id),
 UNIQUE (key_id, response_id)
);
CREATE INDEX gateway_background_poll ON gateway_background_jobs (next_poll_at, call_id) WHERE settled_at IS NULL;
CREATE INDEX gateway_background_lookup ON gateway_background_jobs (key_id, response_id);

-- +goose Down
DROP TABLE gateway_background_jobs;
