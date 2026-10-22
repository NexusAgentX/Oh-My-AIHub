-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Background responses were retired from the gateway: nothing reads or writes
-- these rows, and the unsettled remainder is abandoned on purpose instead of
-- blocking the upgrade. The count is reported so the abandoned spend stays
-- visible in the migration log; no balance, ledger entry or call is adjusted.
-- +goose StatementBegin
DO $$
DECLARE abandoned bigint;
BEGIN
 SELECT count(*) INTO abandoned FROM gateway_background_jobs WHERE settled_at IS NULL;
 IF abandoned > 0 THEN
  RAISE NOTICE 'dropping gateway_background_jobs: abandoning % unsettled background response(s)', abandoned;
 ELSE
  RAISE NOTICE 'dropping gateway_background_jobs: no unsettled background responses';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE gateway_background_jobs;

-- Images billing is a real call format that the baseline check predates. The
-- routing-mode checks already carry the four modes the gateway supports.
ALTER TABLE calls DROP CONSTRAINT calls_format_check;
ALTER TABLE calls ADD CONSTRAINT calls_format_check CHECK(format IN ('openai_chat','openai_responses','anthropic','gemini','openai_images'));

-- +goose Down
-- The abandoned background responses cannot be reconstructed, so the drop is
-- forward-only like the other retired surfaces.
-- +goose StatementBegin
DO $$
BEGIN
 RAISE EXCEPTION 'retired background responses cannot be restored; keep the dropped table and use the documented forward repair boundary' USING ERRCODE='55000';
END $$;
-- +goose StatementEnd
