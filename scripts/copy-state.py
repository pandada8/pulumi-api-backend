#!/usr/bin/env python3
"""Copy encrypted DIY exports into new backend stacks without rewriting state.

Default is a read-only plan. Never overwrites an unrelated/existing deployment.
No cloud provider or source-backend write is performed by this script.
"""
import argparse
import concurrent.futures
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.error
import urllib.parse
import urllib.request


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'),
                                     ensure_ascii=False).encode()).hexdigest()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--exports', type=Path, required=True, help='Private directory containing *.source.json')
    p.add_argument('--credentials', type=Path, required=True, help='0600 JSON containing url and cli_token')
    p.add_argument('--org', required=True)
    p.add_argument('--project', help='Override project; otherwise use each export subdirectory')
    p.add_argument('--workers', type=int, default=4)
    p.add_argument('--apply', action='store_true')
    p.add_argument('--report', type=Path, required=True, help='Sanitized result; contains no state values')
    a = p.parse_args()
    os.umask(0o077)
    for name in [a.org] + ([a.project] if a.project else []):
        if not re.fullmatch(r'[A-Za-z0-9_.-]+', name) or name in ('.', '..'):
            p.error('Invalid org/project name')
    if a.credentials.stat().st_mode & 0o077:
        p.error('Credentials must not be accessible to group/others')
    credentials = json.loads(a.credentials.read_text())
    url, token = credentials['url'].rstrip('/'), credentials['cli_token']
    origin = urllib.parse.urlsplit(url)
    if (origin.scheme != 'https' or not origin.netloc or origin.username or origin.password
            or origin.query or origin.fragment or origin.path not in ('', '/')):
        p.error('Destination must be a verified HTTPS origin without embedded credentials')
    opener = urllib.request.build_opener(NoRedirect)

    def request(method, path, body=None, missing=False):
        headers = {'Authorization': 'token ' + token, 'Content-Type': 'application/json', 'Accept': 'application/vnd.pulumi+9'}
        if body is not None and len(body) > 65536:
            body = gzip.compress(body, compresslevel=1)
            headers['Content-Encoding'] = 'gzip'
        req = urllib.request.Request(url + path, data=body, method=method, headers=headers)
        try:
            with opener.open(req, timeout=120) as r:
                raw = r.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as e:
            if missing and e.code == 404:
                return None
            # Error bodies may contain user data: do not print them.
            raise RuntimeError(f'{method} {path}: HTTP {e.code}') from None

    if not 1 <= a.workers <= 8:
        p.error('--workers must be between 1 and 8')
    files = sorted(a.exports.rglob('*.source.json'))
    if not files:
        p.error('No *.source.json exports found')

    def prepare(f):
        name = f.name.removesuffix('.source.json')
        project = a.project or f.parent.name
        result = {'stack': a.org + '/' + project + '/' + name, 'status': 'planned'}
        try:
            for v in [name, project]:
                if not re.fullmatch(r'[A-Za-z0-9_.-]+', v) or v in ('.', '..'):
                    raise ValueError('Invalid stack/project name')
            raw = f.read_bytes()
            state = json.loads(raw)
            if state.get('version') != 3:
                raise ValueError('Only schema v3 is supported')
            dep = state['deployment']
            if dep.get('secrets_providers', {}).get('type') not in (None, 'passphrase'):
                raise ValueError('Secrets provider needs a separate migration plan')
            if dep.get('features'):
                raise ValueError('Unsupported state features')
            resources, pending = dep.get('resources', []), dep.get('pending_operations', [])
            for r in resources + [op['resource'] for op in pending]:
                urn = r['urn'].split('::')
                if len(urn) != 4 or urn[0] != 'urn:pulumi:' + name or urn[1] != project:
                    raise ValueError('Source URN does not match destination identity')
            path = '/api/stacks/' + '/'.join(urllib.parse.quote(v, safe='') for v in [a.org, project, name])
            metadata = request('GET', path, missing=True)
            sha = digest(state)
            result.update(resources=len(resources), pendingOperations=len(pending),
                          secretProvider=dep.get('secrets_providers', {}).get('type'),
                          sourceFileSHA256=hashlib.sha256(raw).hexdigest(), stateSHA256=sha)
            if metadata is not None:
                if (metadata.get('activeUpdate') or metadata.get('version', 0) > 1
                        or metadata.get('tags', {}).get('migration:state-sha256') != sha):
                    raise ValueError('Destination exists; refusing to overwrite')
                existing = request('GET', path + '/export')
                if metadata.get('version', 0) == 1 and existing != state:
                    raise ValueError('Destination changed; refusing to overwrite')
                if metadata.get('version', 0) == 0 and existing['deployment'].get('resources'):
                    raise ValueError('Unfinished destination changed; refusing to overwrite')
                result['status'] = 'already-copied' if metadata.get('version') == 1 else 'resume-empty-stack'
            return f, path, metadata, result
        except Exception as e:
            result.update(status='failed', error=str(e))
            return f, None, None, result

    with concurrent.futures.ThreadPoolExecutor(max_workers=a.workers) as pool:
        plans = list(pool.map(prepare, files))
    identities = [x[3]['stack'] for x in plans]
    if len(set(identities)) != len(identities):
        p.error('Multiple source exports map to the same destination')
    report = {'destination': url, 'mode': 'copy' if a.apply else 'plan', 'stacks': []}

    def copy(plan):
        f, path, metadata, result = plan
        if result['status'] == 'failed' or not a.apply:
            return result
        try:
            raw = f.read_bytes()
            if hashlib.sha256(raw).hexdigest() != result['sourceFileSHA256']:
                raise ValueError('Source export changed since preflight')
            state = json.loads(raw)
            if metadata is None:
                tags = {'migration:role': 'standby-copy', 'migration:source': 'diy-export',
                        'migration:state-sha256': result['stateSHA256']}
                # The create route has a smaller body limit than import.
                request('POST', path.rsplit('/', 1)[0], json.dumps({
                    'stackName': result['stack'].split('/')[-1], 'tags': tags}).encode())
                metadata = request('GET', path)
            if metadata.get('version', 0) == 0:
                # Upload the original bytes: CLI import clears pending operations.
                request('POST', path + '/import', raw)
            if (request('GET', path + '/export') != state
                    or request('GET', path + '/export/1') != state):
                raise ValueError('Destination verification failed')
            result.update(status='verified-copy', destinationVersion=request('GET', path)['version'])
        except Exception as e:
            result.update(status='failed', error=str(e))
        return result

    with concurrent.futures.ThreadPoolExecutor(max_workers=a.workers) as pool:
        for result in pool.map(copy, plans):
            report['stacks'].append(result)
            if result['status'] == 'failed':
                print(json.dumps(result, ensure_ascii=False), flush=True)
            if len(report['stacks']) % 25 == 0:
                print('Processed', len(report['stacks']), '/', len(plans), flush=True)
            a.report.write_text(json.dumps(report, indent=2, ensure_ascii=False) + '\n')
    summary = {status: sum(x['status'] == status for x in report['stacks'])
               for status in sorted(set(x['status'] for x in report['stacks']))}
    print(json.dumps(summary), flush=True)
    return 1 if summary.get('failed') else 0


if __name__ == '__main__':
    raise SystemExit(main())
