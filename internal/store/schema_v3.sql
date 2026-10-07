BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE stacks IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM stacks WHERE active_update_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Stop or finish all active updates before schema v3 migration';
 END IF;
END $$;
ALTER TABLE snapshots ADD COLUMN resource_count bigint CHECK(resource_count >= 0);
-- Read existing payloads once. Keep their bytes and integrity hashes unchanged.
UPDATE snapshots SET resource_count = CASE
 WHEN json_typeof(convert_from(raw_bytes,'UTF8')::json->'deployment'->'resources') = 'array'
 THEN json_array_length(convert_from(raw_bytes,'UTF8')::json->'deployment'->'resources')
 ELSE 0 END;
ALTER TABLE snapshots ALTER COLUMN resource_count SET NOT NULL;
ALTER TABLE stack_heads ADD COLUMN resource_count bigint CHECK(resource_count >= 0);
UPDATE stack_heads h SET resource_count = p.resource_count
 FROM snapshots p WHERE p.id = h.snapshot_id AND h.journal_update_id IS NULL;
-- Legacy journal heads remain NULL until materialization. Listing replays only
-- these heads; new journal publications always carry the candidate's count.
INSERT INTO schema_migrations(version) VALUES(3);
COMMIT;
