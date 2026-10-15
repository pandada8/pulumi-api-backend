"""Schema v3/v4 compatibility through the real API and PostgreSQL."""
import json
import uuid


def run(c, c2, env, command, container, deployment):
    s = c.stack()
    original = c.call('GET', s + '/export')
    original_revision = c.call('GET', s + '/revisions')['head']
    name = s.split('/')[-1]
    urn = f'urn:pulumi:{name}::wire::pkg:index:Thing::r'
    r = dict(urn=urn, type='pkg:index:Thing', custom=True, id='r', viewOf=urn,
             replaceWith=[urn], taint=True, refreshBeforeUpdate=True,
             resourceHooks={'beforeCreate': ['hook']}, inputs={'plain': urn})
    d = deployment('v4')
    d.update(version=4, features=['views', 'replaceWith', 'taint', 'refreshBeforeUpdate', 'hooks', 'snippets-prototype'])
    d['deployment']['resources'] = [r]
    d['deployment']['snippets'] = [dict(name='snippet', type='pkg:index:Thing', code='value = 42',
                                     descriptor={'name': 'pkg'}, references={'r': urn})]
    # Import preserves even whitespace and the exact input bytes.
    raw = json.dumps(d, indent=2).encode()
    imported = c.call('POST', s + '/import', raw=raw)['updateId']
    assert c2.call('GET', s + '/export') == d
    sql = f"SELECT schema_version=4 AND raw_bytes=decode('{raw.hex()}','hex') FROM snapshots WHERE id=(SELECT snapshot_id FROM stack_heads WHERE stack_id=(SELECT stack_id FROM stack_names WHERE name='{name}' AND is_current));"
    assert command(['docker', 'exec', container, 'psql', '-U', 'postgres', '-d', 'backend', '-Atc', sql]).stdout.strip() == b't'
    c.call('POST', s + '/import', dict(d, features=['unknown-feature']), status=422)
    c.call('POST', s + '/import', dict(d, version=3, features=[]), status=422)
    # Full checkpoint retains its envelope; invalid-only writes preserve the head.
    u, t, _ = c.update(s)
    c.call('PATCH', u + '/checkpoint', d, lease=t, status=204)
    c.call('PATCH', u + '/checkpoint', dict(d, features=['unknown-feature']), lease=t, status=422)
    c.call('PATCH', u + '/checkpoint', {'version': 4, 'isInvalid': True}, lease=t, status=204)
    assert c2.call('GET', s + '/export') == d
    c.call('PATCH', u + '/checkpoint', d, lease=t, status=204)
    c.call('POST', u + '/complete', {'status': 'succeeded'}, lease=t, status=204)
    # Mixed schema history activation in both directions.
    tree = c.call('GET', s + '/revisions')
    c.call('POST', s + '/activate', dict(target=original_revision, expectedHead=tree['head'], expectedEpoch=0, requestID=str(uuid.uuid4()), reason='v3 rollback'))
    assert c.call('GET', s + '/export') == original
    c.call('POST', s + '/activate', dict(target=imported, expectedHead=original_revision, expectedEpoch=1, requestID=str(uuid.uuid4()), reason='v4 restore'))
    assert c.call('GET', s + '/export') == d
    # Journal replay and durable materialization retain all six features and snippets.
    ju, jt, _ = c.update(s, journal=True)
    r = dict(r, outputs={'value': 'updated'})
    entry = dict(version=1, kind=4, sequenceID=1, operationID=1, removeOld=0, state=r)
    c.call('PATCH', ju + '/journalentries', {'entries': [entry]}, lease=jt, status=204)
    before = c.call('GET', s + '/export')
    assert before['version'] == 4 and set(before['features']) == set(d['features'])
    assert before['deployment']['snippets'] == d['deployment']['snippets']
    c.call('POST', ju + '/complete', {'status': 'succeeded'}, lease=jt, status=204)
    jobs = int(command(['docker', 'exec', container, 'psql', '-U', 'postgres', '-d', 'backend', '-Atc', "SELECT count(*) FROM jobs WHERE status='pending'"]).stdout.strip())
    for _ in range(jobs):
        command(['bin/backend', 'worker', '--once'], env=env)
    assert c2.call('GET', s + '/export') == before
    assert c.call('GET', s + '/export/1') == d
    new_name = 'renamed' + uuid.uuid4().hex[:10]
    c.call('POST', s + '/rename', {'newName': new_name, 'newProject': 'wire'}, status=204)
    renamed = c.call('GET', '/api/stacks/demo/wire/' + new_name + '/export')
    new_urn = urn.replace('urn:pulumi:' + name + '::', 'urn:pulumi:' + new_name + '::')
    resource = renamed['deployment']['resources'][0]
    assert resource['urn'] == new_urn and resource['viewOf'] == new_urn and resource['replaceWith'] == [new_urn]
    assert resource['inputs']['plain'] == urn
    assert renamed['deployment']['snippets'][0]['references']['r'] == new_urn
    print('Schema v4 import/checkpoint/journal/materialization/mixed activation/rename: PASS')
