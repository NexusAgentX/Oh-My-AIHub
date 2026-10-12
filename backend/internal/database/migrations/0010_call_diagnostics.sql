-- +goose Up
ALTER TABLE calls ADD COLUMN diagnostics jsonb;

-- +goose Down
ALTER TABLE calls DROP COLUMN diagnostics;
