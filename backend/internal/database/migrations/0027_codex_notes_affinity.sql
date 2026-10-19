-- +goose Up
ALTER TABLE api_keys ADD COLUMN codex_notes boolean NOT NULL DEFAULT false;

-- Explicit opt-in routing metadata only; never context, note paths or bodies.
-- Keep tombstones when channels/credentials disappear so a session cannot move
-- silently. Account and platform-Key authentication is checked on every use.
CREATE TABLE codex_session_routes (
 account_id uuid NOT NULL,
 key_id uuid NOT NULL,
 session_id text NOT NULL CHECK(length(session_id)=36),
 group_id uuid NOT NULL,
 channel_id uuid NOT NULL,
 credential_version text NOT NULL CHECK(length(credential_version) BETWEEN 1 AND 32),
 credential_scope text NOT NULL CHECK(length(credential_scope) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,key_id,session_id)
);

-- +goose Down
DROP TABLE codex_session_routes;
ALTER TABLE api_keys DROP COLUMN codex_notes;
