#!/usr/bin/env python3
import os,pathlib,secrets
root=pathlib.Path(__file__).resolve().parent.parent
os.chdir(root);p=pathlib.Path('.dev');p.mkdir(mode=0o700,exist_ok=True)
key=p/'master.key'
if not key.exists():
    with key.open('xb') as f:f.write(secrets.token_bytes(32))
    key.chmod(0o600)
if not pathlib.Path('.env').exists():
    with pathlib.Path('.env').open('x') as f:f.write(f'DB_PASSWORD={secrets.token_hex(24)}\nLOCAL_UID={os.getuid()}\nLOCAL_GID={os.getgid()}\nBACKEND_PUBLIC_URL=http://localhost:8080\nBACKEND_ENABLE_JOURNAL=false\n')
    pathlib.Path('.env').chmod(0o600)
