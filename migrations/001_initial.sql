BEGIN;
CREATE TABLE schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE service_meta (singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), generation uuid NOT NULL);
CREATE TABLE principals (id uuid PRIMARY KEY, login text NOT NULL UNIQUE, display_name text NOT NULL, disabled boolean NOT NULL DEFAULT false);
CREATE TABLE organizations (id uuid PRIMARY KEY, name text NOT NULL UNIQUE);
CREATE TABLE memberships (
  org_id uuid NOT NULL REFERENCES organizations(id), principal_id uuid NOT NULL REFERENCES principals(id),
  role text NOT NULL CHECK(role IN ('reader','writer','admin')), can_decrypt boolean NOT NULL DEFAULT false,
  PRIMARY KEY(org_id,principal_id)
);
CREATE TABLE api_tokens (
  id uuid PRIMARY KEY, principal_id uuid NOT NULL REFERENCES principals(id), digest bytea NOT NULL UNIQUE CHECK(octet_length(digest)=32),
  can_write boolean NOT NULL, can_decrypt boolean NOT NULL, expires_at timestamptz, revoked_at timestamptz
);
CREATE TABLE stacks (
  id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES organizations(id), tags jsonb NOT NULL DEFAULT '{}',
  last_version bigint NOT NULL DEFAULT 0 CHECK(last_version>=0), fence bigint NOT NULL DEFAULT 0 CHECK(fence>=0),
  active_update_id uuid, deleted_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(org_id,id)
);
CREATE TABLE stack_names (
  org_id uuid NOT NULL, project text NOT NULL, name text NOT NULL, stack_id uuid NOT NULL,
  is_current boolean NOT NULL, PRIMARY KEY(org_id,project,name),
  FOREIGN KEY(org_id,stack_id) REFERENCES stacks(org_id,id)
);
CREATE UNIQUE INDEX one_current_name ON stack_names(stack_id) WHERE is_current;
CREATE TABLE snapshots (
  id uuid PRIMARY KEY, stack_id uuid NOT NULL REFERENCES stacks(id), schema_version integer NOT NULL CHECK(schema_version=3),
  raw_bytes bytea NOT NULL, sha256 bytea NOT NULL CHECK(octet_length(sha256)=32), is_invalid boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(stack_id,id)
);
CREATE TABLE updates (
  id uuid PRIMARY KEY, stack_id uuid NOT NULL REFERENCES stacks(id), actor_token_id uuid NOT NULL REFERENCES api_tokens(id),
  kind text NOT NULL CHECK(kind IN ('update','preview','refresh','destroy','import','rename')),
  dry_run boolean NOT NULL, status text NOT NULL CHECK(status IN ('created','running','succeeded','failed','cancelled')),
  mode text NOT NULL CHECK(mode IN ('full','delta','journal','none')), request jsonb NOT NULL,
  base_snapshot_id uuid, final_snapshot_id uuid, version bigint, fence bigint,
  lease_digest bytea CHECK(lease_digest IS NULL OR octet_length(lease_digest)=32), lease_generation uuid, lease_expires_at timestamptz,
  start_request_digest bytea, start_response_encrypted bytea, complete_status text, reason text,
  journal_count bigint NOT NULL DEFAULT 0 CHECK(journal_count>=0), event_count bigint NOT NULL DEFAULT 0 CHECK(event_count>=0),
  delta_sequence bigint NOT NULL DEFAULT 0 CHECK(delta_sequence>=0), event_closed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(), started_at timestamptz, ended_at timestamptz,
  UNIQUE(stack_id,id),
  FOREIGN KEY(stack_id,base_snapshot_id) REFERENCES snapshots(stack_id,id),
  FOREIGN KEY(stack_id,final_snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE UNIQUE INDEX one_running_update ON updates(stack_id) WHERE status='running';
CREATE UNIQUE INDEX unique_history_version ON updates(stack_id,version) WHERE NOT dry_run AND version IS NOT NULL;
ALTER TABLE stacks ADD CONSTRAINT active_update_belongs_to_stack FOREIGN KEY(id,active_update_id)
  REFERENCES updates(stack_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE stack_heads (
  stack_id uuid PRIMARY KEY REFERENCES stacks(id), generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
  snapshot_id uuid NOT NULL, journal_update_id uuid, journal_upto bigint NOT NULL DEFAULT 0 CHECK(journal_upto>=0),
  CHECK(journal_update_id IS NOT NULL OR journal_upto=0),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id),
  FOREIGN KEY(stack_id,journal_update_id) REFERENCES updates(stack_id,id)
);
CREATE TABLE checkpoint_receipts (
  stack_id uuid NOT NULL, update_id uuid NOT NULL, sequence_number bigint NOT NULL CHECK(sequence_number>0),
  target_sha256 bytea NOT NULL CHECK(octet_length(target_sha256)=32), snapshot_id uuid NOT NULL,
  PRIMARY KEY(update_id,sequence_number),
  FOREIGN KEY(stack_id,update_id) REFERENCES updates(stack_id,id),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE TABLE journal_entries (
  update_id uuid NOT NULL REFERENCES updates(id), sequence_id bigint NOT NULL CHECK(sequence_id>0),
  ingest_order bigint NOT NULL CHECK(ingest_order>0), operation_id bigint NOT NULL CHECK(operation_id>=0),
  payload bytea NOT NULL, digest bytea NOT NULL CHECK(octet_length(digest)=32),
  PRIMARY KEY(update_id,sequence_id), UNIQUE(update_id,ingest_order)
);
CREATE TABLE engine_events (
  update_id uuid NOT NULL REFERENCES updates(id), sequence bigint NOT NULL CHECK(sequence>=0),
  ingest_order bigint NOT NULL CHECK(ingest_order>0), event_type text NOT NULL, urn text, payload jsonb NOT NULL,
  digest bytea NOT NULL CHECK(octet_length(digest)=32), PRIMARY KEY(update_id,sequence), UNIQUE(update_id,ingest_order)
);
CREATE TABLE materializations (
  stack_id uuid NOT NULL, update_id uuid NOT NULL, upto bigint NOT NULL, replayer_version text NOT NULL,
  snapshot_id uuid NOT NULL, PRIMARY KEY(update_id,upto,replayer_version),
  FOREIGN KEY(stack_id,update_id) REFERENCES updates(stack_id,id),
  FOREIGN KEY(stack_id,snapshot_id) REFERENCES snapshots(stack_id,id)
);
CREATE TABLE stack_keys (
  stack_id uuid NOT NULL REFERENCES stacks(id), version integer NOT NULL CHECK(version>0),
  kek_id text NOT NULL, wrapped_key bytea NOT NULL, active boolean NOT NULL,
  PRIMARY KEY(stack_id,version)
);
CREATE UNIQUE INDEX one_active_key ON stack_keys(stack_id) WHERE active;
CREATE TABLE web_sessions (
  digest bytea PRIMARY KEY CHECK(octet_length(digest)=32), token_id uuid NOT NULL REFERENCES api_tokens(id),
  csrf_digest bytea NOT NULL CHECK(octet_length(csrf_digest)=32), expires_at timestamptz NOT NULL
);
CREATE TABLE jobs (
  id uuid PRIMARY KEY, job_key text NOT NULL UNIQUE, kind text NOT NULL, payload jsonb NOT NULL,
  status text NOT NULL CHECK(status IN ('pending','running','done')), attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(), locked_until timestamptz, last_error text
);
CREATE TABLE audit_log (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, at timestamptz NOT NULL DEFAULT clock_timestamp(),
  principal_id uuid, org_id uuid, stack_id uuid, update_id uuid, action text NOT NULL, result text NOT NULL, request_id uuid NOT NULL
);
CREATE INDEX updates_history ON updates(stack_id,version DESC) WHERE NOT dry_run;
CREATE INDEX updates_expiry ON updates(lease_expires_at) WHERE status='running';
CREATE INDEX jobs_pending ON jobs(available_at) WHERE status IN ('pending','running');
INSERT INTO schema_migrations(version) VALUES (1);
COMMIT;
