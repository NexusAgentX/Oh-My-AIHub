-- +goose Up
CREATE TABLE market_contributions (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES accounts(id),
 kind text NOT NULL CHECK(kind IN ('supplier','group','group_model','model_id')),
 target_id text NOT NULL CHECK(octet_length(target_id) BETWEEN 1 AND 256),
 title text NOT NULL CHECK(char_length(title) BETWEEN 1 AND 128),
 state text NOT NULL CHECK(state IN ('draft','submitted','rejected','withdrawn','publication_pending','approved')),
 revision bigint NOT NULL CHECK(revision>0),
 document jsonb NOT NULL CHECK(jsonb_typeof(document)='object' AND octet_length(document::text)<=8388608),
 base_document jsonb NOT NULL CHECK(jsonb_typeof(base_document)='object' AND octet_length(base_document::text)<=8388608),
 report_ids uuid[] NOT NULL DEFAULT '{}',
 task_ids uuid[] NOT NULL DEFAULT '{}',
 pending_prices uuid[] NOT NULL DEFAULT '{}',
 review_reason text NOT NULL DEFAULT '' CHECK(char_length(review_reason)<=1000),
 reviewed_by uuid REFERENCES accounts(id),
 submitted_at timestamptz,
 published_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK(state NOT IN ('approved','publication_pending') OR published_at IS NOT NULL),
 CHECK(state<>'submitted' OR submitted_at IS NOT NULL),
 CHECK(cardinality(report_ids)<=1024 AND cardinality(task_ids)<=1024 AND cardinality(pending_prices)<=1024)
);
CREATE INDEX market_contributions_owner ON market_contributions(owner_id,created_at DESC,id DESC);
CREATE INDEX market_contributions_queue ON market_contributions(created_at DESC,id DESC) WHERE state IN ('submitted','publication_pending');
CREATE TRIGGER market_contributions_no_delete BEFORE DELETE ON market_contributions FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE INDEX market_report_bindings_target ON market_report_bindings(target_id,created_at DESC,id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM market_contributions) THEN RAISE EXCEPTION 'cannot discard contribution review history'; END IF;
END $$;
-- +goose StatementEnd
DROP INDEX market_report_bindings_target;
DROP TABLE market_contributions;
