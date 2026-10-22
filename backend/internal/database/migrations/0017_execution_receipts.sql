-- +goose Up
-- Receipts prove only already accounted in-call execution units. They are not
-- a resource registry and contain no file, code, request or response bodies.
CREATE TABLE call_execution_receipts (
    receipt_key text PRIMARY KEY CHECK (receipt_key ~ '^[a-f0-9]{64}$'),
    call_id uuid NOT NULL REFERENCES calls(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX call_execution_receipts_call ON call_execution_receipts(call_id);
CREATE TRIGGER call_execution_receipts_immutable BEFORE UPDATE OR DELETE ON call_execution_receipts FOR EACH ROW EXECUTE FUNCTION market_append_only();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM call_execution_receipts) THEN
        RAISE EXCEPTION 'cannot discard retained execution receipts';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE call_execution_receipts;
