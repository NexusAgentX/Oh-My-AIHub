-- +goose Up
-- This setting and its admission/cache consumers are removed together. No
-- historical calls, revenue statistics, balances, or ledger entries change.
ALTER TABLE channels DROP COLUMN daily_revenue_cap_nano;

-- +goose Down
ALTER TABLE channels ADD COLUMN daily_revenue_cap_nano bigint
 CHECK (daily_revenue_cap_nano IS NULL OR daily_revenue_cap_nano > 0);
