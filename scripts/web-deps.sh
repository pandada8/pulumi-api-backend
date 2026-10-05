#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ! -x .dev/web-venv/bin/playwright ]]; then
 if command -v uv >/dev/null; then
  uv venv --clear .dev/web-venv
  uv pip install --python .dev/web-venv/bin/python playwright==1.56.0
 else
  python3 -m venv .dev/web-venv
  .dev/web-venv/bin/pip install playwright==1.56.0
 fi
fi
.dev/web-venv/bin/playwright install chromium
