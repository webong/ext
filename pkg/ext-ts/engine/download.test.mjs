// The download helper against every case of lib/plugin/testdata/v1/download-cases.json,
// served by a local TLS server whose certificate openssl makes at test time, with a
// plain HTTP server beside it that must never receive a request. With EXT_ENGINE_ADDON
// set it also downloads and installs an archive end to end.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,readdir,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createServer as createTLSServer} from 'node:https';
import {createServer} from 'node:http';
import {download,MAX_REDIRECTS,loadEngine,status} from './index.mjs';

const fixtures=fileURLToPath(new URL('../../../lib/plugin/testdata/v1/',import.meta.url));
const fixture=JSON.parse(await readFile(join(fixtures,'download-cases.json')));
const archives=JSON.parse(await readFile(join(fixtures,'archive-cases.json')));

let openssl=true;
try{execFileSync('openssl',['version'],{stdio:'ignore'});}catch{openssl=false;}

// Starts the two servers; `extra` adds routes to the fixture's. Each request the TLS
// origin receives is recorded with its headers.
async function servers(extra={}){
  const dir=await mkdtemp(join(tmpdir(),'ext-node-tls-'));
  execFileSync('openssl',['req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:prime256v1','-nodes','-days','1','-subj','/CN=127.0.0.1',
    '-addext','subjectAltName=IP:127.0.0.1','-keyout',join(dir,'key.pem'),'-out',join(dir,'cert.pem')],{stdio:'ignore'});
  const cert=await readFile(join(dir,'cert.pem')),key=await readFile(join(dir,'key.pem'));
  await rm(dir,{recursive:true,force:true});
  const routes={...fixture.routes,...extra},received=[];let plainHits=0;
  const plain=createServer((req,res)=>{plainHits++;res.end('payload');});
  await new Promise(resolve=>plain.listen(0,'127.0.0.1',resolve));
  const fill=value=>value.replaceAll('{origin}',origin).replaceAll('{plain}','127.0.0.1:'+plain.address().port);
  const origin_=createTLSServer({cert,key},(req,res)=>{
    received.push({path:req.url,headers:req.headers});
    const route=routes[req.url];
    if(!route){res.writeHead(404);res.end();return;}
    if(route.redirect!==undefined){res.writeHead(302,{Location:fill(route.redirect)});res.end();return;}
    const body=route.content??Buffer.from(route.body??'x'.repeat(route.bodyBytes??0));
    if(route.chunked){res.writeHead(route.status??200);for(let i=0;i<body.length;i+=256)res.write(body.subarray(i,i+256));res.end();return;}
    res.writeHead(route.status??200,{'Content-Length':body.length});res.end(body);
  });
  await new Promise(resolve=>origin_.listen(0,'127.0.0.1',resolve));
  const origin='127.0.0.1:'+origin_.address().port;
  return {ca:cert,fill,received,plainHits:()=>plainHits,close:async()=>{origin_.closeAllConnections();plain.closeAllConnections();await Promise.all([new Promise(r=>origin_.close(r)),new Promise(r=>plain.close(r))]);}};
}
const outcome=async promise=>{
  try{await promise;return 'ok';}
  catch(error){return error?.status===status.INVALID?'invalid':'failed';}
};

test('download reproduces every case of the shared fixture',{skip:!openssl&&'openssl is needed for the test certificate'},async t=>{
  assert.equal(fixture.maxRedirects,MAX_REDIRECTS);
  assert.equal(fixture.cases.length,22);
  const s=await servers();
  try{
    for(const c of fixture.cases){
      await t.test(c.name,async()=>{
        const dir=await mkdtemp(join(tmpdir(),'ext-node-download-'));
        const destination=join(dir,'archive');
        try{
          if(c.destinationExists)await writeFile(destination,'existing');
          const before=s.received.length;
          const got=await outcome(download(s.fill(c.url),destination,{ca:s.ca,maxBytes:c.maxBytes,headers:c.headers}));
          assert.equal(got,c.expect);
          const left=await readdir(dir);
          if(got==='ok')assert.equal(await readFile(destination,'utf8'),c.body);
          else if(c.destinationExists)assert.equal(await readFile(destination,'utf8'),'existing','an existing file is never changed');
          else assert.deepEqual(left,[],'a failed download leaves no file');
          if(c.absentAtLanding){
            const requests=s.received.slice(before);
            assert.ok(requests.length>1,'the case redirects');
            for(const name of Object.keys(c.headers))assert.ok(requests[0].headers[name.toLowerCase()]!==undefined,name+' is sent with the first request');
            for(const name of c.absentAtLanding)assert.equal(requests.at(-1).headers[name.toLowerCase()],undefined,name+' reached the landing request');
          }
        }finally{await rm(dir,{recursive:true,force:true});}
      });
    }
    assert.equal(s.plainHits(),0,'the plain HTTP server received a request');
  }finally{await s.close();}
});

test('download refuses before any request',async()=>{
  const dir=await mkdtemp(join(tmpdir(),'ext-node-download-'));
  try{
    for(const url of ['https://','HTTP://127.0.0.1/x','https://@127.0.0.1/x','https://127.0.0.1/x#','/relative','//127.0.0.1/x','ftp://127.0.0.1/x'])
      await assert.rejects(download(url,join(dir,'a')),e=>e.status===status.INVALID,url);
    await assert.rejects(download('https://127.0.0.1/x',join(dir,'a'),{maxBytes:-1}),TypeError);
    assert.deepEqual(await readdir(dir),[]);
  }finally{await rm(dir,{recursive:true,force:true});}
});

test('a store downloads, installs and skips with the receipt',{skip:(!openssl&&'openssl is needed for the test certificate')||(!process.env.EXT_ENGINE_ADDON&&'EXT_ENGINE_ADDON is not set')},async()=>{
  const engine=loadEngine(process.env.EXT_ENGINE_ADDON);
  const step=archives.cases.find(c=>c.name==='tar-gz-top').steps[0];
  const content=await readFile(join(fixtures,step.install));
  const s=await servers({'/archive.tar.gz':{content},'/moved':{redirect:'/archive.tar.gz'}});
  const base=await mkdtemp(join(tmpdir(),'ext-node-download-'));
  const temporaryDirectory=join(base,'tmp');
  await (await import('node:fs/promises')).mkdir(temporaryDirectory);
  const url=s.fill('https://{origin}/moved');
  try{
    const store=new engine.PackageStore(join(base,'plugins'));
    try{
      await assert.rejects(store.installArchiveFrom(url,'0'.repeat(64),{ca:s.ca,temporaryDirectory}),e=>e.status===status.MISMATCH);
      const manifest=await store.installArchiveFrom(url,step.sha256.toUpperCase(),{ca:s.ca,temporaryDirectory,id:'example/archive'});
      assert.equal(JSON.parse(manifest).descriptor.identity.revision,step.revision);
      assert.deepEqual(await store.receipt('example/archive'),{id:'example/archive',revision:step.revision,artifact_sha256:step.sha256});
      await assert.rejects(store.installArchiveFrom(s.fill('https://{origin}/missing'),step.sha256,{ca:s.ca,temporaryDirectory}),e=>e.status===undefined);
    }finally{store.close();}
    const small=new engine.PackageStore(join(base,'small'),{maxArchiveBytes:content.length-1});
    try{await assert.rejects(small.installArchiveFrom(url,step.sha256,{ca:s.ca,temporaryDirectory}),e=>e.status===status.INVALID);}
    finally{small.close();}
    assert.deepEqual(await readdir(temporaryDirectory),[],'every download directory is removed');
    assert.equal(s.plainHits(),0);
  }finally{await s.close();await rm(base,{recursive:true,force:true});}
});
