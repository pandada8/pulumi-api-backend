BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE stacks IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM stacks WHERE active_update_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Stop or finish all active updates before schema v4 migration';
 END IF;
END $$;
ALTER TABLE snapshots DROP CONSTRAINT snapshots_schema_version_check;
ALTER TABLE snapshots ADD CONSTRAINT snapshots_schema_version_check CHECK(schema_version IN (3,4));
INSERT INTO schema_migrations(version) VALUES(4);
COMMIT;
