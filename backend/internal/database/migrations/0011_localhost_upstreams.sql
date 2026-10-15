-- +goose Up
-- Application policy still requires explicit opt-in before accepting localhost
-- HTTP. Keep the database boundary narrow enough for local mock upstreams.
ALTER TABLE channels DROP CONSTRAINT channels_base_url_check;
ALTER TABLE channels ADD CONSTRAINT channels_base_url_check
    CHECK (base_url ~ '^https://' OR base_url ~ '^http://localhost(:[0-9]+)?(/|$)');

-- +goose Down
-- Refuse rollback while local HTTP channels exist; never delete them silently.
ALTER TABLE channels DROP CONSTRAINT channels_base_url_check;
ALTER TABLE channels ADD CONSTRAINT channels_base_url_check CHECK (base_url ~ '^https://');
