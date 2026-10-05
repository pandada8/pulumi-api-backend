#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ! -f .dev/admin.token ]]; then
 docker compose run --rm -v "$PWD/.dev:/out" --entrypoint /app/backendctl api bootstrap --org demo --user admin --token-file /out/admin.token
fi
if [[ ! -f .dev/cli.token ]]; then
 docker compose run --rm -v "$PWD/.dev:/out" --entrypoint /app/backendctl api token create --user admin --write --decrypt --out /out/cli.token
fi
