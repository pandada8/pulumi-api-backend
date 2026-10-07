#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
out=${1:?usage: scripts/backup.sh OUTPUT_DIRECTORY}
mkdir -p "$out"
chmod 700 "$out"
docker compose exec -T db pg_dump -U backend -d backend -Fc > "$out/database.dump"
python3 - "$out" <<'PY'
import hashlib,json,pathlib,subprocess,sys
out=pathlib.Path(sys.argv[1]);key=pathlib.Path('.dev/master.key').read_bytes()
json.dump({'schema':2,'codeSHA':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'masterKeyID':hashlib.sha256(key).hexdigest(),'databaseSHA256':hashlib.sha256((out/'database.dump').read_bytes()).hexdigest()},open(out/'manifest.json','w'),indent=2)
PY
chmod 600 "$out/database.dump" "$out/manifest.json"
echo 'Database backup complete. Back up the master key through a separate secure channel.'
