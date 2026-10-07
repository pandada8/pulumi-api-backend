# Copying DIY state without cutting over

Use a copy rehearsal before changing a live deployment backend. Read the source
with `pulumi stack ls -a --json`; without `-a`, the list only covers the current
project. Export each fully qualified stack with `pulumi stack export --stack
SOURCE --file FILE`. Keep exports in a private directory (0700), with files 0600.
An encrypted state can still contain sensitive ordinary properties.

Organize exports as `exports/PROJECT/STACK.source.json`. For legacy short names,
derive PROJECT from the resource URNs rather than the current working directory.
Keep original stack/project names, URNs, IDs, secrets provider and pending
operations. Empty modern stacks use the project from their qualified name.

The destination org must already exist and the token must have write access.
The credentials file is a 0600 JSON containing `url` and `cli_token` (the deployment
credentials file may contain additional fields). This tool reads only these two
fields, requires verified HTTPS, and does not forward credentials on redirects.

```sh
# Read-only plan; no source or destination writes.
python3 scripts/copy-state.py \
  --exports PRIVATE_EXPORT_DIRECTORY \
  --credentials PRIVATE_CREDENTIALS_JSON \
  --org TARGET_ORG --report PRIVATE_PLAN_JSON

# Create new standby stacks and copy encrypted current state.
python3 scripts/copy-state.py \
  --exports PRIVATE_EXPORT_DIRECTORY \
  --credentials PRIVATE_CREDENTIALS_JSON \
  --org TARGET_ORG --report PRIVATE_RESULT_JSON --apply
```

The tool supports schema v3 with passphrase secrets (or no secrets provider).
It checks URN identities, adds standby-copy tags, creates an empty stack, and
uploads the original state through the import API. It verifies both current
export and version 1 against the complete source JSON, including ciphertext and
pending operations. It never runs a cloud program or overwrites an unrelated
existing stack. A retry can resume its own tagged version-0 empty stack, or
verify its identical version-1 copy. Failed copies have a nonzero exit status
and a per-stack result; retry a directory containing only those exports.

The create and import are separate transactions. A failure can leave a tagged
empty destination that needs a retry. Do not run deployments against these
standby stacks while copying or retrying them. Tags describe the role; they do
not enforce a write prohibition.

Unlike the upstream CLI `stack import`, raw API import preserves pending
operations. The CLI's `SaveSnapshot` explicitly clears them. Do not discard
pending state merely to make a migration appear clean: investigate those stacks
against real resources before enabling deployment.

Verify read/decryption using the existing CLI in an isolated `PULUMI_HOME`, with
the target backend/token supplied only to its process. `stack export
--show-secrets` can check the passphrase in memory; avoid printing or saving its
plaintext output. A raw copy does not change the secrets provider to the service
provider: the original passphrase is still needed. Service decrypt permissions
do not replace possession of that external passphrase.

Full checkpoint/import requests allow 64 MiB on the wire and 128 MiB after
expansion. Gzip is used for large imports; some encrypted states compress poorly.
Other request limits remain unchanged. Larger states need a separate plan.

Before an actual cutover:

- Preserve org naming when programs hardcode `organization/project/stack`
  StackReferences, or explicitly adapt and verify references. Copying to a
  rehearsal org does not rewrite embedded references or application code.
- Restore project configuration and encryption salt from the authoritative
  configuration source. State exports do not include `Pulumi.STACK.yaml`, CI
  credentials, programs, or the original deployment history. The destination
  starts with its own import version 1.
- Confirm passphrases, resolve pending operations, and check provider versions,
  account context and deployment settings. Keep deployment credentials separate
  from backend tokens.
- Pause source writers, take a fresh final copy, verify it, then switch CLI/CI
  backend settings. Use a reviewed preview before writing. Never operate two
  independent state copies against the same real resources concurrently.

Copying while source deployments continue produces one captured snapshot per
stack, not a globally atomic backup or an automatically synchronized replica.
Keep the OSS/DIY source authoritative until a separate cutover is approved.
