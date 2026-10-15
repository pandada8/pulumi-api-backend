#!/usr/bin/env python3
import concurrent.futures,json,os,pathlib,sys,uuid
from playwright.sync_api import sync_playwright, expect
url,tokenfile,out=sys.argv[1:];out=pathlib.Path(out);out.mkdir(exist_ok=True)
from test import Client
c=Client(url,pathlib.Path(tokenfile).read_text().strip());s=c.stack('browser');u,t,start=c.update(s)
urn='urn:pulumi:'+s.split('/')[-1]+'::browser::pulumi:pulumi:Stack::browser-'+s.split('/')[-1]
c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':{'manifest':{'time':'2026-01-01T00:00:00Z','version':'3.246.0','magic':'x'},'resources':[{'urn':urn,'type':'pulumi:pulumi:Stack','outputs':{'safe':'hello','password':{'4dabf18193072939515e22adb298388d':'1b47061264138c4ac30d75fd1eb44270','ciphertext':'browser-secret-canary'},'xss':'<script>window.__injected=1</script>'}}]}},lease=t,status=204)
c.call('POST',u+'/events/batch',{'events':[{'sequence':0,'timestamp':1,'stdoutEvent':{'message':'<img src=x onerror="window.__injected=1">','color':'raw'}}]},lease=t,status=204)
c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
preview,pt,_=c.update(s,'preview');c.call('POST',preview+'/complete',{'status':'succeeded'},lease=pt,status=204)
base=s.removeprefix('/api/stacks')
tree_project='tree-'+uuid.uuid4().hex[:8]
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
    tree_paths=list(pool.map(lambda _:c.stack(tree_project),range(105)))
with sync_playwright() as pw:
    browser=pw.chromium.launch(headless=True,args=['--no-sandbox'])
    page=browser.new_page(viewport={'width':1280,'height':900})
    page.add_init_script("window.__cspViolations=[];addEventListener('securitypolicyviolation',e=>window.__cspViolations.push(e.violatedDirective))")
    try:
        page.goto(url+'/stacks');assert page.url.endswith('/login')
        page.get_by_label('API token').fill(c.token);page.get_by_role('button',name='Sign in').click();page.wait_for_url('**/stacks')
        page.get_by_role('heading',name='Stacks.',exact=True).wait_for()
        expect(page.locator(".tree-footer")).not_to_contain_text("Loading more stacks")
        first=page.request.get(url+'/console/api/stacks').json()
        assert first.get('nextCursor'), 'pagination fixture must span multiple pages'
        first_names={x['stackName'] for x in first['data']}
        beyond_first=next(p for p in tree_paths if p.split('/')[-1] not in first_names)
        target=beyond_first.split('/')[-1]
        page.get_by_label('Search stacks',exact=True).fill(target)
        page.locator('.stack-row').filter(has_text=target).wait_for()
        assert page.locator('.stack-row:visible').count()==1
        page.get_by_role('button',name='Collapse all',exact=True).click()
        assert page.locator('.stack-row:visible').count()==0
        page.get_by_role('button',name='Expand all',exact=True).click()
        assert page.locator('.stack-row:visible').count()==1
        page.get_by_role('button',name='Clear search',exact=True).click()
        page.locator('button.org-filter').click()
        expect(page.get_by_role('menu')).to_be_visible()
        assert page.evaluate('window.__cspViolations')==[], 'console must work under CSP'
        page.keyboard.press('Escape')
        page.screenshot(path=str(out/'console-tree.png'))
        for path in [base,base+'/resources',base+'/history',base+'/updates/1',base+'/previews/'+preview.split('/')[-1]]:
            res=page.goto(url+path);assert res.status==200,(path,res.status)
            page.locator('h1').wait_for(state='attached');assert page.locator('h1').count()==1
            assert 'browser-secret-canary' not in page.locator('body').inner_text()
            assert page.evaluate('window.__injected') is None
        page.goto(url+base+'/updates/1');expect(page.locator("#events")).to_contain_text("onerror")
        assert page.evaluate('window.__injected') is None
        assert page.evaluate('localStorage.length')==0
        cookies=page.context.cookies();assert any(x['httpOnly'] and x['sameSite']=='Lax' for x in cookies)
        page.screenshot(path=str(out/'console.png'),full_page=True)
        page.set_viewport_size({'width':390,'height':844});page.goto(url+base+'/resources');page.locator('.resource-item').first.wait_for()
        page.screenshot(path=str(out/'console-mobile.png'),full_page=True)
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'mobile layout overflows'
        page.get_by_role('button',name='Open navigation',exact=True).click()
        page.locator('.sidebar.mobile-open').wait_for()
        page.keyboard.press('Escape')
        page.reload();page.locator('h1').wait_for(state='attached')
        page.get_by_role('button',name='Open navigation',exact=True).click()
        page.get_by_role('button',name='Sign out',exact=True).click()
        page.wait_for_url('**/login')
        assert page.request.get(url+'/console/api/session').status==401
        (out/'browser.json').write_text(json.dumps({'result':'PASS','pages':5,'checks':['login redirect','DOM','CLI permalinks','secret redaction','XSS','event polling','cookie','no localStorage','tree pagination beyond 100','search','collapse/expand','mobile layout/navigation','logout after reload'],'browser':browser.version},indent=2))
    except Exception:
        page.screenshot(path=str(out/'failure.png'),full_page=True);raise
    finally:browser.close()
print('PASS: Chromium console, permalinks, XSS, redaction, events')
