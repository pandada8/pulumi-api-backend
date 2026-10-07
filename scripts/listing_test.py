"""Resource counts must follow current state, including historical activation."""
import uuid

def run(c, c2, deployment):
    project='listing-'+uuid.uuid4().hex[:8]
    s=c.stack(project=project)
    def count(client):
        rows=client.call('GET','/api/user/stacks?project='+project)['stacks']
        assert len(rows)==1
        return rows[0]['resourceCount']
    assert count(c2)==0
    state=deployment()
    resource={'urn':'urn:pulumi:'+s.split('/')[-1]+'::'+project+'::pkg:index:Thing::r','type':'pkg:index:Thing','custom':True,'id':'r'}
    state['deployment']['resources']=[resource]
    first=c.call('POST',s+'/import',state)['updateId']
    assert count(c2)==1
    state['deployment']['resources'].append(dict(resource,urn=resource['urn']+'2',id='r2'))
    c.call('POST',s+'/import',state)
    assert count(c2)==2
    tree=c.call('GET',s+'/revisions')
    c.call('POST',s+'/activate',{'target':first,'expectedHead':tree['head'],'expectedEpoch':tree['activationEpoch'],'requestID':str(uuid.uuid4()),'reason':'Verify listing tracks activated state'})
    assert count(c2)==1
    c.call('DELETE',s+'?force=true',status=204)
    assert c2.call('GET','/api/user/stacks?project='+project)['stacks']==[]
    print('Listing counts: create, import, activation, deletion and cross-instance reads: PASS')
