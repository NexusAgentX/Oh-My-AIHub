-- +goose Up
-- Diagnostic trace only. Group capability bindings deliberately exclude entries.
ALTER TABLE detection_attempts ADD COLUMN entry_url text NOT NULL DEFAULT '' CHECK(octet_length(entry_url)<=2048);

-- +goose Down
ALTER TABLE detection_attempts DROP COLUMN entry_url;
