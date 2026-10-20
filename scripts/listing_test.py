"""Stack counts and lastUpdate follow persisted state and update history."""
import time, uuid

def run(c, c2, deployment):
    project='listing-'+uuid.uuid4().hex[:8]
    s=c.stack(project=project)
    def summary(client):
        rows=client.call('GET','/api/user/stacks?project='+project)['stacks']
        assert len(rows)==1
        return rows[0]
    def count(client):
        return summary(client)['resourceCount']
    assert count(c2)==0
    assert 'lastUpdate' not in summary(c2)
    # Created-but-not-started requests and previews do not count as updates.
    c.call('POST',s+'/update',{'name':project,'options':{}})
    for kind,dry in [('preview',False),('update',True)]:
        u,t,_=c.update(s,kind,dry)
        assert 'lastUpdate' not in summary(c2)
        c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
    assert 'lastUpdate' not in summary(c2)
    state=deployment()
    resource={'urn':'urn:pulumi:'+s.split('/')[-1]+'::'+project+'::pkg:index:Thing::r','type':'pkg:index:Thing','custom':True,'id':'r'}
    state['deployment']['resources']=[resource]
    first=c.call('POST',s+'/import',state)['updateId']
    assert count(c2)==1
    def latest_start():
        return c.call('GET',s+'/updates?page=1&pageSize=1')['updates'][0]['startTime']
    imported_start=latest_start()
    assert type(summary(c2)['lastUpdate']) is int
    assert summary(c2)['lastUpdate']==imported_start
    # Separate timestamps so stale/preview timestamps cannot pass by coincidence.
    time.sleep(1.1)
    for kind,dry in [('preview',False),('update',True)]:
        u,t,_=c.update(s,kind,dry)
        assert summary(c2)['lastUpdate']==imported_start
        c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
    assert summary(c2)['lastUpdate']==imported_start
    u,t,_=c.update(s)
    running_start=latest_start()
    assert running_start>imported_start
    assert summary(c2)['lastUpdate']==running_start
    c.call('POST',u+'/complete',{'status':'failed'},lease=t,status=204)
    assert summary(c2)['lastUpdate']==running_start
    state['deployment']['resources'].append(dict(resource,urn=resource['urn']+'2',id='r2'))
    c.call('POST',s+'/import',state)
    assert count(c2)==2
    assert summary(c2)['lastUpdate']==latest_start()
    tree=c.call('GET',s+'/revisions')
    c.call('POST',s+'/activate',{'target':first,'expectedHead':tree['head'],'expectedEpoch':tree['activationEpoch'],'requestID':str(uuid.uuid4()),'reason':'Verify listing tracks activated state'})
    assert count(c2)==1
    assert summary(c2)['lastUpdate']==latest_start()
    c.call('DELETE',s+'?force=true',status=204)
    assert c2.call('GET','/api/user/stacks?project='+project)['stacks']==[]
    print('Listing counts and lastUpdate: empty, unstarted, preview, dry-run, import, running, failed, activation and cross-instance reads: PASS')
