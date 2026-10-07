# Operations

`backend` provides `serve`, `worker`, and `migrate`; `worker --once` processes one durable materialization job and sweeps expired/revoked updates. Migrations hold a PostgreSQL advisory lock and repeat as a no-op. Serve requires migration version 3 and never migrates automatically.

Configuration: `BACKEND_DATABASE_URL`, `BACKEND_PUBLIC_URL`, `BACKEND_MASTER_KEY_FILE` are required. The key must contain exactly 32 random bytes, mode 0600, and be readable by the container UID. `BACKEND_CONSOLE_URL` defaults to PUBLIC_URL. LISTEN defaults to `:8080`. URLs are origin URLs without paths. HTTP requires `BACKEND_DEV_HTTP=true`. Journal negotiation defaults off. `BACKEND_ENABLE_DELTA=true` fails startup. DATABASE_URL is never logged. The process uses 20 database connections; provision PostgreSQL connection capacity for all instances and workers.

Development Compose sets DEV_HTTP. For deployment, terminate TLS at a reverse proxy, set both URLs to HTTPS and DEV_HTTP=false, restrict direct backend and metrics access to your network, and mount the master key read-only. `/healthz` checks process liveness, `/readyz` checks schema/database readiness, `/metrics` provides bounded-route request counters, failures, and latency histograms. The metric endpoint exposes no stack names, URNs, or tokens. Request logs contain request ID, method, and duration only.

`backendctl` is a local database administrator tool, requiring the same database/master-key configuration. Administrative authorization is filesystem/database access; it is not exposed as a public API. Examples:

```sh
bin/backendctl bootstrap --org demo --user admin --token-file .dev/admin.token
bin/backendctl token create --user admin --write --decrypt --out .dev/cli.token
bin/backendctl member set --org demo --user alice --role reader --decrypt=false
bin/backendctl token revoke --id TOKEN_UUID
bin/backendctl restore-generation
```

Token plaintext is written only to a new 0600 file; commands never print it. Membership and token permissions are intersected: writers/admins still need token write permission, and decryption requires both membership and token decryption permission. Bootstrap preserves existing organizations, users, and membership and issues a new token when its target file is absent. Bootstrap refuses to overwrite an existing token file. Token creation failure after a database commit can leave an unused token row; revoke it rather than assuming file output is a transaction with PostgreSQL. Stack keys use AES-256-GCM wrapped by a purpose-derived file KEK. Keep the master key separately from database backups. Automatic key rotation and managed KMS are not implemented.

Updates have 300-second leases, fencing, source-token/membership revalidation, and recovery-generation binding. The worker sweeps every five seconds; requests also check expiry before accessing their stack. Cancellation does not stop external cloud API calls or the user's CLI process. Successful checkpoint/journal/event responses follow synchronous database commits. PostgreSQL must keep `fsync=on` and `synchronous_commit=on`; never use unlogged state tables. Full checkpoint uploads lack sequence IDs and assume the upstream single-writer ordering. A failed update preserves confirmed partial state and pending operations.

Journal ACKs insert immutable entries and publish a head watermark. A disposable reference cache is published only on commit and rebuilt when another instance advances its watermark. Exports replay the fixed base and committed journal with the pinned upstream replayer. Completion queues durable materialization; export/history do not depend on a worker finishing first. Jobs use `SKIP LOCKED`, 120-second claims and bounded exponential retries; final publication checks the update watermark before compacting the head. No journal/snapshot online GC is performed. Monitor database size and failed jobs. The current materializer runs replay while holding the stack lock; sufficiently large stacks can contend with Start and reach the five-second lock timeout. Reference caches are cleared at completion and discarded after failed transactions.

Backup from the repository root:

```sh
scripts/backup.sh ./backup-YYYYMMDD
```

This writes a custom-format logical database dump and a manifest with schema, code SHA, dump hash, and master-key identifier. Back up the key separately through a controlled channel. Stop incoming deployment writes before restoring. Restore only into an empty isolated database/container whose name begins `pulumid-restore-`; configure BACKEND_DATABASE_URL for that target and BACKEND_MASTER_KEY_FILE for its separately restored key, then:

```sh
scripts/restore.sh ./backup-YYYYMMDD pulumid-restore-EXAMPLE --isolated
```

The script verifies the manifest, restores, rotates the service generation, and freezes old running updates before any traffic is admitted. Verify authoritative exports and old secret decryption on the isolated target before switching traffic. The automated test does this in a separate database and confirms the source is untouched. A logical backup is a point-in-time view of backend records; rolling it back does not roll back cloud resources. Reconcile against actual cloud resources if a stale backup is used.

Stop development services with `docker compose down`; this keeps the database volume. `docker compose down -v` deletes that database and is not a routine shutdown command. No service is left running by automated tests. Root-key loss prevents service secrets decryption; backing up only PostgreSQL is insufficient.

Schema v2 and state activation: see `version-tree.md`. Stop writers/API/worker before migrating; active updates cause migration to fail. Restore with the bundled script requires schema 3; migrate older dumps in an isolated database first.

Schema v3 stores a resource count on each snapshot and current head. Existing
snapshots are parsed once during migration; their bytes and hashes are preserved.
Imports, checkpoints, journal publications, materialization and state activation
update counts in the same transaction as the head. Stack listing reads metadata
without loading full states. Legacy journal heads use replay until materialized;
new journal heads carry counts. Stop API/worker before migrating.
