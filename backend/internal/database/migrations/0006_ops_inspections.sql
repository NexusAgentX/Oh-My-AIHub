-- +goose Up
-- Operations cross-module inspection history (Feature #22).
-- Each row is one persisted execution of the fixed invariant set:
-- ledger zero-sum/projections, call settlement linkage, and C2C
-- quantity/hold consistency. Only non-sensitive aggregate differences
-- are stored; no request bodies, credentials, or raw errors.

CREATE TABLE ops_inspections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inspection_version text NOT NULL CHECK (inspection_version = 'ops-v1'),
    triggered_by text NOT NULL CHECK (triggered_by IN ('startup', 'periodic', 'manual')),
    zero_sum_ok boolean NOT NULL,
    projection_ok boolean NOT NULL,
    call_settlement_ok boolean NOT NULL,
    c2c_consistency_ok boolean NOT NULL,
    zero_sum_difference_nano bigint NOT NULL DEFAULT 0,
    posted_projection_difference_nano bigint NOT NULL DEFAULT 0,
    asset_projection_difference_nano bigint NOT NULL DEFAULT 0,
    authorization_projection_difference_nano bigint NOT NULL DEFAULT 0,
    successful_calls_without_settlement bigint NOT NULL DEFAULT 0,
    settlements_without_ledger_transaction bigint NOT NULL DEFAULT 0,
    c2c_quantity_violations bigint NOT NULL DEFAULT 0,
    c2c_hold_violations bigint NOT NULL DEFAULT 0,
    notes jsonb NOT NULL DEFAULT '{}'::jsonb,
    checked_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(notes) = 'object')
);

CREATE INDEX ops_inspections_checked_at_idx ON ops_inspections(checked_at DESC, id);

-- +goose Down
DROP TABLE ops_inspections;
