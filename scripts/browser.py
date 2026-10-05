#!/usr/bin/env python3
import json,os,pathlib,sys
from playwright.sync_api import sync_playwright
url,tokenfile,out=sys.argv[1:];out=pathlib.Path(out);out.mkdir(exist_ok=True)
from test import Client
c=Client(url,pathlib.Path(tokenfile).read_text().strip());s=c.stack('browser');u,t,start=c.update(s)
urn='urn:pulumi:'+s.split('/')[-1]+'::browser::pulumi:pulumi:Stack::browser-'+s.split('/')[-1]
c.call('PATCH',u+'/checkpoint',{'version':3,'deployment':{'manifest':{'time':'2026-01-01T00:00:00Z','version':'3.246.0','magic':'x'},'resources':[{'urn':urn,'type':'pulumi:pulumi:Stack','outputs':{'safe':'hello','password':{'4dabf18193072939515e22adb298388d':'1b47061264138c4ac30d75fd1eb44270','ciphertext':'browser-secret-canary'},'xss':'<script>window.__injected=1</script>'}}]}},lease=t,status=204)
c.call('POST',u+'/events/batch',{'events':[{'sequence':0,'timestamp':1,'stdoutEvent':{'message':'<img src=x onerror="window.__injected=1">','color':'raw'}}]},lease=t,status=204)
c.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
preview,pt,_=c.update(s,'preview');c.call('POST',preview+'/complete',{'status':'succeeded'},lease=pt,status=204)
base=s.removeprefix('/api/stacks')
with sync_playwright() as pw:
    browser=pw.chromium.launch(headless=True,args=['--no-sandbox'])
    page=browser.new_page(viewport={'width':1280,'height':900})
    try:
        page.goto(url+'/stacks');assert page.url.endswith('/login')
        page.get_by_label('API token').fill(c.token);page.get_by_role('button',name='Sign in').click();page.wait_for_url('**/stacks')
        for path in [base,base+'/resources',base+'/history',base+'/updates/1',base+'/previews/'+preview.split('/')[-1]]:
            res=page.goto(url+path);assert res.status==200,(path,res.status)
            assert page.locator('h1').count()==1
            assert 'browser-secret-canary' not in page.locator('body').inner_text()
            assert page.evaluate('window.__injected') is None
        page.goto(url+base+'/updates/1');page.wait_for_function("document.querySelector('#events').textContent.includes('onerror')")
        assert page.evaluate('window.__injected') is None
        assert page.evaluate('localStorage.length')==0
        cookies=page.context.cookies();assert any(x['httpOnly'] and x['sameSite']=='Lax' for x in cookies)
        page.screenshot(path=str(out/'console.png'),full_page=True)
        page.set_viewport_size({'width':390,'height':844});page.goto(url+base+'/resources');page.screenshot(path=str(out/'console-mobile.png'),full_page=True)
        (out/'browser.json').write_text(json.dumps({'result':'PASS','pages':5,'checks':['login redirect','DOM','CLI permalinks','secret redaction','XSS','event polling','cookie','no localStorage'],'browser':browser.version},indent=2))
    except Exception:
        page.screenshot(path=str(out/'failure.png'),full_page=True);raise
    finally:browser.close()
print('PASS: Chromium console, permalinks, XSS, redaction, events')
