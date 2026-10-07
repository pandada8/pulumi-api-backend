#!/usr/bin/env python3
"""Inspect immutable history or activate a revision using a backend API token."""
import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--url', default=os.environ.get('PULUMI_BACKEND_URL'))
    p.add_argument('--stack', required=True, help='org/project/stack')
    sub = p.add_subparsers(dest='command', required=True)
    tree = sub.add_parser('tree'); tree.add_argument('--offset', type=int, default=0)
    audit = sub.add_parser('activations'); audit.add_argument('--offset', type=int, default=0)
    diff = sub.add_parser('diff'); diff.add_argument('before'); diff.add_argument('after')
    activate = sub.add_parser('activate')
    activate.add_argument('target')
    activate.add_argument('--expected-head', required=True)
    activate.add_argument('--expected-epoch', required=True, type=int)
    activate.add_argument('--reason', required=True)
    activate.add_argument('--request-id', default=None, help='Reuse the same UUID when retrying')
    a = p.parse_args()
    token = os.environ.get('PULUMI_ACCESS_TOKEN')
    if not token or not a.url: p.error('Set PULUMI_ACCESS_TOKEN and --url / PULUMI_BACKEND_URL')
    if len(a.stack.split('/')) != 3 or any(not v or v in ('.','..') for v in a.stack.split('/')):
        p.error('--stack must be org/project/stack')
    origin=urllib.parse.urlsplit(a.url)
    if origin.scheme not in ('http','https') or not origin.netloc or origin.username or origin.password or origin.query or origin.fragment or origin.path not in ('','/'):
        p.error('--url must be an HTTP(S) origin without credentials, query or path')
    base=a.url.rstrip('/')+'/api/stacks/'+'/'.join(urllib.parse.quote(v,safe='') for v in a.stack.split('/'))
    body=None
    if a.command in ('tree','activations'):
        path=('/revisions' if a.command=='tree' else '/activations')+'?offset='+str(a.offset)
    elif a.command=='diff':
        path='/revision-diff?'+urllib.parse.urlencode({'from':a.before,'to':a.after})
    else:
        request_id=a.request_id or str(uuid.uuid4())
        print('Activation request ID: '+request_id, file=sys.stderr)
        print('Changes backend state only. Cloud resources, code, config and tags are NOT rolled back.',file=sys.stderr)
        path='/activate'
        body=json.dumps(dict(target=a.target,expectedHead=a.expected_head,expectedEpoch=a.expected_epoch,requestID=request_id,reason=a.reason)).encode()
    req=urllib.request.Request(base+path,data=body,headers={'Authorization':'token '+token,'Content-Type':'application/json'})
    # Never forward the API credential to a redirected endpoint.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs): return None
    try:
        with urllib.request.build_opener(NoRedirect).open(req,timeout=120) as r:
            print(json.dumps(json.load(r),indent=2,ensure_ascii=False))
    except urllib.error.HTTPError as e:
        print(f'HTTP {e.code}: '+e.read().decode(),file=sys.stderr);return 1
    except urllib.error.URLError as e:
        print('Request failed: '+str(e.reason),file=sys.stderr);return 1
    return 0


if __name__=='__main__': sys.exit(main())
