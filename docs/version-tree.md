# Immutable state history and activation

The backend preserves a tree of completed state revisions. A -> B -> C can be
activated at B; the next completed write D has parent B, and C remains readable.
Activation changes backend state only. It does not undo cloud API calls, restore
source code, change cloud credentials, or roll back local Pulumi configuration.

## Operator commands

Set `PULUMI_BACKEND_URL` and `PULUMI_ACCESS_TOKEN` through your normal secret
injection mechanism. Use a token with both write and decrypt permission to
activate; read-only tokens can inspect history and compare revisions.

```sh
python3 scripts/history.py --stack org/project/stack tree
python3 scripts/history.py --stack org/project/stack diff REVISION_A REVISION_B
python3 scripts/history.py --stack org/project/stack activate REVISION_B \
  --expected-head REVISION_C --expected-epoch 0 \
  --request-id YOUR_NEW_UUID --reason 'Recover state before failed deployment'
python3 scripts/history.py --stack org/project/stack activations
```

Copy `head` and `activationEpoch` from the tree response. Supply a new UUID for a
new activation; reuse the UUID **and all original arguments** for a retry after a
network error. The script generates and prints a UUID if omitted. An idempotent
retry returns the original activation result, never moves a later head back.
A successful response's `activatedRevision` is the original operation's target,
not a guarantee about the current head after other operations.

Tree pages contain up to 100 nodes; follow `hasMore` / `nextOffset` using
`tree --offset N`. Parents may be on another page. Activation audit pages also
accept `--offset N`, with up to 100 entries each. Revisions include their originating
operation version (root=0). An activation creates an operation but no duplicate
state node.

After activation, start a **new** CLI invocation and preview. Discard old saved
plans. Review actual resource IDs and consider refresh. Refresh only reconciles
tracked resources; it cannot discover every resource created on the abandoned
branch. A refresh that persists state creates another node on the selected branch.
Do not assume the next up will reproduce the old deployment's cloud resources.

## Version and state semantics

* State nodes are append-only (database triggers reject update/delete).
* Operation history is linear and its public integer version never decreases.
  Example: v1=A, v2=B, v3=C, v4=activate B, v5=D. Export v3 still returns C;
  export v4 returns B. `stacks.last_version` remains the operation counter.
* Activation appears as `import` in Pulumi's existing history protocol. Its
  immutable audit row records source/target nodes, actor token, reason, request ID,
  expected epoch, timestamp and resulting operation ID/version.
* Config metadata is explicitly retained from the latest completed operation;
  tags and local project/config files are unchanged. The activation history message
  identifies the target. This is **state-only** activation, not environment rollback.
* Only finalized operation boundaries are nodes in this release. Running head
  checkpoints may advance while `current_revision` still names the last finalized
  node. Intermediate checkpoints are not individually navigable revisions.
* Invalid checkpoints and states with pending operations cannot be activated.
  Resource URNs must match the current project/stack name, so pre-rename resources
  require explicit repair/import instead. Targets must belong to the same stack.
* Service-managed secrets are validated against retained stack keys. External
  secrets providers remain dependent on the user's original external key/provider;
  the backend cannot verify availability of an external KMS or passphrase.
* Deleted stack rows/history remain stored, but this release does not expose
  tombstoned stacks through normal routes or support undeleting them.
* Diff reports resource actions and changed JSON paths, not secret values.
  Missing/null are distinct. Ciphertext changes are not proof of plaintext changes.

## API

All routes are under `/api/stacks/{org}/{project}/{stack}` and use the existing
`Authorization: token ...` authentication and current org membership checks:

| Method / suffix | Result |
| --- | --- |
| GET `/revisions?offset=0` | Head, activation epoch, active update, nodes and pagination |
| GET `/revisions/{uuid}` | Raw historical deployment, like existing state export |
| GET `/revision-diff?from={uuid}&to={uuid}` | Resource actions and changed paths |
| GET `/activations?offset=0` | Chronological activation audit |
| POST `/activate` | Atomic, idempotent state activation |

Activation JSON:

```json
{
  "target": "revision-uuid",
  "expectedHead": "current-revision-uuid",
  "expectedEpoch": 0,
  "requestID": "idempotency-uuid",
  "reason": "Recovery reason"
}
```

409 means active update, stale expected head/epoch, already-current target, or
conflicting idempotency key. 422 means the historical state cannot safely be
activated. No forced takeover endpoint is provided.

## Concurrency and materialization

Activation holds the existing stack row lock. Snapshot selection, audit insertion,
new import history, head switch, fencing and epoch increment commit together.
CreateUpdate captures activation epoch; Start rejects operations created before a
subsequent activation. Running updates prohibit activation. Existing lease, status,
fence and generation checks reject stale checkpoints/journal and lease renewals.
These checks cannot stop cloud calls already issued by an old CLI process.

Journal revisions bind immutable base + update + high watermark. Replaying that
representation is deterministic: read-time manifest timestamps are replaced by
the base manifest time, and pending operations are sorted. Worker materialization
cannot move a head that no longer matches its original journal/watermark. Full
snapshot activations reuse stored bytes; journal targets are materialized in the
activation transaction. Large histories may hit the 30-second transaction timeout;
no latency or storage savings are claimed without production measurements.

## Schema v2 upgrade / recovery

1. Back up PostgreSQL and retain the matching master key. Record the running binary.
2. Quiesce all deployment clients and stop API/worker processes. Migration refuses
   stacks with active updates; it does not silently cancel them.
3. Run the new `backend migrate` once, then again to verify idempotency.
4. Start the new API/worker; verify readiness, historical exports and CLI operations.

Migration backfills the existing **linear** completed-update history, retaining
public version numbers. The root references the oldest retained snapshot; this is
best-effort legacy initialization, not reconstruction of every previous checkpoint.
Updates that are only `created` can subsequently start under epoch 0; operators
must keep clients stopped during upgrade and restart CLI operations afterwards.

Old binaries reject schema v2. Do not roll back only the executable after migration.
Prefer a forward fix; restoring a v1 database backup requires traffic isolation,
matching keys, recovery generation rotation and reconciliation of any newer cloud
changes. Backup/restore scripts now label and require schema 2.

Storage is still PostgreSQL only. No delta protocol, GC, retention pruning or OSS
archive is enabled by this change; abandoned branches continue occupying storage.
