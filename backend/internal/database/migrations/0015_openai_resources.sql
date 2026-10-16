-- +goose Up
ALTER TABLE channel_models DROP CONSTRAINT channel_models_formats_check;
ALTER TABLE channel_models ADD CONSTRAINT channel_models_formats_check CHECK (
 cardinality(formats)>0 AND formats <@ ARRAY['openai_chat','openai_responses','anthropic','gemini','openai_compact','openai_count','openai_conversations','openai_image_generation','openai_image_edit','openai_image_variation']::text[]
);
ALTER TABLE calls DROP CONSTRAINT calls_format_check;
ALTER TABLE calls ADD CONSTRAINT calls_format_check CHECK (format IN ('openai_chat','openai_responses','anthropic','gemini','openai_compact','openai_count','openai_conversations','openai_image_generation','openai_image_edit','openai_image_variation'));
-- Routing metadata only; no messages, prompts, images or response bodies.
CREATE TABLE gateway_resources (
 account_id uuid NOT NULL REFERENCES accounts(id),
 key_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('chat','response','conversation')),
 resource_id text NOT NULL CHECK (length(resource_id) BETWEEN 1 AND 256),
 call_id uuid NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
 channel_id uuid NOT NULL REFERENCES channels(id),
 model_id text NOT NULL,
 metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata)='object' AND pg_column_size(metadata)<=65536),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (account_id,key_id,kind,resource_id),
 UNIQUE (channel_id,kind,resource_id)
);
CREATE INDEX gateway_resources_call ON gateway_resources(call_id);
CREATE INDEX gateway_resources_page ON gateway_resources(account_id,key_id,kind,created_at,resource_id);
-- +goose Down
DROP TABLE gateway_resources;
-- Retained calls and channel declarations may use these formats; downgrade
-- intentionally keeps the widened format constraints rather than losing data.
