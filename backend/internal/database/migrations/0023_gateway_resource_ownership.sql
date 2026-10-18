-- +goose Up
ALTER TABLE calls DROP CONSTRAINT calls_format_check;
ALTER TABLE calls ADD CONSTRAINT calls_format_check CHECK(format IN ('openai_chat','openai_responses','anthropic','gemini','openai_images'));
-- Identity metadata only. No foreign-key cascades: deleted channels/Keys and
-- historical calls remain meaningful tombstones and collision evidence.
CREATE TABLE gateway_resources (
 identity_hash bytea PRIMARY KEY CHECK(octet_length(identity_hash)=32),
 kind text NOT NULL CHECK(kind IN ('response','conversation','item','stored_chat','cache','audio','container')),
 remote_id text NOT NULL CHECK(octet_length(remote_id) BETWEEN 1 AND 2048),
 parent_id text NOT NULL DEFAULT '' CHECK(octet_length(parent_id)<=2048),
 group_id uuid NOT NULL,
 credential_scope text NOT NULL CHECK(length(credential_scope) BETWEEN 1 AND 128),
 account_id uuid NOT NULL,
 key_id uuid NOT NULL,
 channel_id uuid NOT NULL,
 credential_version text NOT NULL CHECK(length(credential_version) BETWEEN 1 AND 32),
 creation_id text NOT NULL CHECK(length(creation_id) BETWEEN 1 AND 256),
 model_id text NOT NULL DEFAULT '' CHECK(length(model_id)<=256),
 quarantined boolean NOT NULL DEFAULT false,
 deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(kind<>'item' OR parent_id<>'')
);
CREATE INDEX gateway_resources_lookup ON gateway_resources(kind,remote_id);
CREATE INDEX gateway_resources_owner ON gateway_resources(account_id,key_id,kind,created_at);

-- +goose Down
ALTER TABLE calls DROP CONSTRAINT calls_format_check;
ALTER TABLE calls ADD CONSTRAINT calls_format_check CHECK(format IN ('openai_chat','openai_responses','anthropic','gemini'));
DROP TABLE gateway_resources;
