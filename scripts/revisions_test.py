"""Branch/history regression tests, called by the isolated PostgreSQL harness."""
import concurrent.futures
import json
import uuid


def run(c, c2, env, command, root, container, deployment):
    s = c.stack()
    base = c.call('GET', s + '/revisions')['head']
    ids, states = [], []
    for label in ('A', 'B', 'C'):
        u, t, start = c.update(s)
        d = deployment(label)
        c.call('PATCH', u + '/checkpoint', {'version': 3, 'deployment': d['deployment']}, lease=t, status=204)
        c.call('POST', u + '/complete', {'status': 'succeeded'}, lease=t, status=204)
        ids.append(u.split('/')[-1]); states.append(d)
    tree = c.call('GET', s + '/revisions')
    nodes = {n['id']: n for n in tree['nodes']}
    assert nodes[ids[0]]['parent'] == base and nodes[ids[2]]['parent'] == ids[1]
    stale = c.call('POST', s + '/update', {'name': 'wire', 'options': {}})['updateID']
    req = dict(target=ids[1], expectedHead=ids[2], expectedEpoch=0, requestID=str(uuid.uuid4()), reason='Branch from B')
    # Competing activation transactions: exactly one succeeds against the expected head.
    def activate(client, body):
        import urllib.request, urllib.error
        q=urllib.request.Request(client.url+s+'/activate',data=json.dumps(body).encode(),headers={'Authorization':'token '+client.token,'Content-Type':'application/json'})
        try:r=urllib.request.urlopen(q)
        except urllib.error.HTTPError as e:r=e
        return r.code,json.load(r)
    req2 = dict(req, target=ids[0], requestID=str(uuid.uuid4()))
    # First exercise the deterministic B branch, then a separate CAS race below.
    result=c.call('POST',s+'/activate',req)
    assert result['version']==4
    assert c2.call('GET',s+'/export')==states[1]
    assert c.call('GET',s+'/export/3')==states[2]
    assert c.call('GET',s+'/export/4')==states[1]
    c.call('POST',s+'/update/'+stale,{},status=409)
    c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':states[2]['deployment']},lease=t,status=403)
    c.call('POST',s+'/activate',dict(req,reason='different'),status=409)
    c.call('POST',s+'/activate',req2,status=409)
    # Active updates block activation; D must attach to B, never C or activation's update ID.
    du,dt,ds=c.update(s)
    req3=dict(target=ids[0],expectedHead=ids[1],expectedEpoch=1,requestID=str(uuid.uuid4()),reason='test')
    c.call('POST',s+'/activate',req3,status=409)
    d=deployment('D')
    c.call('PATCH',du+'/checkpoint',{'version':3,'deployment':d['deployment']},lease=dt,status=204)
    c.call('POST',du+'/complete',{'status':'failed'},lease=dt,status=204)
    tree=c.call('GET',s+'/revisions');nodes={n['id']:n for n in tree['nodes']};did=du.split('/')[-1]
    assert nodes[did]['parent']==ids[1] and ids[2] in nodes and tree['head']==did
    assert c.call('GET',s+'/export/5')==d
    assert c.call('GET',s+'/revisions/'+ids[2])==states[2]
    retry=c.call('POST',s+'/activate',req)
    assert retry['replayed'] and retry['version']==4
    assert c.call('GET',s+'/revisions')['head']==did
    audit=c.call('GET',s+'/activations');assert len(audit)==1 and audit[0]['from']==ids[2] and audit[0]['target']==ids[1]
    # Another stack's node and malformed IDs cannot be activated/read.
    other=c.stack();oid=c.call('GET',other+'/revisions')['head']
    rq=dict(req3,expectedHead=did,target=oid)
    c.call('POST',s+'/activate',rq,status=404)
    c.call('GET',s+'/revisions/not-a-uuid',status=400)
    c.call('GET',s+'/revisions?offset=-1',status=400)
    # Race two valid activations from the same head/epoch across API processes.
    race1=dict(req3,expectedHead=did,requestID=str(uuid.uuid4()))
    race2=dict(race1,target=ids[2],requestID=str(uuid.uuid4()))
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        results=list(pool.map(lambda pair:activate(*pair),[(c,race1),(c2,race2)]))
    assert sorted(x[0] for x in results)==[200,409],results
    # Reader and no-decrypt token may inspect ciphertext history but never activate it.
    import pathlib
    out=root/'test-results'/('revision-reader-'+uuid.uuid4().hex)
    command(['bin/backendctl','token','create','--out',out],env=env)
    reader=type(c)(c.url,out.read_text().strip());out.unlink()
    reader.call('GET',s+'/revisions')
    reader.call('POST',s+'/activate',req,status=403)
    # Journal history remains readable before and after materialization and activation.
    js=c.stack();ju,jt,_=c.update(js,journal=True)
    c.call('POST',ju+'/complete',{'status':'succeeded'},lease=jt,status=204)
    jnode=ju.split('/')[-1];jstate=c.call('GET',js+'/revisions/'+jnode)
    jdu,jdt,_=c.update(js);jd=deployment('journal-child')
    c.call('PATCH',jdu+'/checkpoint',{'version':3,'deployment':jd['deployment']},lease=jdt,status=204)
    c.call('POST',jdu+'/complete',{'status':'succeeded'},lease=jdt,status=204)
    jreq=dict(target=jnode,expectedHead=jdu.split('/')[-1],expectedEpoch=0,requestID=str(uuid.uuid4()),reason='journal restore')
    c.call('POST',js+'/activate',jreq)
    command(['bin/backend','worker','--once'],env=env)
    assert c.call('GET',js+'/export')==jstate
    assert c.call('GET',js+'/revisions/'+jnode)==jstate
    # DB rejects mutation of historical nodes (not only an API convention).
    mutation=command(['docker','exec',container,'psql','-U','postgres','-d','backend','-v','ON_ERROR_STOP=1','-c',f"UPDATE state_revisions SET parent_id=NULL WHERE id='{did}'"],check=False)
    assert mutation.returncode!=0
    # Invalid checkpoints, pending operations and pre-rename URNs cannot be activated.
    vs=c.stack();vu,vt,_=c.update(vs)
    c.call('PATCH',vu+'/checkpoint',{'version':3,'isInvalid':True},lease=vt,status=204)
    c.call('POST',vu+'/complete',{'status':'failed'},lease=vt,status=204)
    invalid_id=vu.split('/')[-1]
    c.call('POST',vs+'/import',deployment('repair'))
    vtree=c.call('GET',vs+'/revisions')
    bad=dict(target=invalid_id,expectedHead=vtree['head'],expectedEpoch=0,requestID=str(uuid.uuid4()),reason='invalid')
    c.call('POST',vs+'/activate',bad,status=422)
    pend=deployment('pending');pend['deployment']['pending_operations']=[{'type':'creating','resource':{'urn':'urn:pulumi:'+vs.split('/')[-1]+'::wire::pkg:index:Thing::r','type':'pkg:index:Thing','custom':True}}]
    pu=c.call('POST',vs+'/import',pend)['updateId']
    c.call('POST',vs+'/import',deployment('repaired'))
    vtree=c.call('GET',vs+'/revisions')
    c.call('POST',vs+'/activate',dict(bad,target=pu,expectedHead=vtree['head'],requestID=str(uuid.uuid4())),status=422)
    renamed=deployment('old-name');renamed['deployment']['resources']=[{'urn':'urn:pulumi:other::wire::pkg:index:Thing::r','type':'pkg:index:Thing','custom':True,'id':'res-1'}]
    ru=c.call('POST',vs+'/import',renamed)['updateId']
    c.call('POST',vs+'/import',deployment('current-name'))
    vtree=c.call('GET',vs+'/revisions')
    c.call('POST',vs+'/activate',dict(bad,target=ru,expectedHead=vtree['head'],requestID=str(uuid.uuid4())),status=422)
    # Resource diff reports paths, including missing -> null, without secret values.
    ds=c.stack();d1=deployment();d2=deployment()
    resource={'urn':'urn:pulumi:'+ds.split('/')[-1]+'::wire::pkg:index:Thing::r','type':'pkg:index:Thing','custom':True,'id':'res-1','outputs':{}}
    d1['deployment']['resources']=[resource]
    d2['deployment']['resources']=[dict(resource,outputs={'value':None})]
    n1=c.call('POST',ds+'/import',d1)['updateId'];n2=c.call('POST',ds+'/import',d2)['updateId']
    delta=c.call('GET',ds+'/revision-diff?from='+n1+'&to='+n2)
    assert delta[0]['action']=='update' and '/outputs/value' in delta[0]['paths']
    # Operator script uses the same public API (no database credentials).
    cli_env=dict(env,PULUMI_BACKEND_URL=c.url,PULUMI_ACCESS_TOKEN=c.token)
    tree_cli=command(['python3','scripts/history.py','--stack',s.removeprefix('/api/stacks/'),'tree'],env=cli_env)
    assert json.loads(tree_cli.stdout)['head']==c.call('GET',s+'/revisions')['head']
    print('Revision tree, activation, history, CAS, stale writer, journal and permissions: PASS')


def migration(env, command, root, container, deployment):
    import subprocess
    command(['docker','exec',container,'createdb','-U','postgres','legacy'])
    uid,oid,sid,p0,p1,p2,u1,u2,tid=[str(uuid.uuid4()) for _ in range(9)]
    raw=json.dumps(deployment()).replace("'","''")
    sql=(root/'internal/store/schema.sql').read_text()+f'''
INSERT INTO principals(id,login,display_name) VALUES('{uid}','legacy','legacy');
INSERT INTO organizations(id,name) VALUES('{oid}','legacy');
INSERT INTO api_tokens(id,principal_id,digest,can_write,can_decrypt) VALUES('{tid}','{uid}',decode(repeat('ab',32),'hex'),true,true);
INSERT INTO stacks(id,org_id,last_version) VALUES('{sid}','{oid}',2);
INSERT INTO snapshots(id,stack_id,schema_version,raw_bytes,sha256,created_at) VALUES
 ('{p0}','{sid}',3,convert_to('{raw}','UTF8'),decode(repeat('ab',32),'hex'),'2026-01-01'),
 ('{p1}','{sid}',3,convert_to('{raw}','UTF8'),decode(repeat('ab',32),'hex'),'2026-01-02'),
 ('{p2}','{sid}',3,convert_to('{raw}','UTF8'),decode(repeat('ab',32),'hex'),'2026-01-03');
INSERT INTO stack_heads(stack_id,snapshot_id) VALUES('{sid}','{p2}');
INSERT INTO updates(id,stack_id,actor_token_id,kind,dry_run,status,mode,request,base_snapshot_id,final_snapshot_id,version) VALUES
 ('{u1}','{sid}','{tid}','update',false,'succeeded','full','{{}}','{p0}','{p1}',1),
 ('{u2}','{sid}','{tid}','update',false,'failed','full','{{}}','{p1}','{p2}',2);
UPDATE stacks SET active_update_id='{u2}' WHERE id='{sid}';
'''
    subprocess.run(['docker','exec','-i',container,'psql','-U','postgres','-d','legacy','-v','ON_ERROR_STOP=1'],input=sql,text=True,check=True,stdout=subprocess.DEVNULL)
    legacyenv=dict(env,BACKEND_DATABASE_URL=env['BACKEND_DATABASE_URL'].replace('/backend?','/legacy?'))
    assert command(['bin/backend','migrate'],env=legacyenv,check=False).returncode!=0
    command(['docker','exec',container,'psql','-U','postgres','-d','legacy','-c','UPDATE stacks SET active_update_id=NULL'])
    command(['bin/backend','migrate'],env=legacyenv);command(['bin/backend','migrate'],env=legacyenv)
    q=f"SELECT count(*)=3 AND bool_and((id='{u1}' AND parent_id='{sid}') OR (id='{u2}' AND parent_id='{u1}') OR (id='{sid}' AND parent_id IS NULL)) FROM state_revisions; SELECT current_revision='{u2}' AND last_version=2 FROM stacks;"
    r=command(['docker','exec',container,'psql','-U','postgres','-d','legacy','-Atc',q])
    assert r.stdout.decode().strip()=='t\nt',r.stdout
    print('Schema v1 history backfill, active update guard and migration idempotency: PASS')
