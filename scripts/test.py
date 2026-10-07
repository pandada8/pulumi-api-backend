#!/usr/bin/env python3
"""Isolated PostgreSQL, two API instances, and unmodified pinned CLI; no user credentials."""
import signal, concurrent.futures
import base64, gzip, hashlib, http.cookiejar, json, os, pathlib, shutil, socket, subprocess, sys, tempfile, time, urllib.request, urllib.error, uuid
ROOT=pathlib.Path(__file__).resolve().parent.parent
os.chdir(ROOT)
RUN='pulumid-'+uuid.uuid4().hex[:12]
RESULT=ROOT/'test-results'/RUN
if __name__=='__main__':RESULT.mkdir(parents=True)
PROCS=[]
BACKEND=os.environ.get('PULUMID_TEST_BACKEND','bin/backend')
CONTAINER=RUN

def command(args, *, cwd=ROOT, env=None, timeout=120, check=True):
    p=subprocess.Popen(list(map(str,args)),cwd=cwd,env=env,start_new_session=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    try: stdout,stderr=p.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        os.killpg(p.pid,signal.SIGKILL);p.wait();raise
    r=subprocess.CompletedProcess(args,p.returncode,stdout,stderr)
    with (RESULT/'commands.log').open('ab') as f:
        f.write(('COMMAND '+str(args)+'\n').encode()+(b'[binary pg_dump omitted]\n' if 'pg_dump' in list(map(str,args)) else r.stdout)+r.stderr)
    if check and r.returncode:
        raise AssertionError(f'command failed ({r.returncode}): {args}\n{r.stderr.decode()[-4000:]}')
    return r

def port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]

def spawn(args,env,log):
    f=(RESULT/log).open('wb');p=subprocess.Popen(args,env=env,start_new_session=True,stdout=f,stderr=subprocess.STDOUT);f.close();PROCS.append(p);return p

def ready(url):
    until=time.monotonic()+60
    while time.monotonic()<until:
        try:urllib.request.urlopen(url,timeout=1);return
        except (urllib.error.URLError,TimeoutError):time.sleep(.1)
    raise AssertionError('readiness timeout: '+url)

class Client:
    def __init__(self,url,token):self.url,self.token=url,token
    def call(self,method,path,data=None,*,lease=None,status=200,compressed=False,raw=None):
        headers={'Authorization':('update-token '+lease) if lease else ('token '+self.token),'Accept':'application/vnd.pulumi+9','Content-Type':'application/json'}
        b=raw if raw is not None else None if data is None else json.dumps(data,separators=(',',':')).encode()
        if compressed:b=gzip.compress(b);headers['Content-Encoding']='gzip'
        req=urllib.request.Request(self.url+path,data=b,headers=headers,method=method)
        try:r=urllib.request.urlopen(req,timeout=120)
        except urllib.error.HTTPError as e:r=e
        code=r.code;b=r.read();r.close()
        assert code==status,(method,path,code,status,b[:1000])
        if b:
            value=json.loads(b)
            if status>=400:assert value['code']==status
            return value
    def stack(self,project='wire',name=None):
        name=name or ('s'+uuid.uuid4().hex[:10]);s='/api/stacks/demo/'+project+'/'+name
        self.call('POST','/api/stacks/demo/'+project,{'stackName':name,'tags':{'test':'run'}})
        return s
    def update(self,s,kind='update',dry=False,journal=False):
        project=s.split('/')[4]
        created=self.call('POST',s+'/'+kind,{'name':project,'runtime':'go','config':{},'options':{'dryRun':dry},'metadata':{'message':'test'}})
        u=s+'/update/'+created['updateID']
        start=self.call('POST',u,{'journalVersion':1 if journal else 0})
        return u,start['token'],start

def deployment(value='one'):
    return {'version':3,'deployment':{'manifest':{'time':'2026-01-01T00:00:00Z','version':'3.246.0','magic':hashlib.sha256(b'3.246.0').hexdigest()},'resources':[],'pending_operations':[],'unknownField':{'value':value,'integer':9007199254740993}}}

def full(c,s,u,t,value='one'):
    d=deployment(value)
    c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':d['deployment']},lease=t,status=204,compressed=True)
    return d

def contract(c,c2,env):
    c.call('GET','/api/user');Client(c.url,'bad').call('GET','/api/user',status=401)
    c.call('POST','/api/stacks/demo/wire',raw=b'{"stackName":"a","stackName":"b"}',status=400)
    c.call('POST','/api/stacks/demo/wire',raw=b'{}{}',status=400)
    # Two actual racing Start requests across API instances allocate exactly one writer/version.
    racing=c.stack();ids=[client.call('POST',racing+'/update',{'name':'wire','options':{}})['updateID'] for client in (c,c2)]
    def startRace(pair):
        client,id=pair
        req=urllib.request.Request(client.url+racing+'/update/'+id,data=b'{}',headers={'Authorization':'token '+client.token,'Content-Type':'application/json'},method='POST')
        try:r=urllib.request.urlopen(req,timeout=30)
        except urllib.error.HTTPError as e:r=e
        code=r.code;body=json.load(r);r.close();return code,id,body
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:starts=list(pool.map(startRace,zip((c,c2),ids)))
    assert sorted(x[0] for x in starts)==[200,409],starts
    winner=next(x for x in starts if x[0]==200);assert winner[2]['version']==1
    c.call('POST',racing+'/update/'+winner[1]+'/complete',{'status':'succeeded'},lease=winner[2]['token'],status=204)
    s=c.stack();assert c.call('GET',s)['version']==0
    c.call('HEAD','/api/stacks/demo/wire')
    c.call('POST','/api/stacks/demo/wire',{'stackName':s.split('/')[-1]},status=409)
    c.call('POST','/api/stacks/demo/wire',{'stackName':'teams','teams':['x']},status=422)
    u,t,start=c.update(s)
    assert c2.call('POST',u,{'journalVersion':0})==start
    c.call('POST',u,{'tags':{}},status=409)
    created=c2.call('POST',s+'/update',{'name':'wire','options':{}})['updateID']
    c2.call('POST',s+'/update/'+created,{},status=409)
    d=full(c,s,u,t)
    assert c2.call('GET',s+'/export')==d
    c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':d['deployment']},lease='wrong',status=403)
    c.call('PATCH',u+'/checkpoint',{'version':4,'deployment':{}},lease=t,status=422)
    c.call('PATCH',u+'/checkpoint',{'version':3,'isInvalid':False},lease=t,status=400)
    assert c.call('POST',u+'/renew_lease',{'duration':300},lease=t)['token']==t
    c.call('POST',u+'/renew_lease',{'duration':301},lease=t,status=400)
    other=c.stack();c.call('POST',other+'/encrypt',{'plaintext':base64.b64encode(b'canary').decode()},lease=t,status=403)
    c.call('DELETE',s,status=409)
    # Concurrent event retries, stable ingestion pagination, tamper detection.
    events=[{'sequence':i,'timestamp':int(time.time()),'stdoutEvent':{'message':f'event {i}','color':'raw'}} for i in range(205)]
    c.call('POST',u+'/events/batch',{'events':events[::-1]},lease=t,status=204,compressed=True)
    c2.call('POST',u+'/events/batch',{'events':events},lease=t,status=204)
    read=c.call('GET',u+'/events');assert len(read['events'])==100
    token=read['continuationToken'];c.call('GET',u+'/events?continuationToken='+token+'x',status=400)
    assert len(c2.call('GET',u+'/events?continuationToken='+token)['events'])==100
    c.call('POST',u+'/events/batch',{'events':[dict(events[0],timestamp=0)]},lease=t,status=409)
    c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
    c2.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
    c.call('POST',u+'/complete',{'status':'failed'},lease=t,status=409)
    c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':{}},lease=t,status=403)
    assert c.call('GET',s)['activeUpdate']==''
    assert c.call('GET',s+'/export/1')==d
    # Preview and dryRun paths never persist state or allocate formal version.
    for kind,dry in [('preview',False),('update',True)]:
        pu,pt,ps=c.update(s,kind,dry)
        assert ps['version']==1
        c.call('PATCH',pu+'/checkpoint',{'version':3,'deployment':d['deployment']},lease=pt,status=409)
        c.call('POST',pu+'/complete',{'status':'succeeded'},lease=pt,status=204)
        assert c.call('GET',s+'/export')==d
    iu=c.call('POST',s+'/import',deployment('two'),compressed=True)['updateId']
    assert c.call('GET',s+'/update/'+iu)['status']=='succeeded'
    assert c.call('GET',s+'/update/'+iu).get('continuationToken') is None
    assert c.call('GET',s+'/export/1')==d
    # Secrets are randomized, authenticated, and stack-bound.
    plain=base64.b64encode(b'canary-secret-test-only').decode()
    one=c.call('POST',s+'/encrypt',{'plaintext':plain})['ciphertext'];two=c.call('POST',s+'/encrypt',{'plaintext':plain})['ciphertext'];assert one!=two
    assert c.call('POST',s+'/decrypt',{'ciphertext':one})['plaintext']==plain
    c.call('POST',other+'/decrypt',{'ciphertext':one},status=400)
    raw=bytearray(base64.b64decode(one));raw[-1]^=1
    c.call('POST',s+'/decrypt',{'ciphertext':base64.b64encode(raw).decode()},status=400)
    assert c.call('POST',s+'/batch-decrypt',{'ciphertexts':[one,two]},compressed=True)['plaintexts']=={one:plain,two:plain}
    assert len(c.call('POST',s+'/batch-encrypt',{'plaintexts':[plain,plain]})['ciphertexts'])==2
    old=s;snew=s.rsplit('/',1)[0]+'/renamed'+uuid.uuid4().hex[:6]
    c.call('POST',s+'/rename',{'newName':snew.split('/')[-1]},status=204)
    c.call('GET',old,status=404)
    assert c.call('POST',old+'/decrypt',{'ciphertext':one})['plaintext']==plain
    assert c.call('POST',snew+'/decrypt',{'ciphertext':one})['plaintext']==plain
    c.call('POST','/api/stacks/demo/wire',{'stackName':old.split('/')[-1]},status=409)
    cu,ct,_=c.update(snew);full(c,snew,cu,ct,'partial')
    c2.call('POST',cu+'/cancel',status=204);c.call('POST',cu+'/cancel',status=204)
    c.call('PATCH',cu+'/checkpoint',{'version':3,'deployment':{}},lease=ct,status=403)
    assert c.call('GET',snew+'/export')['deployment']['unknownField']['value']=='partial'
    # Durable ACK across a process restart.
    assert c2.call('GET',snew+'/export')['deployment']['unknownField']['integer']==9007199254740993
    c.call('DELETE',snew,status=204);c.call('POST','/api/stacks/demo/wire',{'stackName':snew.split('/')[-1]})
    c.call('POST',snew+'/decrypt',{'ciphertext':one},status=400)
    # Reader cannot write or decrypt; token can_decrypt alone grants nothing.
    command(['bin/backendctl','member','set','--org','demo','--user','reader','--role','reader'],env=env)
    tokenPath=RESULT/'reader.token'
    command(['bin/backendctl','token','create','--user','reader','--decrypt','--out',tokenPath],env=env)
    reader=Client(c.url,tokenPath.read_text().strip());reader.call('GET',snew);reader.call('POST',snew+'/decrypt',{'ciphertext':one},status=403)
    reader.call('POST',snew+'/update',{'name':'wire','options':{}},status=403)
    command(['bin/backendctl','member','set','--org','other','--user','reader','--role','reader'],env=env,check=False) # unknown org does not create one
    # Revocation fences existing leases immediately.
    newpath=RESULT/'revoke.token';command(['bin/backendctl','token','create','--user','admin','--write','--out',newpath],env=env)
    rev=Client(c.url,newpath.read_text().strip());rs=rev.stack();ru,rt,_=rev.update(rs)
    tokenid=sql("SELECT id FROM api_tokens WHERE digest=decode('"+hashlib.sha256(rev.token.encode()).hexdigest()+"','hex')").strip()
    command(['bin/backendctl','token','revoke','--id',tokenid],env=env)
    rev.call('GET',rs,status=401);c.call('PATCH',ru+'/checkpoint',{'version':3,'deployment':d['deployment']},lease=rt,status=403)
    # Expiry and recovery generation reject stale writers, retain partial state.
    es=c.stack();eu,et,_=c.update(es);full(c,es,eu,et,'expiry')
    sql("UPDATE updates SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id='"+eu.split('/')[-1]+"'")
    c.call('PATCH',eu+'/checkpoint',{'version':3,'deployment':{}},lease=et,status=403)
    nu,nt,_=c2.update(es);c.call('POST',nu+'/complete',{'status':'failed'},lease=nt,status=204)
    assert c.call('GET',es+'/export')['deployment']['unknownField']['value']=='expiry'
    # Organization membership is required independently of token privilege.
    private=RESULT/'private.token';command(['bin/backendctl','bootstrap','--org','private','--user','bob','--token-file',private],env=env)
    bob=Client(c.url,private.read_text().strip());bob.call('POST','/api/stacks/private/wire',{'stackName':'hidden'})
    c.call('GET','/api/stacks/private/wire/hidden',status=404);bob.call('GET',snew,status=404)
    # isInvalid with missing deployment retains recovery data and blocks fresh writes until import repair.
    invalid=c.stack();iu,it,_=c.update(invalid);recover=full(c,invalid,iu,it,'recover')
    c.call('PATCH',iu+'/checkpoint',{'version':3,'isInvalid':True},lease=it,status=204)
    assert c.call('GET',invalid+'/export')==recover
    c.call('POST',iu+'/complete',{'status':'failed'},lease=it,status=204)
    bad=c.call('POST',invalid+'/update',{'name':'wire','options':{}})['updateID'];c.call('POST',invalid+'/update/'+bad,{},status=409)
    c.call('POST',invalid+'/import',recover);fixed,ft,_=c.update(invalid);c.call('POST',fixed+'/complete',{'status':'succeeded'},lease=ft,status=204)
    return {'stack':snew,'update':u,'oldStack':old,'secret':one}

def journal(c,c2):
    s=c.stack();u,t,start=c.update(s,journal=True);assert start['journalVersion']==1
    urn='urn:pulumi:'+s.split('/')[-1]+'::wire::backendtest:index:Item::r'
    r={'urn':urn,'custom':True,'type':'backendtest:index:Item','inputs':{'name':'r'}}
    begin={'version':1,'kind':0,'sequenceID':12,'operationID':1,'removeOld':None,'removeNew':None,'operation':{'type':'creating','resource':r}}
    c.call('PATCH',u+'/journalentries',{'entries':[begin]},lease=t,status=204,compressed=True)
    assert len(c2.call('GET',s+'/export')['deployment']['pending_operations'])==1
    success={'version':1,'kind':1,'sequenceID':11,'operationID':1,'removeOld':None,'removeNew':None,'state':dict(r,id='world-r',outputs={'value':'one'})}
    c.call('PATCH',u+'/journalentries',{'entries':[success]},lease=t,status=204)
    assert next(x for x in c2.call('GET','/api/user/stacks')['stacks'] if x['stackName']==s.split('/')[-1])['resourceCount']==1
    outputs={'version':1,'kind':4,'sequenceID':13,'operationID':2,'removeOld':None,'removeNew':1,'state':dict(r,id='world-r',outputs={'value':'two'})}
    c2.call('PATCH',u+'/journalentries',{'entries':[outputs,outputs]},lease=t,status=204)
    c.call('PATCH',u+'/journalentries',{'entries':[outputs]},lease=t,status=204)
    assert sql("SELECT journal_count FROM updates WHERE id='"+u.split('/')[-1]+"'").strip()=='3'
    state=c.call('GET',s+'/export')['deployment'];assert len(state['resources'])==1 and state['resources'][0]['outputs']['value']=='two'
    c.call('PATCH',u+'/journalentries',{'entries':[dict(outputs,removeNew=999,sequenceID=14)]},lease=t,status=400)
    c.call('PATCH',u+'/journalentries',{'entries':[dict(outputs,kind=99,sequenceID=14)]},lease=t,status=422)
    c.call('PATCH',u+'/journalentries',{'entries':[dict(outputs,kind=5,newSnapshot=None,sequenceID=14)]},lease=t,status=400)
    c.call('PATCH',u+'/journalentries',{'entries':[dict(outputs,operationID=3)]},lease=t,status=409)
    # Invalid states/operations must not change either journal count or readable head.
    for bad in [dict(success,sequenceID=21,state={}),dict(begin,sequenceID=22,operationID=8,operation={'type':'creating','resource':{}}),dict(begin,sequenceID=23,operationID=8,operation={'type':'invalid','resource':r}),dict(outputs,sequenceID=24,state=dict(r,provider='invalid-provider-reference'))]:
        c.call('PATCH',u+'/journalentries',{'entries':[bad]},lease=t,status=422)
        assert sql("SELECT journal_count FROM updates WHERE id='"+u.split('/')[-1]+"'").strip()=='3'
        assert c2.call('GET',s+'/export')['deployment']['resources']==state['resources']
    c.call('PATCH',u+'/journalentries',{'entries':[dict(begin,sequenceID=25,operationID=8),dict(outputs,sequenceID=26,state=dict(r,provider='invalid-provider-reference'))]},lease=t,status=422)
    assert sql("SELECT journal_count FROM updates WHERE id='"+u.split('/')[-1]+"'").strip()=='3'
    c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
    assert c.call('GET',s+'/export/1')['deployment']['resources']==state['resources']
    # No worker runs: terminal full updates must retain the journal baseline.
    for terminal in ['failed','cancelled','expired']:
        fu,ft,_=c.update(s)
        if terminal=='expired':
            sql("UPDATE updates SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id='"+fu.split('/')[-1]+"'")
            c.call('GET',s)
        elif terminal=='cancelled':c.call('POST',fu+'/cancel',status=204)
        else:c.call('POST',fu+'/complete',{'status':'failed'},lease=ft,status=204)
        version=sql("SELECT version FROM updates WHERE id='"+fu.split('/')[-1]+"'").strip()
        assert c.call('GET',s+'/export/'+version)['deployment']['resources']==state['resources']
        assert c.call('GET',s+'/export')['deployment']['resources']==state['resources']
    c.call('DELETE',s,status=400);c.call('DELETE',s+'?force=true',status=204)

def sql(query):
    return command(['docker','exec',CONTAINER,'psql','-U','postgres','-d','backend','-Atc',query]).stdout.decode()

def web(c,fixture):
    jar=http.cookiejar.CookieJar();opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    def req(method,path,data=None,origin=None,csrf=None,expected=200):
        headers={}
        if origin:headers['Origin']=origin
        if csrf:headers['X-CSRF-Token']=csrf
        if data is not None:headers['Content-Type']='application/json'
        request=urllib.request.Request(c.url+path,data=None if data is None else json.dumps(data).encode(),headers=headers,method=method)
        try:r=opener.open(request,timeout=60)
        except urllib.error.HTTPError as e:r=e
        b=r.read();assert r.code==expected,(r.code,b[:500]);return b,r
    req('POST','/console/api/session',{'token':c.token},expected=403)
    req('POST','/console/api/session',{'token':c.token},origin='https://evil.test',expected=403)
    b,r=req('POST','/console/api/session',{'token':c.token},origin=c.url);csrf=json.loads(b)['data']['csrfToken']
    cookie=r.headers['Set-Cookie'];assert 'HttpOnly' in cookie and 'SameSite=Lax' in cookie
    req('GET','/stacks');s=fixture['stack'];req('GET',s.removeprefix('/api/stacks'))
    req('GET',s.removeprefix('/api/stacks')+'/resources')
    sid=c.call('GET',s)['id'];b,_=req('GET','/console/api/stacks/'+sid);assert b'canary-secret' not in b
    req('DELETE','/console/api/session',origin=c.url,expected=401)
    req('DELETE','/console/api/session',origin=c.url,csrf=csrf)
    req('GET','/console/api/stacks',expected=401)

def e2e(c,env,mode):
    home=RESULT/('home-'+mode+'-'+uuid.uuid4().hex[:6]);home.mkdir()
    plugin=home/'plugins/resource-backendtest-v0.0.1';plugin.mkdir(parents=True)
    shutil.copy(ROOT/'bin/pulumi-resource-backendtest',plugin/'pulumi-resource-backendtest')
    project=RESULT/('project-'+mode+'-'+uuid.uuid4().hex[:6]);project.mkdir()
    for f in ['main.go','go.mod','go.sum','Pulumi.yaml']:shutil.copy(ROOT/'tests/projects/basic'/f,project/f)
    text=(project/'go.mod').read_text().replace('../../../third_party/pulumi/sdk',str(ROOT/'third_party/pulumi/sdk'))
    (project/'go.mod').write_text(text)
    runenv=env.copy();runenv.update(PULUMI_HOME=str(home),PULUMI_ACCESS_TOKEN=c.token,PULUMI_SKIP_UPDATE_CHECK='true',PULUMI_CONSOLE_DOMAIN=c.url.removeprefix('http://'),PULUMI_SKIP_CHECKPOINTS='false')
    cli=ROOT/'bin/pulumi'
    def run(*args):return command([cli,*args],cwd=project,env=runenv,timeout=300).stdout.decode()
    name='cli'+uuid.uuid4().hex[:8];s='/api/stacks/demo/basic/'+name
    run('login',env['BACKEND_PUBLIC_URL']);run('login',c.url);run('whoami');run('stack','init','demo/basic/'+name);run('stack','select','demo/basic/'+name)
    run('config','set','worldURL',env['WORLD_URL']);run('config','set','message','hello');run('config','set','password','e2e-canary-secret','--secret')
    if mode=='fault':
        req=urllib.request.Request(env['WORLD_URL']+'/control',data=json.dumps({'delayMs':60000}).encode(),headers={'X-Test-Control':env['WORLD_CONTROL_TOKEN'],'Content-Type':'application/json'},method='POST')
        urllib.request.urlopen(req).close()
        before=json.load(urllib.request.urlopen(env['WORLD_URL']+'/resources'))
        f=(RESULT/('cli-killed-'+name+'.log')).open('wb')
        process=subprocess.Popen([str(cli),'up','--yes','--non-interactive','--skip-preview'],cwd=project,env=runenv,start_new_session=True,stdout=f,stderr=subprocess.STDOUT);f.close()
        try:
            until=time.monotonic()+120
            while time.monotonic()<until:
                world=json.load(urllib.request.urlopen(env['WORLD_URL']+'/resources'))
                if len(world)>len(before):break
                if process.poll() is not None:raise AssertionError('CLI exited before crash fault')
                time.sleep(.1)
            else:raise AssertionError('world resource never created')
            os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=10)
            state=c.call('GET',s+'/export');pending=state['deployment'].get('pending_operations',[])
            assert any(x['type']=='creating' and x['resource']['type']=='backendtest:index:Item' for x in pending),state
            active=c.call('GET',s)['activeUpdate'];c.call('POST',s+'/update/'+active+'/cancel',status=204)
            assert c.call('GET',s+'/export')['deployment']['pending_operations']==pending
            assert len(json.load(urllib.request.urlopen(env['WORLD_URL']+'/resources')))>len(before)
        finally:
            if process.poll() is None:os.killpg(process.pid,signal.SIGKILL);process.wait()
            req=urllib.request.Request(env['WORLD_URL']+'/control',data=b'{"delayMs":0}',headers={'X-Test-Control':env['WORLD_CONTROL_TOKEN'],'Content-Type':'application/json'},method='POST');urllib.request.urlopen(req).close()
        return
    run('preview','--non-interactive');run('up','--yes','--non-interactive','--skip-preview')
    state=c.call('GET',s+'/export');assert len(state['deployment']['resources'])==3
    assert 'e2e-canary-secret' not in json.dumps(state)
    out=run('stack','output','value');assert out.strip()=='hello'
    run('config','set','message','updated');run('up','--yes','--non-interactive','--skip-preview')
    run('config','set','replaceKey','replacement');run('up','--yes','--non-interactive','--skip-preview')
    run('up','--refresh','--yes','--non-interactive','--skip-preview')
    run('refresh','--yes','--non-interactive','--skip-preview')
    # Import an independently created world resource into the same deployment.
    data={'inputs':{'name':'imported','value':'outside','replaceKey':'import'}}
    req=urllib.request.Request(env['WORLD_URL']+'/resources',data=json.dumps(data).encode(),headers={'Content-Type':'application/json'},method='POST')
    with urllib.request.urlopen(req,timeout=10) as response: resource=json.load(response)
    provider=next(r['urn'] for r in c.call('GET',s+'/export')['deployment']['resources'] if r['type']=='pulumi:providers:backendtest')
    run('import','backendtest:index:Item','imported',resource['id'],'--provider','test='+provider,'--protect=false','--yes','--non-interactive','--generate-code=false')
    run('stack','history','--json');run('stack','export','--file',project/'export.json')
    run('stack','import','--file',project/'export.json')
    # Rename keeps provider identity, application inputs, and old service-secret paths.
    renamed=name+'new';run('stack','rename',renamed);s='/api/stacks/demo/basic/'+renamed
    run('up','--yes','--non-interactive','--skip-preview')
    # Provider delete failure keeps its resource in durable state; a subsequent successful destroy removes it.
    req=urllib.request.Request(env['WORLD_URL']+'/control',data=b'{"failDelete":true}',headers={'X-Test-Control':env['WORLD_CONTROL_TOKEN'],'Content-Type':'application/json'},method='POST');urllib.request.urlopen(req).close()
    failed=command([cli,'destroy','--yes','--non-interactive','--skip-preview'],cwd=project,env=runenv,timeout=300,check=False)
    assert failed.returncode!=0
    assert any(r['type']=='backendtest:index:Item' for r in c.call('GET',s+'/export')['deployment']['resources'])
    req=urllib.request.Request(env['WORLD_URL']+'/control',data=b'{"failDelete":false}',headers={'X-Test-Control':env['WORLD_CONTROL_TOKEN'],'Content-Type':'application/json'},method='POST');urllib.request.urlopen(req).close()
    run('destroy','--yes','--non-interactive','--skip-preview')
    run('stack','rm','--yes');c.call('GET',s,status=404)
    assert mode=='full' or int(sql('SELECT count(*) FROM journal_entries').strip())>0

def fault(c,c2,env):
    def ctrl(name,mode=None,method='POST'):
        data=None if mode is None else json.dumps({'mode':mode}).encode()
        req=urllib.request.Request(c.url+'/_test/faults/'+name,data=data,headers={'X-Test-Control':env['BACKEND_TEST_CONTROL_TOKEN'],'Content-Type':'application/json'},method=method)
        with urllib.request.urlopen(req,timeout=10) as response:return json.load(response)
    s=c.stack();u,t,_=c.update(s);before=full(c,s,u,t,'baseline')
    # Disconnect after committed ACK: clients retry safely, no rewind or double journal application.
    ctrl('after_db_commit_before_response','drop')
    try:full(c,s,u,t,'committed')
    except (urllib.error.URLError,ConnectionError,__import__('http.client').client.RemoteDisconnected):pass
    else:raise AssertionError('drop hook did not disconnect')
    assert c2.call('GET',s+'/export')['deployment']['unknownField']['value']=='committed'
    full(c,s,u,t,'committed')
    ctrl('before_db_commit','fail')
    c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':deployment('uncommitted')['deployment']},lease=t,status=500)
    ctrl('before_db_commit',method='DELETE')
    assert c2.call('GET',s+'/export')['deployment']['unknownField']['value']=='committed'
    # A request paused before taking a write lock must not survive a concurrent cancellation.
    ctrl('after_candidate_before_lock','block')
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        pending=pool.submit(c.call,'PATCH',u+'/checkpoint',{'version':3,'deployment':deployment('stale')['deployment']},lease=t,status=403)
        until=time.monotonic()+10
        while time.monotonic()<until:
            if ctrl('after_candidate_before_lock',method='GET')['hits']:break
            time.sleep(.05)
        else:raise AssertionError('hook not reached')
        c2.call('POST',u+'/cancel',status=204)
        ctrl('after_candidate_before_lock',method='DELETE');pending.result(timeout=30)
    assert c.call('GET',s+'/export')['deployment']['unknownField']['value']=='committed'
    # Real database restart retains successful checkpoint ACKs.
    command(['docker','restart',CONTAINER],timeout=60)
    until=time.monotonic()+60
    while time.monotonic()<until:
        if command(['docker','exec',CONTAINER,'pg_isready','-h','127.0.0.1','-U','postgres'],check=False).returncode==0:break
        time.sleep(.1)
    ready(c.url+'/readyz');assert c.call('GET',s+'/export')['deployment']['unknownField']['value']=='committed'
    # Corrupt acknowledged state must fail loudly, never silently return an older snapshot.
    corrupt=c.stack();cu,ct,_=c.update(corrupt);full(c,corrupt,cu,ct)
    cid=c.call('GET',corrupt)['id'];sql("UPDATE snapshots SET raw_bytes=convert_to('{}','UTF8') WHERE id=(SELECT snapshot_id FROM stack_heads WHERE stack_id='"+cid+"')")
    c.call('GET',corrupt+'/export',status=500)
    sql("UPDATE snapshots SET raw_bytes=convert_to('"+json.dumps(deployment(),separators=(',',':'))+"','UTF8'),sha256=decode('"+hashlib.sha256(json.dumps(deployment(),separators=(',',':')).encode()).hexdigest()+"','hex') WHERE id=(SELECT snapshot_id FROM stack_heads WHERE stack_id='"+cid+"')")
    c.call('POST',cu+'/cancel',status=204)
    # Failed journal commits cannot publish speculative reference caches.
    js=c.stack();ju,jt,_=c.update(js,journal=True)
    entry={'version':1,'kind':0,'sequenceID':1,'operationID':1}
    ctrl('before_db_commit','fail');c.call('PATCH',ju+'/journalentries',{'entries':[entry]},lease=jt,status=500)
    ctrl('before_db_commit',method='DELETE');c.call('PATCH',ju+'/journalentries',{'entries':[entry]},lease=jt,status=204)
    assert sql("SELECT journal_count FROM updates WHERE id='"+ju.split('/')[-1]+"'").strip()=='1'
    c.call('POST',ju+'/cancel',status=204)
    # Materialization CAS must not overwrite a later full checkpoint.
    s=c.stack();ju,jt,_=c.update(s,journal=True)
    urn='urn:pulumi:'+s.split('/')[-1]+'::wire::backendtest:index:Item::r'
    r={'urn':urn,'type':'backendtest:index:Item','custom':True,'id':'world-r'}
    entries=[{'version':1,'kind':0,'sequenceID':1,'operationID':1,'operation':{'type':'creating','resource':r}},{'version':1,'kind':1,'sequenceID':2,'operationID':1,'state':r}]
    c.call('PATCH',ju+'/journalentries',{'entries':entries},lease=jt,status=204)
    c.call('POST',ju+'/complete',{'status':'succeeded'},lease=jt,status=204)
    fullu,ft,_=c.update(s);current=full(c,s,fullu,ft,'newer');c.call('POST',fullu+'/complete',{'status':'succeeded'},lease=ft,status=204)
    for _ in range(int(sql("SELECT count(*) FROM jobs WHERE status='pending'").strip())):command([BACKEND,'worker','--once'],env=env)
    assert c.call('GET',s+'/export')==current
    assert c.call('GET',s+'/export/1')['deployment']['resources'][0]['id']=='world-r'

def restore(c,env):
    s=c.stack();u,t,_=c.update(s);before=full(c,s,u,t,'backup')
    plain=base64.b64encode(b'restore-secret-canary').decode();cipher=c.call('POST',s+'/encrypt',{'plaintext':plain})['ciphertext']
    dump=command(['docker','exec',CONTAINER,'pg_dump','-U','postgres','-d','backend','-Fc']).stdout
    (RESULT/'database.dump').write_bytes(dump)
    command(['docker','exec',CONTAINER,'createdb','-U','postgres','restoredb'])
    p=subprocess.run(['docker','exec','-i',CONTAINER,'pg_restore','-U','postgres','-d','restoredb','--exit-on-error'],input=dump,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=60)
    assert p.returncode==0,p.stderr.decode()
    restoreenv=dict(env,BACKEND_DATABASE_URL=env['BACKEND_DATABASE_URL'].replace('/backend?','/restoredb?'))
    command(['bin/backendctl','restore-generation'],env=restoreenv)
    rp=port();restoreenv['BACKEND_LISTEN']=f'127.0.0.1:{rp}'
    spawn([BACKEND],restoreenv,'restore.log');rc=Client(f'http://127.0.0.1:{rp}',c.token);ready(rc.url+'/readyz')
    assert rc.call('GET',s+'/export')==before
    assert rc.call('POST',s+'/decrypt',{'ciphertext':cipher})['plaintext']==plain
    rc.call('PATCH',u+'/checkpoint',{'version':3,'deployment':deployment('stale')['deployment']},lease=t,status=403)
    assert rc.call('GET',s)['activeUpdate']==''
    ru,rt,_=rc.update(s);rc.call('POST',ru+'/complete',{'status':'succeeded'},lease=rt,status=204)
    assert c.call('GET',s)['activeUpdate']==u.split('/')[-1] # source DB was never altered

def main():
    suite=sys.argv[1] if len(sys.argv)>1 else 'all';dbport,apiport,api2port,worldport=port(),port(),port(),port()
    master=RESULT/'master.key';master.write_bytes(os.urandom(32));master.chmod(0o600)
    env=os.environ.copy();env['PATH']=str(ROOT/'bin')+os.pathsep+env['PATH'];env.update(BACKEND_DATABASE_URL=f'postgres://postgres:test-only@127.0.0.1:{dbport}/backend?sslmode=disable',BACKEND_MASTER_KEY_FILE=str(master),BACKEND_PUBLIC_URL=f'http://127.0.0.1:{apiport}',BACKEND_CONSOLE_URL=f'http://127.0.0.1:{apiport}',BACKEND_LISTEN=f'127.0.0.1:{apiport}',BACKEND_DEV_HTTP='true',BACKEND_ENABLE_JOURNAL='true',WORLD_URL=f'http://127.0.0.1:{worldport}',WORLD_CONTROL_TOKEN=uuid.uuid4().hex,BACKEND_TEST_FAULTS='true',BACKEND_TEST_CONTROL_TOKEN=uuid.uuid4().hex)
    try:
        command(['docker','run','-d','--name',CONTAINER,'-e','POSTGRES_PASSWORD=test-only','-e','POSTGRES_DB=backend','-p',f'127.0.0.1:{dbport}:5432','postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73'])
        until=time.monotonic()+60
        while time.monotonic()<until:
            if command(['docker','exec',CONTAINER,'pg_isready','-h','127.0.0.1','-U','postgres'],check=False).returncode==0:break
            time.sleep(.2)
        else:raise AssertionError('postgres timeout')
        command([BACKEND,'migrate'],env=env);command([BACKEND,'migrate'],env=env)
        command(['bin/backendctl','bootstrap','--token-file',RESULT/'admin.token'],env=env)
        command(['bin/backendctl','token','create','--write','--decrypt','--out',RESULT/'cli.token'],env=env)
        token=(RESULT/'cli.token').read_text().strip()
        p1=spawn([BACKEND],env,'api1.log');env2=dict(env,BACKEND_LISTEN=f'127.0.0.1:{api2port}');p2=spawn([BACKEND],env2,'api2.log')
        ready(env['BACKEND_PUBLIC_URL']+'/readyz');url2=f'http://127.0.0.1:{api2port}';ready(url2+'/readyz')
        c,c2=Client(env['BACKEND_PUBLIC_URL'],token),Client(url2,token)
        contractenv=dict(env,TEST_API_URL=c.url,TEST_TOKEN_FILE=str(RESULT/'cli.token'))
        command(['go','test','-race','./tests/contract'],env=contractenv,timeout=300)
        rename_stack=c.stack()
        rename_env=dict(env,TEST_RENAME_STACK_ID=c.call('GET',rename_stack)['id'])
        command(['go','test','-race','./internal/store','-run','TestRenameWaitingLookup|TestListingDoesNotReadSnapshots','-count=1'],env=rename_env,timeout=60)
        fixture=contract(c,c2,env);journal(c,c2);web(c,fixture)
        import listing_test
        listing_test.run(c,c2,deployment)
        import revisions_test
        revisions_test.run(c,c2,env,command,ROOT,CONTAINER,deployment)
        revisions_test.migration(env,command,ROOT,CONTAINER,deployment)
        if suite in ('all','web'):
            command(['.dev/web-venv/bin/python','scripts/browser.py',c.url,RESULT/'cli.token',RESULT],env=env,timeout=180)
        # An API process restart cannot lose an ACK. Second instance stays available.
        before=c.call('GET',fixture['stack']+'/export');p1.terminate();p1.wait(timeout=10);assert c2.call('GET',fixture['stack']+'/export')==before
        p1=spawn([BACKEND],env,'api1-restart.log');ready(c.url+'/readyz');assert c.call('GET',fixture['stack']+'/export')==before
        if suite in ('all','e2e','fault'):
            spawn(['bin/world','--listen',f'127.0.0.1:{worldport}','--dir',str(RESULT/'world')],env,'world.log');ready(env['WORLD_URL']+'/resources')
            e2e(c,env,'journal')
            # Different instance configuration negotiates full checkpoints.
            p2.terminate();p2.wait(timeout=10);env2['BACKEND_ENABLE_JOURNAL']='false';spawn([BACKEND],env2,'api2-full.log');ready(url2+'/readyz');e2e(c2,env,'full')
            if suite in ('all','fault'):
                e2e(c,env,'fault');e2e(c2,env,'fault')
        if suite in ('all','fault'):fault(c,c2,env)
        if suite in ('all','restore'):restore(c,env)
        if suite=='bench':
            import bench
            bench.run(c,env)
        data={'result':'PASS','codeSHA':command(['git','rev-parse','HEAD']).stdout.decode().strip(),'suite':suite,'runID':RUN,'pulumiSHA':command(['git','-C','third_party/pulumi','rev-parse','HEAD']).stdout.decode().strip(),'go':command(['go','version']).stdout.decode().strip()}
        (RESULT/'result.json').write_text(json.dumps(data,indent=2));print(json.dumps(data));print('Evidence: test-results/'+RUN)
    finally:
        for p in reversed(PROCS):
            if p.poll() is None:os.killpg(p.pid,signal.SIGTERM)
        for p in PROCS:
            try:p.wait(timeout=10)
            except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
        command(['docker','rm','-f',CONTAINER],check=False)
if __name__=='__main__':main()
