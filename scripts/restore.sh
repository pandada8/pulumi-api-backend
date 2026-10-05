#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
backup=${1:?usage: scripts/restore.sh BACKUP_DIRECTORY TARGET_CONTAINER --isolated}
target=${2:?target container required}
[[ ${3:-} = --isolated ]] || { echo 'Explicit --isolated required; do not restore into a live database.' >&2; exit 1; }
[[ "$target" = pulumid-restore-* ]] || { echo 'Target must be a dedicated pulumid-restore-* container' >&2; exit 1; }
python3 - "$backup" <<'PY'
import hashlib,json,pathlib,sys
p=pathlib.Path(sys.argv[1]);m=json.loads((p/'manifest.json').read_text())
assert m['schema']==1
assert m['databaseSHA256']==hashlib.sha256((p/'database.dump').read_bytes()).hexdigest()
assert m['masterKeyID']==hashlib.sha256(pathlib.Path(__import__('os').environ['BACKEND_MASTER_KEY_FILE']).read_bytes()).hexdigest()
PY
docker exec -i "$target" pg_restore -U postgres -d backend --exit-on-error < "$backup/database.dump"
bin/backendctl restore-generation
echo 'Restore finished; generation rotated and old running updates fenced. Verify exports and secrets before routing traffic.'
