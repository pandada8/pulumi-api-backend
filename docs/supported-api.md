# Implemented compatibility

Pinned source: `21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17`, deployment schema 3, journal 1, REST Accept `application/vnd.pulumi+9`. The explicit CLI/manifest build label is 3.246.0. No patch is made to upstream CLI, engine, Go SDK, or language host.

Implemented: user/default organization/capabilities/CLI version; stack list/create/get/delete, project HEAD, tags, current and historical export, import, same-organization rename; create update/preview/refresh/destroy; Start/Renew/Complete/Cancel; full checkpoints, journal entries, structured and legacy events, history/latest; individual and batch service encryption/decryption and audit endpoints; web login/session/CSRF/logout, stack/resource/history and CLI stack/update/preview pages, web data APIs; expiry worker, journal materialization, migration, bootstrap/members/tokens/recovery generation, database backup and isolated restore.

Wire errors are JSON `code,message`. Successful writes with no object return 204; object-returning calls return valid JSON 200. Gzip request decoding, duplicate-key/trailing-value rejection, and endpoint-specific compressed/uncompressed limits are implemented. Stable signed cursors are used for Pulumi API stacks/events, scoped to principal/filter/generation. The console uses the same authorized head resolution and hides recognized secrets/additional secret outputs; arbitrary log text is not automatically scrubbed.

State/lease semantics include stack UUID binding, source-token revocation, database locking across instances, monotonic history versions, dryRun/preview isolation, retry-stable Start/Complete/Renew, cancellation/expiry fencing, and persisted partial state. Journal identity is sequenceID, replay order is server ingestion order, and resource references use operationID/old index. The implementation reuses the pinned upstream `backend.NewJournalReplayer`; no URN-based replacement of that algorithm is used.

Unsupported features return 422 or an unimplemented route 404: schema-v4 resource features, nonempty teams/cloud config, delta checkpoints, remote execution, ESC, policy, OAuth, and cross-org transfer. Only `batch-encrypt` is advertised. Existing journal heads remain readable with negotiation disabled.

Differences from the original design that remain material:

- Core transaction orchestration lives in `internal/store`; handlers do no SQL. `internal/core` contains pure crypto/cursor helpers. This is a smaller package layout than the design document.
- Start, journal cold-reference loading, rename, and materialization hold the stack row lock during preparation/replay. The design's lock-free candidate plus generation retry optimization is not implemented. This is correct under concurrent writers but can return 503 on large-stack contention.
- The console is a read-only server-rendered inspector with resource/history tables and event polling. Web resource/list and Pulumi API pagination are implemented. Resource diffs compare URN+ID+delete marker and show paths without property values. The HTML stack list supports project filtering and signed next-page links.
- Events history derives `resourceChanges` only from a stored engine summary event. Missing statistics are omitted rather than fabricated. Dedicated replay/job/WAL/RSS metrics are not provided by the HTTP metric endpoint.
- Materializer claims do not renew beyond 120 seconds. Database uniqueness/CAS protects publication if a duplicate worker claims an expired job, but long jobs can duplicate computation. Database statement timeout is 30 seconds.
- API benchmarks cover full vs journal storage with a real delay proxy. They are not the fixed provider/CLI/S3-DIY benchmark acceptance matrix, and no S3 superiority claim is made.
- Fault tests cover pre-lock cancellation, before-commit failure/rollback, committed response loss, persisted create-before-CLI-kill, database/API restart, and materialization CAS. The design's entire individually named fault-control matrix and real 300-second renewal-duration test are not implemented.

The repository includes the original implementation specification for traceability; it must not be read as evidence that every design acceptance target has passed. `docs/test-results.md` reports actual executions.
