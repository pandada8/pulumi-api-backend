"""API storage microbenchmark, separate from the CLI/DIY end-to-end performance acceptance matrix."""
import concurrent.futures,csv,gzip,http.server,json,statistics,threading,time,urllib.request
from test import Client,deployment
from __main__ import RESULT

def run(c,env):
    class DelayProxy(http.server.BaseHTTPRequestHandler):
        protocol_version='HTTP/1.1'
        def log_message(self,*args):pass
        def forward(self):
            b=self.rfile.read(int(self.headers.get('Content-Length','0')))
            headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','connection')}
            time.sleep(.01)
            req=urllib.request.Request(c.url+self.path,data=b if b else None,headers=headers,method=self.command)
            try:r=urllib.request.urlopen(req,timeout=120)
            except urllib.error.HTTPError as e:r=e
            body=r.read();status=r.code;content=r.headers.get('Content-Type','application/json');r.close();time.sleep(.01)
            self.send_response(status);self.send_header('Content-Type',content);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
        do_GET=do_POST=do_PATCH=do_DELETE=forward
    proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),DelayProxy)
    thread=threading.Thread(target=proxy.serve_forever);thread.start()
    client=Client('http://127.0.0.1:'+str(proxy.server_port),c.token)
    rows=[]
    try:
        probe=[]
        for _ in range(5):start=time.perf_counter();client.call('GET','/api/user');probe.append((time.perf_counter()-start)*1000)
        for n,concurrency in [(1000,1),(10000,1),(1000,10),(10000,10)]:
            for mode in ['full-api','journal-api']:
                print(f'BENCH N={n} concurrency={concurrency} mode={mode}',flush=True)
                stacks=[]
                for _ in range(concurrency):
                    s=c.stack('bench');name=s.split('/')[-1]
                    d=deployment();d['deployment'].pop('unknownField')
                    d['deployment']['resources']=[{'urn':f'urn:pulumi:{name}::bench::backendtest:index:Item::r{i}','type':'backendtest:index:Item','custom':True,'id':str(i),'inputs':{'name':str(i)},'outputs':{'value':'x'*1024}} for i in range(n)]
                    c.call('POST',s+'/import',d,compressed=True);stacks.append((s,d))
                def iteration(pair,iteration):
                    s,d=pair;start=time.perf_counter();u,t,_=client.update(s,journal=mode=='journal-api')
                    changes=max(1,n//100)
                    for i in range(changes):d['deployment']['resources'][i]['outputs']['value']=str(iteration)+'x'*1020
                    payload={'version':3,'deployment':d['deployment']}
                    if mode=='journal-api':
                        entries=[]
                        for i in range(changes):
                            r=d['deployment']['resources'][i];entries.extend([{'version':1,'kind':0,'sequenceID':2*i+1,'operationID':i+1}, {'version':1,'kind':1,'sequenceID':2*i+2,'operationID':i+1,'removeOld':i,'state':r}])
                        payload={'entries':entries}
                    encoded=json.dumps(payload,separators=(',',':')).encode();uploaded=len(gzip.compress(encoded));ack=time.perf_counter()
                    client.call('PATCH',u+('/journalentries' if mode=='journal-api' else '/checkpoint'),payload,lease=t,compressed=True,status=204)
                    ackMs=(time.perf_counter()-ack)*1000
                    client.call('POST',u+'/complete',{'status':'succeeded'},lease=t,status=204)
                    total=(time.perf_counter()-start)*1000
                    cold=time.perf_counter();export=client.call('GET',s+'/export');coldMs=(time.perf_counter()-cold)*1000
                    warm=time.perf_counter();client.call('GET',s+'/export');warmMs=(time.perf_counter()-warm)*1000
                    assert len(export['deployment']['resources'])==n
                    assert export['deployment']['resources'][0]['outputs']['value']==d['deployment']['resources'][0]['outputs']['value']
                    return dict(mode=mode,N=n,stateBytes=len(json.dumps(d)),changeRatio=.01,rttMs=round(statistics.median(probe),3),concurrency=concurrency,run=iteration,totalMs=total,persistWaitMs=ackMs,uploadBytes=uploaded,requestCount=6,ackP95Ms=ackMs,exportColdMs=coldMs,exportWarmMs=warmMs,dbWalBytes='',maxRssBytes='')
                with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
                    for iterationID in range(-3,30):
                        samples=list(pool.map(lambda pair:iteration(pair,iterationID),stacks))
                        if iterationID>=0:rows.extend(samples)
                # Explicitly clean only stacks created by this benchmark; immutable histories stay until the isolated DB is removed.
                for s,_ in stacks:c.call('DELETE',s+'?force=true',status=204)
        with (RESULT/'bench.csv').open('w') as f:w=csv.DictWriter(f,fieldnames=list(rows[0]));w.writeheader();w.writerows(rows)
        summary=[]
        for n,concurrency in [(1000,1),(10000,1),(1000,10),(10000,10)]:
            for mode in ['full-api','journal-api']:
                samples=[r for r in rows if r['N']==n and r['concurrency']==concurrency and r['mode']==mode]
                percentile=lambda k:sorted(r[k] for r in samples)[max(0,int(len(samples)*.95)-1)]
                summary.append(dict(mode=mode,N=n,concurrency=concurrency,samples=len(samples),ackP95Ms=percentile('ackP95Ms'),exportColdP95Ms=percentile('exportColdMs'),exportWarmP95Ms=percentile('exportWarmMs'),meanUploadBytes=statistics.mean(r['uploadBytes'] for r in samples),totalP95Ms=percentile('totalMs')))
        (RESULT/'bench.json').write_text(json.dumps({'scope':'API storage microbenchmark; one full checkpoint or one journal batch per 1% update. Does not measure CLI per-resource persistence or S3/DIY. No export cache is claimed. DB WAL/RSS columns not collected.','warmups':3,'iterations':30,'measuredRoundTripMs':statistics.median(probe),'latencyProxy':'10ms before forwarding and 10ms before responding','results':summary},indent=2))
    finally:proxy.shutdown();thread.join();proxy.server_close()
