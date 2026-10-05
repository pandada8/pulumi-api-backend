#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
expected=21bf19ba40dba5dce0f565e8c614b5d8aa4dbb17
actual=$(git -C third_party/pulumi rev-parse HEAD)
[[ "$actual" = "$expected" ]] || { echo "Pulumi SHA mismatch" >&2; exit 1; }
mkdir -p bin test-results
(cd third_party/pulumi/pkg && go build -ldflags '-X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=3.246.0' -o ../../../bin/pulumi ./cmd/pulumi)
root="$PWD"
(cd third_party/pulumi/sdk/go/pulumi-language-go && go build -ldflags '-X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=3.246.0' -o "$root/bin/pulumi-language-go" .)
python3 - <<'PY' 
import json,subprocess
json.dump({'pulumiSHA':subprocess.check_output(['git','-C','third_party/pulumi','rev-parse','HEAD'],text=True).strip(),'go':subprocess.check_output(['go','version'],text=True).strip(),'cliVersion':subprocess.check_output(['bin/pulumi','version'],text=True).strip()},open('test-results/build.json','w'),indent=2)
PY
