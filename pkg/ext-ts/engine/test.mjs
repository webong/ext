import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,symlink,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {loadEngine,status} from './index.mjs';
const engine=loadEngine(process.env.EXT_ENGINE_ADDON);
const descriptor=JSON.parse(await readFile(new URL('../../../lib/plugin/testdata/v1/descriptor.json',import.meta.url)));
function envelope(operation='echo',payload={value:7}){return {apiVersion:'ext.plugin/v1',id:'1',plugin:descriptor.identity,contract:{name:'ext.conformance',version:'v1'},operation,payload,deadline:new Date(Date.now()+5000).toISOString()};}
test('C services and async guest dispatch',async()=>{
 assert.deepEqual(engine.service('descriptor.validate',descriptor),descriptor);
 const g=new engine.Guest({descriptor,handle:async r=>{await new Promise(resolve=>setTimeout(resolve,1));return {payload:r.payload};}});
 assert.deepEqual(await g.descriptor(),descriptor);
 assert.deepEqual((await g.invoke(envelope())).payload,{value:7});g.close();
});
test('public errors and private failure sanitization',async()=>{
 const g=new engine.Guest({descriptor,handle:async r=>{if(r.operation==='public-error')return {error:{code:'busy',message:'try again',retryAfterMilliseconds:10}};throw new Error('private secret');}});
 const publicReply=await g.invoke(envelope('public-error'));assert.equal(publicReply.error.code,'busy');assert.equal(publicReply.error.retryAfterMilliseconds,10);
 const privateReply=await g.invoke(envelope());assert.equal(privateReply.error.code,'operation_failed');assert.ok(!JSON.stringify(privateReply).includes('secret'));g.close();
});
test('abort with a late promise is safe',async()=>{
 const g=new engine.Guest({descriptor,handle:async()=>{await new Promise(r=>setTimeout(r,60));return {payload:'late'};}});
 const abort=new AbortController();setTimeout(()=>abort.abort(),5);
 await assert.rejects(g.invoke(envelope(),{signal:abort.signal}),e=>e.status===12);g.close();await new Promise(r=>setTimeout(r,80));
});
test('C host with async policy, generated calls and drain',{skip:!process.env.EXT_CENGINE_GUEST},async()=>{
 let verified=0,authorized=0;
 const host=await engine.Host.open({executable:process.env.EXT_CENGINE_GUEST,descriptor,verify:async()=>{verified++;return true;},authorize:async()=>{authorized++;return true;}});
 assert.equal(verified,1);const reply=await host.call({name:'ext.conformance',version:'v1'},'echo',{value:9});assert.deepEqual(reply.payload,{value:9});assert.equal(authorized,1);await host.drain();host.close();
});
test('C host denies before launch',{skip:!process.env.EXT_CENGINE_GUEST},async()=>{
 await assert.rejects(engine.Host.open({executable:process.env.EXT_CENGINE_GUEST,descriptor,verify:()=>false,authorize:()=>true}),e=>e.status===2);
});
test('C integrity work and symlink rejection',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ext-node-integrity-'));
 try{
   await writeFile(join(root,'data.txt'),'abc');
   const fileHash=createHash('sha256').update('abc').digest('hex');
   const expected=createHash('sha256').update(`./data.txt ${fileHash}\n`).digest('hex');
   assert.equal(await engine.directoryDigest(root),expected);
   await symlink(join(root,'data.txt'),join(root,'link'));
   await assert.rejects(engine.directoryDigest(root));
   await assert.rejects(engine.verifyArtifacts({},root));
 }finally{await rm(root,{recursive:true,force:true});}
});
test('C metadata observer does not expose payloads',{skip:!process.env.EXT_CENGINE_GUEST},async()=>{
 const events=[];
 const host=await engine.Host.open({executable:process.env.EXT_CENGINE_GUEST,descriptor,verify:()=>true,authorize:()=>true,observe:event=>events.push(event)});
 try{
  await host.call({name:'ext.conformance',version:'v1'},'echo',{secret:'private payload'});
  await new Promise(resolve=>setImmediate(resolve));
  assert.ok(events.some(e=>e.stage==='verify'));
  assert.ok(events.some(e=>e.stage==='invoke'));
  assert.ok(!JSON.stringify(events).includes('private payload'));
 }finally{host.close();}
});
test('C instance manager keeps leased values across replacement',async()=>{
 const created=[],disposed=[];
 const instances=new engine.Instances({capacity:4,validate:config=>{if(config.bad)throw new Error('rejected');},create:async({key,config})=>{const value={key,config};created.push(value);return value;},dispose:async value=>{disposed.push(value);}});
 await assert.rejects(instances.configure('db','r0',{bad:true}),e=>e.status===1);
 await instances.configure('db','r1',{n:1});
 const first=await instances.acquire('db');
 assert.deepEqual(first.value,{key:'db',config:{n:1}});assert.equal(first.revision,'r1');
 await instances.configure('db','r2',{n:2});
 const second=await instances.acquire('db');
 assert.equal(second.revision,'r2');assert.deepEqual(second.value,{key:'db',config:{n:2}});
 assert.deepEqual(disposed,[],'a leased value is never disposed');
 await first.release();await first.release();
 await second.release();
 await instances.remove('db');
 await assert.rejects(instances.acquire('db'),e=>e.status===10);
 await instances.close();
 assert.equal(created.length,2);assert.equal(disposed.length,2);
 await assert.rejects(instances.acquire('db'),e=>e.status===5);
});
test('C instance close waits for outstanding leases',async()=>{
 const disposed=[];
 const instances=new engine.Instances({create:({key})=>({key}),dispose:value=>{disposed.push(value.key);}});
 await instances.configure('a','r1',{});
 const lease=await instances.acquire('a');
 await assert.rejects(instances.close({timeout:50}),e=>e.status===6);
 await assert.rejects(instances.acquire('a'),e=>e.status===5);
 assert.deepEqual(disposed,[],'close never destroys a leased value');
 await lease.release();
 await instances.close();
 assert.deepEqual(disposed,['a']);
});
test('C stream manager scopes, sequences and cleans up',async()=>{
 const events=[];
 const streams=new engine.Streams({capacity:2,maxAge:60_000,open:async parameters=>{let at=0;const total=parameters.total;return {
   read:async limit=>{const items=[];while(items.length<limit&&at<total)items.push(at++);return {items,done:at>=total};},
   close:async()=>{events.push('close');}};}});
 const id=await streams.open('alice',{total:5});
 assert.match(id,/^[0-9a-f]{48}$/);
 assert.deepEqual(await streams.read('alice',id,1,3),{items:[0,1,2],done:false});
 assert.deepEqual(await streams.read('alice',id,2,2),{items:[3,4],done:true});
 await assert.rejects(streams.read('bob',id,1,1),e=>e.status===2);
 const other=await streams.open('alice',{total:9});
 assert.deepEqual(await streams.read('alice',other,1,1),{items:[0],done:false});
 await assert.rejects(streams.read('alice',other,9,1),e=>e.status===15);
 await streams.remove('alice',other);
 await assert.rejects(streams.read('alice',other,2,1),e=>e.status===2);
 await streams.close();
 assert.ok(events.includes('close'));
});
test('malformed requests are public invalid_request responses',async()=>{
 const g=new engine.Guest({descriptor,handle:async r=>({payload:r.payload})});
 try{
  const mutations={
   'empty id':r=>{r.id='';},'oversized id':r=>{r.id='i'.repeat(4096);},'wrong api':r=>{r.apiVersion='ext.plugin/v2';},
   'unknown operation':r=>{r.operation='nope';},'unknown contract':r=>{r.contract={name:'unknown',version:'v1'};},
   'other plugin':r=>{r.plugin={...r.plugin,id:'other'};},'no deadline':r=>{delete r.deadline;},
  };
  for(const [name,mutate] of Object.entries(mutations)){
   const r=envelope();mutate(r);const reply=await g.invoke(r);
   assert.equal(reply.error?.code,'invalid_request',name);assert.equal(reply.payload,undefined,name);
  }
  const expired=envelope();expired.deadline='2000-01-01T00:00:00Z';
  assert.equal((await g.invoke(expired)).error?.code,'operation_failed','an expired deadline is a failed operation, as in Go');
  const missing=envelope();delete missing.payload;
  assert.equal((await g.invoke(missing)).payload,null,'a missing payload is null, as in Go');
 }finally{g.close();}
});

// Progress events, reverse calls and data endpoints. The plugin is the C engine's
// scripted test guest (lib/plugin/tests/reverse.c), started as `<guest> --guest
// <mode>`; set EXT_REVERSE_TEST_GUEST to the built ext_reverse_test.
const reverseGuest=process.env.EXT_REVERSE_TEST_GUEST;
const reverseSkip=!reverseGuest;
const reverseDescriptor=()=>JSON.parse(execFileSync(reverseGuest,['--descriptor']).toString());
const run={name:'reverse.test',version:'v1'};
function services(seen){
 return {
  authorize:async(request,context)=>{seen.authorized.push(request);assert.ok(context.timeout>0);return request.operation!=='secret';},
  handle:async request=>{
   seen.handled.push(request);
   switch(request.operation){
    case 'sign':return {payload:'signed'};
    case 'empty':return {};
    case 'errok':return {error:{code:'policy',message:'refused for you',retryAfterMilliseconds:5}};
    case 'fail':throw new Error('private');
   }
   throw new Error('unexpected');
  },
  timeout:150,maxPayloadBytes:64,
 };
}
async function reverseHost(mode,reverse){
 const seen={authorized:[],handled:[]};
 const host=await engine.Host.open({executable:reverseGuest,arguments:['--guest',mode],descriptor:reverseDescriptor(),verify:()=>true,authorize:()=>true,...(reverse===false?{}:{reverse:services(seen)})});
 return {host,seen};
}
test('reverse calls are served by the host\'s policy and handler',{skip:reverseSkip},async()=>{
 let {host,seen}=await reverseHost('call:sign');
 let reply=await host.call(run,'run',{});
 assert.equal(reply.payload.reply.id,'g1');assert.equal(reply.payload.reply.payload,'signed');
 assert.equal(seen.authorized.length,1);assert.equal(seen.handled.length,1);
 const request=seen.handled[0];
 assert.equal(request.operation,'sign');assert.equal(request.surface,'signing');assert.equal(request.parent,'1');
 assert.deepEqual(request.plugin,{id:'reverse/guest',revision:'r1'});assert.deepEqual(request.payload,{n:1});
 host.close();
 // Every way a handler can answer, and what the plugin sees.
 for(const [mode,check] of [
  ['call:empty',r=>assert.equal(r.payload,null)],
  ['call:errok',r=>assert.deepEqual(r.error,{code:'policy',message:'refused for you',retryAfterMilliseconds:5})],
  ['call:fail',r=>assert.equal(r.error.code,'operation_failed')],
  ['call:secret',r=>assert.equal(r.error.code,'denied')],
  ['call:nothere',r=>assert.equal(r.error.code,'reverse_undeclared')],
 ]){
  ({host,seen}=await reverseHost(mode));
  reply=await host.call(run,'run',{});check(reply.payload.reply);
  if(mode==='call:secret')assert.equal(seen.handled.length,0);
  if(mode==='call:nothere')assert.equal(seen.authorized.length,0);
  host.close();
 }
 // A host that offers no services answers every request as unavailable.
 ({host}=await reverseHost('call:sign',false));
 reply=await host.call(run,'run',{});assert.equal(reply.payload.reply.error.code,'reverse_unavailable');host.close();
});
test('a plugin that breaks the framing ends the session',{skip:reverseSkip},async()=>{
 for(const mode of ['wrongparent','bad-id','bad-extra']){
  const {host}=await reverseHost(mode);
  await assert.rejects(host.call(run,'run',{}),e=>e.status===status.PROTOCOL,mode);
  await assert.rejects(host.call(run,'run',{}),mode);
  host.close();
 }
});
test('progress events arrive in order before the call settles',{skip:reverseSkip},async()=>{
 const {host}=await reverseHost('interleave');
 const events=[];let settled=false;
 const pending=host.call(run,'run',{},{onProgress:event=>{assert.equal(settled,false);events.push(event);}});
 const reply=await pending;settled=true;
 assert.deepEqual(events,[{kind:'log',text:'one'},{kind:'log',text:'two'}]);
 assert.equal(reply.payload.reply.payload,'signed');
 // A callback that throws does not fail the call, and a call without one works.
 const again=await host.call(run,'run',{},{onProgress:()=>{throw new Error('callback failed');}});
 assert.equal(again.payload.reply.payload,'signed');
 assert.equal((await host.call(run,'run',{})).payload.reply.payload,'signed');
 await assert.rejects(host.call(run,'run',{},{onProgress:'no'}),TypeError);
 await assert.rejects(host.call(run,'run',{},{onProgress(){},progress:{eventsPerSecond:-1}}),TypeError);
 host.close();
});
test('reverse options are checked before anything starts',()=>{
 const base={executable:'/nonexistent',descriptor,verify(){},authorize(){}};
 assert.throws(()=>new engine.Host({...base,reverse:{authorize(){}}}),TypeError);
 assert.throws(()=>new engine.Host({...base,reverse:{handle(){}}}),TypeError);
 assert.throws(()=>new engine.Host({...base,reverse:{authorize(){},handle(){},maxInFlight:-1}}),TypeError);
 assert.throws(()=>new engine.Host({...base,reverse:{authorize(){},handle(){},timeout:0}}),TypeError);
});
test('data endpoints reproduce every outcome of the shared fixture',async()=>{
 const fixture=JSON.parse(await readFile(new URL('../../../lib/plugin/testdata/v1/endpoint-cases.json',import.meta.url)));
 const outcome=fn=>{try{fn();return 'ok';}catch(error){return {[status.INVALID]:'invalid',[status.DENIED]:'denied',[status.EXPIRED]:'expired'}[error.status]??'error '+error.message;}};
 assert.equal(fixture.cases.length,213);
 const seen=new Set();
 for(const c of fixture.cases){
  assert.equal(outcome(()=>engine.endpoint.check(c.endpoint)),c.check,'check: '+c.name);
  assert.equal(outcome(()=>engine.endpoint.validate(c.endpoint,c.policy)),c.validate,'validate: '+c.name);
  seen.add(c.validate);
 }
 assert.deepEqual([...seen].sort(),['denied','expired','invalid','ok']);
 // An outcome is a status: nothing of the endpoint reaches the error.
 const secret={scheme:'ws',address:'localhost:80',token:'s3cr3t-t0ken',expiresAt:'2000-01-01T00:00:00Z'};
 try{engine.endpoint.validate(secret,{schemes:['ws'],now:'2030-01-01T00:00:00Z'});assert.fail('admitted');}
 catch(error){assert.equal(error.status,status.EXPIRED);assert.ok(!JSON.stringify(error).includes('s3cr3t')&&!error.message.includes('s3cr3t'));}
});
// The package store of the C engine through the addon. The Go store is the baseline: pkg/ext-go/cgoengine holds the C
// store to it, and this checks that the binding reaches the same behavior.
const {mkdir,readdir,stat,rename,chmod}=await import('node:fs/promises');
async function storePackage(dir,{id,revision,contract='example.read',body,tree=false,executableMain=false}){
 await rm(dir,{recursive:true,force:true});
 await mkdir(join(dir,'src'),{recursive:true});
 await writeFile(join(dir,'src','main.py'),body,{mode:executableMain?0o700:0o600});
 await chmod(join(dir,'src','main.py'),executableMain?0o700:0o600);
 const artifacts=[{name:'main',path:'src/main.py',sha256:createHash('sha256').update(body).digest('hex')}];
 if(tree){
  await mkdir(join(dir,'data','sub'),{recursive:true});
  await writeFile(join(dir,'data','x.txt'),'ex');await writeFile(join(dir,'data','sub','y.txt'),'why');
  await writeFile(join(dir,'data','run.sh'),'#!/bin/sh\n',{mode:0o700});await chmod(join(dir,'data','run.sh'),0o700);
  artifacts.push({name:'data',path:'data',sha256:(await engine.directoryDigest(join(dir,'data'))),kind:'tree'});
 }
 const manifest=JSON.stringify({apiVersion:'ext.package/v1',descriptor:{apiVersion:'ext.plugin/v1',identity:{id,revision},contracts:[{name:contract,version:'v1',operations:[{name:'read'}]}]},artifacts},null,'\t');
 await writeFile(join(dir,'plugin.json'),manifest+'\r\n\r\n');
 return dir;
}
const mode=async path=>(await stat(path)).mode&0o777;
const rejectsWith=(promise,code)=>assert.rejects(promise,e=>e.status===code);
test('C package store installs, upgrades, replaces and removes',async()=>{
 const base=await mkdtemp(join(tmpdir(),'ext-node-store-'));
 const root=join(base,'store');
 const store=new engine.PackageStore(root);
 try{
  assert.deepEqual(await store.ids(),[]);
  await rejectsWith(store.load('example/a'),status.NOT_FOUND);
  await rejectsWith(store.remove('example/a'),status.NOT_FOUND);
  await rejectsWith(store.load('../bad'),status.INVALID);
  const first=await storePackage(join(base,'s1'),{id:'example/a',revision:'r1',body:'one',tree:true});
  await rejectsWith(store.upgrade(first),status.NOT_FOUND);
  const source=await readFile(join(first,'plugin.json'),'utf8');
  assert.equal(await store.install(first),source,'the manifest is copied as it was');
  assert.equal(await readFile(join(root,'example%2fa','plugin.json'),'utf8'),source);
  await rejectsWith(store.install(first),status.EXISTS);
  assert.deepEqual(await store.ids(),['example/a']);
  assert.equal(await store.load('example/a'),source);
  assert.equal(await mode(join(root,'example%2fa')),0o700);
  assert.equal(await mode(join(root,'example%2fa','src','main.py')),0o600);
  assert.equal(await mode(join(root,'example%2fa','data','run.sh')),0o700);
  const second=await storePackage(join(base,'s2'),{id:'example/a',revision:'r2',body:'two',tree:true});
  await store.upgrade(second);
  assert.equal(await readFile(join(root,'example%2fa','src','main.py'),'utf8'),'two');
  assert.deepEqual(await readdir(root),['example%2fa'],'no staging or backup left behind');
  await rejectsWith(store.upgrade(second),status.INVALID);
  const other=await storePackage(join(base,'s3'),{id:'example/a',revision:'r3',contract:'example.write',body:'three'});
  await rejectsWith(store.upgrade(other),status.MISMATCH);
  await store.replace(other);
  assert.equal(await readFile(join(root,'example%2fa','src','main.py'),'utf8'),'three');
  await store.remove('example/a');
  assert.deepEqual(await store.ids(),[]);
 }finally{store.close();await rm(base,{recursive:true,force:true});}
});
test('C package store refuses what is not what its manifest says, and bounds sizes',async()=>{
 const base=await mkdtemp(join(tmpdir(),'ext-node-store-'));
 const root=join(base,'store');
 const store=new engine.PackageStore(root);
 try{
  const dir=await storePackage(join(base,'s'),{id:'example/c',revision:'r1',body:'cee',tree:true});
  await writeFile(join(dir,'extra.txt'),'unlisted');
  await rejectsWith(store.install(dir),status.INVALID);
  await rm(join(dir,'extra.txt'));
  await writeFile(join(dir,'src','main.py'),'changed');
  await rejectsWith(store.install(dir),status.MISMATCH);
  await writeFile(join(dir,'src','main.py'),'cee');
  await assert.rejects(store.install(join(base,'nowhere')));
  assert.deepEqual(await store.ids(),[]);
  assert.ok(!(await readdir(root).then(x=>x,()=>[])).length,'nothing left below the root');
  // directories that are not what their name says
  await store.install(dir);
  await rename(join(root,'example%2fc'),join(root,'example%2fz'));
  await rejectsWith(store.load('example/z'),status.MISMATCH);
  await rejectsWith(store.remove('example/z'),status.MISMATCH);
  await rename(join(root,'example%2fz'),join(root,'example%2fc'));
  await writeFile(join(root,'example%2fc','src','main.py'),'tampered');
  await rejectsWith(store.load('example/c'),status.MISMATCH);
  await writeFile(join(root,'stray'),'x');
  await rejectsWith(store.ids(),status.INVALID);
 }finally{store.close();await rm(base,{recursive:true,force:true});}
 // limits
 const base2=await mkdtemp(join(tmpdir(),'ext-node-store-'));
 try{
  const big=await storePackage(join(base2,'big'),{id:'example/f',revision:'r1',body:'three bytes'});
  const small=new engine.PackageStore(join(base2,'a'),{maxArtifactBytes:2});
  await rejectsWith(small.install(big),status.INVALID);small.close();
  const tree=await storePackage(join(base2,'tree'),{id:'example/g',revision:'r1',body:'ok',tree:true});
  const few=new engine.PackageStore(join(base2,'b'),{maxTreeFiles:2});
  await rejectsWith(few.install(tree),status.INVALID);few.close();
  const light=new engine.PackageStore(join(base2,'c'),{maxTreeBytes:4});
  await rejectsWith(light.install(tree),status.INVALID);light.close();
  assert.throws(()=>new engine.PackageStore(''),TypeError);
  assert.throws(()=>new engine.PackageStore(join(base2,'d'),{maxArtifactBytes:-1}),TypeError);
 }finally{await rm(base2,{recursive:true,force:true});}
});
test('C package store keeps the execute bit of a file artifact only when asked',async()=>{
 const base=await mkdtemp(join(tmpdir(),'ext-node-store-'));
 const root=join(base,'store');
 const store=new engine.PackageStore(root);
 try{
  const dir=await storePackage(join(base,'s'),{id:'example/e',revision:'r1',body:'run me',executableMain:true});
  await store.install(dir);
  assert.equal(await mode(join(root,'example%2fe','src','main.py')),0o600);
  await store.replace(dir,{preserveExecutable:true});
  assert.equal(await mode(join(root,'example%2fe','src','main.py')),0o700);
 }finally{store.close();await rm(base,{recursive:true,force:true});}
});
test('C package store serializes concurrent calls and refuses calls once closed',async()=>{
 const base=await mkdtemp(join(tmpdir(),'ext-node-store-'));
 const store=new engine.PackageStore(join(base,'store'));
 try{
  const sources=await Promise.all([0,1,2,3].map(i=>storePackage(join(base,'s'+i),{id:'example/t'+i,revision:'r1',body:'t'+i})));
  await Promise.all(sources.map(source=>store.install(source)));
  assert.deepEqual(await store.ids(),['example/t0','example/t1','example/t2','example/t3']);
  // closing with a call outstanding lets the call finish
  const pending=store.ids();store.close();
  assert.deepEqual(await pending,['example/t0','example/t1','example/t2','example/t3']);
  await rejectsWith(store.ids(),5); // closed
 }finally{store.close();await rm(base,{recursive:true,force:true});}
});
