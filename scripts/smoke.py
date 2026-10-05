import base64
import gzip
import hashlib
import json
import os
import time
import uuid
import urllib.request
from pathlib import Path

base = os.environ.get("BACKEND_PUBLIC_URL", "http://localhost:8080").rstrip("/")
token = Path(".dev/cli.token").read_text().strip()

def call(method, path, body=None, lease=None, compressed=False, expected=200):
    headers = {
        "Authorization": ("update-token " + lease) if lease else ("token " + token),
        "Accept": "application/vnd.pulumi+9",
        "Content-Type": "application/json",
    }
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    if compressed:
        assert data is not None
        data = gzip.compress(data)
        headers["Content-Encoding"] = "gzip"
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=60) as response:
        assert response.status == expected, (method, path, response.status)
        raw = response.read()
    return json.loads(raw) if raw else None

who = call("GET", "/api/user")
assert who["githubLogin"]
name = "wire-" + uuid.uuid4().hex[:8]
p = "/api/stacks/demo/wire"
s = p + "/" + name
call("POST", p, {"stackName": name, "tags": {"test": "wire"}})
assert call("GET", s)["version"] == 0

create = call("POST", s + "/update", {
    "name": "wire", "runtime": "go", "main": "", "description": "wire test",
    "config": {},
    "options": {"dryRun": False, "parallel": 1, "color": "raw", "showNames": False},
    "metadata": {"message": "wire smoke", "environment": {}},
})
u = s + "/update/" + create["updateID"]
started = call("POST", u, {"journalVersion": 0, "tags": {"test": "wire"}})
lease = started["token"]
assert started["version"] == 1 and started.get("journalVersion", 0) == 0
assert call("GET", u).get("continuationToken") is not None

deployment = {
    "manifest": {
        "time": "2026-01-01T00:00:00Z", "version": "3.246.0",
        "magic": hashlib.sha256(b"3.246.0").hexdigest(),
    },
    "resources": [], "pending_operations": [],
}
call("PATCH", u + "/checkpoint", {
    "isInvalid": False, "version": 3, "deployment": deployment,
}, lease=lease, compressed=True, expected=204)
assert call("GET", s + "/export")["deployment"] == deployment

plain = base64.b64encode(b"test-secret-value").decode()
cipher = call("POST", s + "/encrypt", {"plaintext": plain})["ciphertext"]
assert call("POST", s + "/decrypt", {"ciphertext": cipher})["plaintext"] == plain
batch = call("POST", s + "/batch-decrypt", {"ciphertexts": [cipher]}, compressed=True)
assert batch["plaintexts"][cipher] == plain
assert call("POST", u + "/renew_lease", {"duration": 300, "token": ""}, lease=lease)["token"] == lease

call("POST", u + "/events/batch", {"events": [{
    "sequence": 0, "timestamp": int(time.time()),
    "stdoutEvent": {"message": "wire smoke", "color": "raw"},
}]}, lease=lease, compressed=True, expected=204)
assert len(call("GET", u + "/events")["events"]) == 1
call("POST", u + "/complete", {"status": "succeeded"}, lease=lease, expected=204)
call("POST", u + "/complete", {"status": "succeeded"}, lease=lease, expected=204)
assert call("GET", s)["activeUpdate"] == ""
assert call("GET", s + "/updates")["updates"][0]["result"] == "succeeded"

exported = call("GET", s + "/export/1")
imported = call("POST", s + "/import", exported, compressed=True)
result = call("GET", s + "/update/" + imported["updateId"])
assert result["status"] == "succeeded" and result.get("continuationToken") is None
call("DELETE", s + "?force=false", expected=204)
print("PASS: identity, stack, full state, lease, secrets, events, history, import")
