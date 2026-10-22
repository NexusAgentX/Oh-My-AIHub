-- +goose Up
-- Separate contributed-channel choices/probe metadata from the legacy model
-- configuration. Existing channel IDs continue to own credentials/call history.
CREATE TABLE market_channels (
 channel_id uuid PRIMARY KEY REFERENCES channels(id),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision>0),
 probe_epoch bigint NOT NULL DEFAULT 1 CHECK (probe_epoch>0),
 address_ids uuid[] NOT NULL CHECK (cardinality(address_ids)<=32),
 overrides jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(overrides)='object')
);
CREATE TABLE market_channel_model_choices (
 channel_id uuid NOT NULL REFERENCES market_channels(channel_id),
 group_model_id uuid NOT NULL REFERENCES market_group_models(id),
 excluded boolean NOT NULL DEFAULT false,
 discount_nano bigint NOT NULL DEFAULT 1000000000 CHECK (discount_nano BETWEEN 0 AND 1000000000000),
 PRIMARY KEY(channel_id,group_model_id)
);
CREATE TABLE market_channel_model_probes (
 channel_id uuid NOT NULL REFERENCES market_channels(channel_id),
 group_model_id uuid NOT NULL REFERENCES market_group_models(id),
 binding text NOT NULL CHECK (binding ~ '^[a-f0-9]{64}$'),
 tests jsonb NOT NULL CHECK (jsonb_typeof(tests)='object'),
 tested_at timestamptz NOT NULL,
 PRIMARY KEY(channel_id,group_model_id)
);

-- +goose StatementBegin
CREATE FUNCTION market_check_channel_links() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid uuid; gid uuid; chosen uuid[];
BEGIN
 IF TG_TABLE_NAME='channels' THEN cid=NEW.id; ELSE cid=NEW.channel_id; END IF;
 SELECT c.market_group_id,mc.address_ids INTO gid,chosen
 FROM channels c JOIN market_channels mc ON mc.channel_id=c.id WHERE c.id=cid AND c.deleted_at IS NULL;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF gid IS NULL OR cardinality(chosen)<>(SELECT count(DISTINCT x) FROM unnest(chosen) x)
  OR EXISTS(SELECT 1 FROM unnest(chosen) x WHERE NOT EXISTS(
   SELECT 1 FROM market_group_addresses a WHERE a.id=x AND a.group_id=gid AND a.deleted_at IS NULL))
  OR EXISTS(SELECT 1 FROM market_channel_model_choices ch JOIN market_group_models m ON m.id=ch.group_model_id WHERE ch.channel_id=cid AND m.group_id<>gid)
 THEN RAISE EXCEPTION 'invalid group channel relationships' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER market_channel_config_links
 AFTER INSERT OR UPDATE ON market_channels DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION market_check_channel_links();
CREATE CONSTRAINT TRIGGER market_channel_owner_links
 AFTER INSERT OR UPDATE ON channels DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION market_check_channel_links();
CREATE CONSTRAINT TRIGGER market_channel_choice_links
 AFTER INSERT OR UPDATE ON market_channel_model_choices DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION market_check_channel_links();

-- Deleted selections are forgotten. Restoring/adding an address later must not
-- silently expand a channel owner's selected set.
-- +goose StatementBegin
CREATE FUNCTION market_forget_channel_address() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
  UPDATE market_channels mc SET address_ids=array_remove(mc.address_ids,NEW.id),revision=revision+1
  FROM channels c WHERE mc.channel_id=c.id AND c.deleted_at IS NULL AND NEW.id=ANY(mc.address_ids);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_channel_address_removed AFTER UPDATE OF deleted_at ON market_group_addresses
 FOR EACH ROW EXECUTE FUNCTION market_forget_channel_address();

-- Keep the persisted anomaly reset boundary consistent with group edits.
-- This runs on publication, never on the inference/settlement path.
-- +goose StatementBegin
CREATE FUNCTION market_touch_group_channels() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.connection_version<>OLD.connection_version THEN
  UPDATE channels SET updated_at=now() WHERE market_group_id=NEW.id AND deleted_at IS NULL;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_channel_group_versioned AFTER UPDATE OF connection_version ON market_groups
 FOR EACH ROW EXECUTE FUNCTION market_touch_group_channels();

-- New calls identify a group offering by their immutable quote snapshot. Keep
-- every existing textual model ID and historical snapshot; removing a legacy
-- model later must not cascade to or block retention of historical calls.
ALTER TABLE calls DROP CONSTRAINT IF EXISTS calls_model_id_fkey;

-- +goose Down
DROP TRIGGER market_channel_group_versioned ON market_groups;
DROP FUNCTION market_touch_group_channels();
DROP TRIGGER market_channel_address_removed ON market_group_addresses;
DROP FUNCTION market_forget_channel_address();
DROP TRIGGER market_channel_choice_links ON market_channel_model_choices;
DROP TRIGGER market_channel_owner_links ON channels;
DROP TRIGGER market_channel_config_links ON market_channels;
DROP FUNCTION market_check_channel_links();
DROP TABLE market_channel_model_probes;
DROP TABLE market_channel_model_choices;
DROP TABLE market_channels;
-- Do not restore a legacy model FK by deleting or rewriting calls. Downgrades
-- cannot make group-offering history belong to the removed global directory.
