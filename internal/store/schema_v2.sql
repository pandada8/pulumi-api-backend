BEGIN;
LOCK TABLE stacks IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM stacks WHERE active_update_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Stop or finish all active updates before schema v2 migration';
 END IF;
END $$;
CREATE TABLE state_revisions (
 id uuid PRIMARY KEY, stack_id uuid NOT NULL REFERENCES stacks(id),
 parent_id uuid, update_id uuid REFERENCES updates(id),
 snapshot_id uuid NOT NULL, journal_update_id uuid, journal_upto bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(stack_id,id),
 FOREIGN KEY(stack_id,parent_id) REFERENCES state_revisions(stack_id,id),
 FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id),
 FOREIGN KEY(stack_id,journal_update_id) REFERENCES updates(stack_id,id)
);
ALTER TABLE stacks ADD COLUMN current_revision uuid;
ALTER TABLE stacks ADD COLUMN activation_epoch bigint NOT NULL DEFAULT 0;
ALTER TABLE stacks ADD FOREIGN KEY(id,current_revision) REFERENCES state_revisions(stack_id,id);
ALTER TABLE updates ADD COLUMN base_revision uuid;
ALTER TABLE updates ADD COLUMN created_epoch bigint NOT NULL DEFAULT 0;
ALTER TABLE updates ADD FOREIGN KEY(stack_id,base_revision) REFERENCES state_revisions(stack_id,id);
CREATE TABLE state_activations (
 stack_id uuid NOT NULL REFERENCES stacks(id), request_id uuid NOT NULL,
 actor_token_id uuid NOT NULL REFERENCES api_tokens(id),
 from_revision uuid NOT NULL, target_revision uuid NOT NULL,
 expected_epoch bigint NOT NULL, update_id uuid NOT NULL REFERENCES updates(id),
 reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(stack_id,request_id),
 FOREIGN KEY(stack_id,from_revision) REFERENCES state_revisions(stack_id,id),
 FOREIGN KEY(stack_id,target_revision) REFERENCES state_revisions(stack_id,id)
);
-- Existing history is linear. Preserve every existing public version number.
INSERT INTO state_revisions(id,stack_id,snapshot_id,created_at)
 SELECT s.id,s.id,(SELECT p.id FROM snapshots p WHERE p.stack_id=s.id ORDER BY p.created_at,p.id LIMIT 1),s.created_at
 FROM stacks s;
INSERT INTO state_revisions(id,stack_id,parent_id,update_id,snapshot_id,journal_update_id,journal_upto,created_at)
 SELECT id,stack_id,COALESCE(lag(id) OVER (PARTITION BY stack_id ORDER BY version),stack_id),id,
 COALESCE(final_snapshot_id,base_snapshot_id),
 CASE WHEN final_snapshot_id IS NULL AND mode='journal' THEN id END,
 CASE WHEN final_snapshot_id IS NULL AND mode='journal' THEN journal_count ELSE 0 END,
 COALESCE(ended_at,created_at)
 FROM updates WHERE NOT dry_run AND status IN ('succeeded','failed','cancelled');
UPDATE updates u SET base_revision=r.parent_id FROM state_revisions r WHERE r.update_id=u.id;
UPDATE stacks s SET current_revision=COALESCE((SELECT u.id FROM updates u WHERE u.stack_id=s.id AND NOT u.dry_run AND u.status IN ('succeeded','failed','cancelled') ORDER BY u.version DESC LIMIT 1),s.id);
CREATE INDEX state_revisions_listing ON state_revisions(stack_id,created_at,id);
CREATE FUNCTION reject_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'History records are immutable';
END $$;
CREATE TRIGGER immutable_revision BEFORE UPDATE OR DELETE ON state_revisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_activation BEFORE UPDATE OR DELETE ON state_activations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
INSERT INTO schema_migrations(version) VALUES(2);
COMMIT;
