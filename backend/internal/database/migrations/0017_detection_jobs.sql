-- +goose Up
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='60s';
-- Typed task snapshots contain only plan/parameter/assertion metadata. Neither
-- credentials nor protocol bodies have a column or an accepted JSON field.
CREATE TABLE detection_tasks (
 id uuid PRIMARY KEY REFERENCES market_report_bindings(id),
 owner_id uuid NOT NULL REFERENCES accounts(id),
 state text NOT NULL CHECK(state IN ('queued','running','finished','cancelled','interrupted','suite_pending','failed','outdated')),
 complete boolean NOT NULL DEFAULT false,
 metadata_schema text NOT NULL DEFAULT 'detection.job.v1' CHECK(metadata_schema='detection.job.v1'),
 metadata jsonb NOT NULL CHECK(jsonb_typeof(metadata)='object' AND octet_length(metadata::text)<=4194304),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(NOT complete OR state='finished')
);
CREATE INDEX detection_owner_recent ON detection_tasks(owner_id,created_at DESC,id);
CREATE INDEX detection_unfinished ON detection_tasks(state,id) WHERE state IN ('queued','running');
CREATE TABLE detection_attempts (
 id uuid PRIMARY KEY,
 task_id uuid NOT NULL REFERENCES detection_tasks(id),
 endpoint_id text NOT NULL CHECK(char_length(endpoint_id) BETWEEN 1 AND 200),
 method text NOT NULL CHECK(method IN ('GET','POST','PATCH','DELETE')),
 path_template text NOT NULL CHECK(char_length(path_template) BETWEEN 1 AND 512),
 status integer NOT NULL CHECK(status=0 OR status BETWEEN 100 AND 599),
 reason text NOT NULL CHECK(reason IN ('ok','unsupported','assertion_failed','prerequisite_failed','external_error','detector_error','not_observed','client_disabled','platform_unavailable','cancelled','interrupted','invalid_response','body_limit','timeout','network_error','invalid_request','key_unavailable','inputs_changed','suite_pending')),
 category text NOT NULL CHECK(category IN ('generate','count','query','cleanup')),
 duration_ms bigint NOT NULL CHECK(duration_ms>=0),
 sent boolean NOT NULL,
 response_received boolean NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK(NOT response_received OR status BETWEEN 100 AND 599)
);
ALTER TABLE detection_attempts ADD CONSTRAINT detection_attempt_owner UNIQUE(id,task_id);
CREATE INDEX detection_attempt_task ON detection_attempts(task_id,created_at,id);
CREATE TRIGGER detection_attempt_immutable BEFORE UPDATE OR DELETE ON detection_attempts FOR EACH ROW EXECUTE FUNCTION market_append_only();

CREATE TABLE detection_artifacts (
 id uuid PRIMARY KEY,
 task_id uuid NOT NULL REFERENCES detection_tasks(id),
 kind text NOT NULL CHECK(kind IN ('response','conversation','cache','stored_chat')),
 native_id text NOT NULL CHECK(octet_length(native_id) BETWEEN 1 AND 256),
 delete_endpoint text NOT NULL CHECK(char_length(delete_endpoint) BETWEEN 1 AND 200),
 delete_path text NOT NULL CHECK(octet_length(delete_path) BETWEEN 1 AND 2048),
 producer_attempt_id uuid NOT NULL,
 removed boolean NOT NULL DEFAULT false,
 reason text NOT NULL DEFAULT 'ok' CHECK(reason IN ('ok','external_error','invalid_response','key_unavailable')),
 FOREIGN KEY(producer_attempt_id,task_id) REFERENCES detection_attempts(id,task_id)
);
CREATE INDEX detection_artifact_task ON detection_artifacts(task_id,id);

CREATE TABLE detection_results (
 id uuid PRIMARY KEY,
 task_id uuid NOT NULL REFERENCES detection_tasks(id),
 scenario_id text NOT NULL CHECK(char_length(scenario_id) BETWEEN 1 AND 200),
 state text NOT NULL CHECK(state IN ('pass','partial','fail','unsupported','inconclusive')),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object' AND octet_length(result::text)<=1048576),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX detection_result_task ON detection_results(task_id,created_at,id);
CREATE TRIGGER detection_result_immutable BEFORE UPDATE OR DELETE ON detection_results FOR EACH ROW EXECUTE FUNCTION market_append_only();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM detection_tasks) THEN
  RAISE EXCEPTION 'detection provenance cannot be discarded by downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE detection_results,detection_artifacts,detection_attempts,detection_tasks;
