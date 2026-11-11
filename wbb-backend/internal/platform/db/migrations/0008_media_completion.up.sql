-- Existing operations have no trustworthy MediaMTX epoch. Preserve them as
-- legacy unknowns; only newly inserted rows may acquire the v1 tuple.
ALTER TABLE media_create_operations ADD COLUMN completion_legacy boolean NOT NULL DEFAULT true;
ALTER TABLE media_create_operations ALTER COLUMN completion_legacy SET DEFAULT false;
ALTER TABLE media_create_operations
    ADD COLUMN completion_contract_version text,
    ADD COLUMN completion_root text,
    ADD COLUMN completion_slot text,
    ADD COLUMN completion_epoch text,
    ADD COLUMN completion_epoch_sequence bigint,
    ADD COLUMN completion_source_path text,
    ADD COLUMN completion_sdp_sha256 text,
    ADD COLUMN completion_evidence_kind text,
    ADD COLUMN completion_confirmed_at timestamptz,
    ADD COLUMN completion_revision bigint;

ALTER TABLE media_create_operations ADD CONSTRAINT media_completion_tuple_complete CHECK (
    num_nonnulls(completion_contract_version,completion_root,completion_slot,completion_epoch,
        completion_epoch_sequence,completion_source_path,completion_sdp_sha256) IN (0,7)
    AND (completion_epoch IS NULL OR (
        completion_contract_version='wbb-media-completion/1' AND length(completion_root)>0 AND
        length(completion_slot)>0 AND completion_epoch ~ '^[0-9a-f]{32}$' AND
        completion_epoch_sequence>0 AND completion_source_path ~ '^wbb/[0-9a-f-]{36}/[0-9]+/source$' AND
        completion_sdp_sha256 ~ '^[0-9a-f]{64}$'))
    AND (completion_legacy OR request_started_at IS NULL OR completion_epoch IS NOT NULL)
    AND (NOT completion_legacy OR (completion_epoch IS NULL AND completion_evidence_kind IS NULL))
    AND (completion_evidence_kind IS NULL OR completion_evidence_kind IN ('completed','never_sent'))
    AND (completion_evidence_kind IS DISTINCT FROM 'completed' OR
        (completion_epoch IS NOT NULL AND completion_confirmed_at IS NOT NULL AND completion_revision>=0))
    AND (completion_evidence_kind IS DISTINCT FROM 'never_sent' OR
        (request_started_at IS NULL AND completion_confirmed_at IS NULL))
    AND (completion_legacy OR state<>'closed' OR completion_evidence_kind IS NOT NULL)
);

CREATE FUNCTION guard_media_completion_ownership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.completion_legacy IS DISTINCT FROM OLD.completion_legacy THEN
        RAISE EXCEPTION 'media completion legacy classification is immutable';
    END IF;
    IF OLD.completion_legacy AND NEW.completion_epoch IS NOT NULL THEN
        RAISE EXCEPTION 'legacy media operation cannot acquire an epoch';
    END IF;
    IF OLD.completion_epoch IS NULL AND NEW.completion_epoch IS NOT NULL AND
       (OLD.request_started_at IS NOT NULL OR OLD.state='closed') THEN
        RAISE EXCEPTION 'dispatched or closed media operation cannot acquire an epoch';
    END IF;
    IF OLD.completion_epoch IS NOT NULL AND ROW(NEW.completion_contract_version,NEW.completion_root,
        NEW.completion_slot,NEW.completion_epoch,NEW.completion_epoch_sequence,
        NEW.completion_source_path,NEW.completion_sdp_sha256) IS DISTINCT FROM
        ROW(OLD.completion_contract_version,OLD.completion_root,OLD.completion_slot,
        OLD.completion_epoch,OLD.completion_epoch_sequence,OLD.completion_source_path,
        OLD.completion_sdp_sha256) THEN
        RAISE EXCEPTION 'media completion ownership is immutable';
    END IF;
    IF OLD.completion_evidence_kind IS NOT NULL AND
       ROW(NEW.completion_evidence_kind,NEW.completion_confirmed_at,NEW.completion_revision) IS DISTINCT FROM
       ROW(OLD.completion_evidence_kind,OLD.completion_confirmed_at,OLD.completion_revision) THEN
        RAISE EXCEPTION 'media completion evidence is immutable';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER media_completion_ownership_guard BEFORE UPDATE ON media_create_operations
    FOR EACH ROW EXECUTE FUNCTION guard_media_completion_ownership();
