-- +goose Up
DROP TRIGGER c2c_evidence_limits ON c2c_evidence;
DROP FUNCTION verify_c2c_evidence_limits();
DROP TRIGGER c2c_evidence_guard ON c2c_evidence;
DROP FUNCTION guard_c2c_evidence();
DROP TABLE c2c_evidence;

-- +goose Down
CREATE TABLE c2c_evidence (
    id uuid PRIMARY KEY,
    trade_id uuid NOT NULL REFERENCES c2c_trades(id) ON DELETE RESTRICT,
    uploader_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('payment', 'dispute')),
    mime_type text NOT NULL CHECK (mime_type IN ('image/jpeg', 'image/png')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    key_id text CHECK (key_id IS NULL OR length(trim(key_id)) BETWEEN 1 AND 64),
    nonce bytea CHECK (nonce IS NULL OR octet_length(nonce) = 12),
    ciphertext bytea CHECK (ciphertext IS NULL OR octet_length(ciphertext) > 16),
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK (width::bigint * height::bigint <= 20000000),
    CHECK (
        (deleted_at IS NULL AND key_id IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL)
        OR (deleted_at IS NOT NULL AND key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL)
    )
);

CREATE UNIQUE INDEX c2c_payment_evidence_unique ON c2c_evidence(trade_id) WHERE kind = 'payment';
CREATE INDEX c2c_evidence_trade_idx ON c2c_evidence(trade_id, created_at, id);
CREATE INDEX c2c_evidence_cleanup_idx ON c2c_evidence(created_at, id) WHERE deleted_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION guard_c2c_evidence() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'C2C evidence metadata cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.deleted_at IS NOT NULL OR NEW.id <> OLD.id OR NEW.trade_id <> OLD.trade_id
       OR NEW.uploader_account_id <> OLD.uploader_account_id OR NEW.kind <> OLD.kind
       OR NEW.mime_type <> OLD.mime_type OR NEW.size_bytes <> OLD.size_bytes
       OR NEW.width <> OLD.width OR NEW.height <> OLD.height OR NEW.sha256 <> OLD.sha256
       OR NEW.created_at <> OLD.created_at OR NEW.deleted_at IS NULL
       OR NEW.key_id IS NOT NULL OR NEW.nonce IS NOT NULL OR NEW.ciphertext IS NOT NULL THEN
        RAISE EXCEPTION 'only C2C evidence ciphertext cleanup is allowed' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION verify_c2c_evidence_limits() RETURNS trigger AS $$
DECLARE
    dispute_count bigint;
    buyer_id uuid;
    seller_id uuid;
BEGIN
    IF NEW.kind = 'dispute' THEN
        SELECT buyer_account_id, seller_account_id INTO buyer_id, seller_id FROM c2c_trades WHERE id = NEW.trade_id;
        IF NEW.uploader_account_id NOT IN (buyer_id, seller_id) THEN
            RAISE EXCEPTION 'only trade participants may upload C2C evidence' USING ERRCODE = '23514';
        END IF;
        SELECT count(*) INTO dispute_count FROM c2c_evidence
         WHERE trade_id = NEW.trade_id AND uploader_account_id = NEW.uploader_account_id AND kind = 'dispute';
        IF dispute_count > 5 THEN
            RAISE EXCEPTION 'C2C dispute evidence limit exceeded' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER c2c_evidence_guard BEFORE UPDATE OR DELETE ON c2c_evidence FOR EACH ROW EXECUTE FUNCTION guard_c2c_evidence();

CREATE CONSTRAINT TRIGGER c2c_evidence_limits
AFTER INSERT ON c2c_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_evidence_limits();
