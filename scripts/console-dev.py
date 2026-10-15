#!/usr/bin/env python3
"""Run the embedded console with an isolated, persistent local development DB."""
import argparse
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
FOLDER = ROOT / '.dev' / 'console'
CONTAINER = 'pulumid-console-dev'
IMAGE = 'postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vite', action='store_true', help='Allow login/logout from the Vite origin on port 5173')
    parser.add_argument('--port', type=int, default=18080)
    parser.add_argument('--database-port', type=int, default=55432)
    args = parser.parse_args()
    os.chdir(ROOT)
    os.umask(0o077)
    FOLDER.mkdir(parents=True, exist_ok=True)
    # Fail before building or touching the DB if another application uses this port.
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', args.port))
    password_file = FOLDER / 'database-password'
    if not password_file.exists():
        password_file.write_text(secrets.token_urlsafe(24))
    key = FOLDER / 'master.key'
    if not key.exists():
        key.write_bytes(secrets.token_bytes(32))
    key.chmod(0o600)
    existing = subprocess.run(['docker', 'inspect', '--format', '{{.State.Running}}', CONTAINER], capture_output=True, text=True)
    if existing.returncode:
        subprocess.run(['docker', 'run', '-d', '--name', CONTAINER,
                        '-e', 'POSTGRES_PASSWORD=' + password_file.read_text().strip(),
                        '-e', 'POSTGRES_DB=pulumid_console', '-p', f'127.0.0.1:{args.database_port}:5432', IMAGE], check=True)
    elif existing.stdout.strip() != 'true':
        subprocess.run(['docker', 'start', CONTAINER], check=True)
    for _ in range(60):
        if subprocess.run(['docker', 'exec', CONTAINER, 'pg_isready', '-U', 'postgres'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            break
        time.sleep(.5)
    else:
        raise RuntimeError('Development database did not become ready')
    for command in [['npm', '--prefix', 'web', 'ci', '--no-fund', '--no-audit'],
                    ['npm', '--prefix', 'web', 'run', 'build'],
                    ['go', 'build', '-o', str(FOLDER / 'backend'), './cmd/backend'],
                    ['go', 'build', '-o', str(FOLDER / 'backendctl'), './cmd/backendctl']]:
        subprocess.run(command, check=True)
    url = f'http://127.0.0.1:{args.port}'
    env = dict(os.environ,
               BACKEND_DATABASE_URL=f'postgres://postgres:{password_file.read_text().strip()}@127.0.0.1:{args.database_port}/pulumid_console?sslmode=disable',
               BACKEND_PUBLIC_URL=url, BACKEND_CONSOLE_URL='http://127.0.0.1:5173' if args.vite else url,
               BACKEND_LISTEN=f'127.0.0.1:{args.port}', BACKEND_DEV_HTTP='true',
               BACKEND_MASTER_KEY_FILE=str(key), BACKEND_ENABLE_JOURNAL='true')
    subprocess.run([str(FOLDER / 'backend'), 'migrate'], env=env, check=True)
    token = FOLDER / 'admin.token'
    if not token.exists():
        subprocess.run([str(FOLDER / 'backendctl'), 'bootstrap', '--org', 'acme', '--user', 'alex', '--token-file', str(token)], env=env, check=True)
    server = subprocess.Popen([str(FOLDER / 'backend'), 'serve'], env=env)
    def stop(*_):
        server.terminate()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        for _ in range(60):
            if server.poll() is not None:
                raise RuntimeError('Development server exited')
            try:
                urllib.request.urlopen(url + '/readyz', timeout=1).close()
                break
            except OSError:
                time.sleep(.25)
        print(f'Console: {url}\nDevelopment token: {token}\nPostgreSQL: {CONTAINER} (127.0.0.1:{args.database_port})', flush=True)
        server.wait()
    finally:
        if server.poll() is None:
            server.terminate()
        server.wait()


if __name__ == '__main__':
    main()
