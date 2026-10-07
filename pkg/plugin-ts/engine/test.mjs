import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,symlink,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {loadEngine} from './index.mjs';
const engine=loadEngine(process.env.EXT_ENGINE_ADDON);
const descriptor=JSON.parse(await readFile(new URL('../../../pkg/plugin/testdata/v1/descriptor.json',import.meta.url)));
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
