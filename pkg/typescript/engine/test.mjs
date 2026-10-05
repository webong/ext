import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,symlink,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {loadEngine} from './index.mjs';
const engine=loadEngine(process.env.CTX_ENGINE_ADDON);
const descriptor=JSON.parse(await readFile(new URL('../../../pkg/plugin/testdata/v1/descriptor.json',import.meta.url)));
function envelope(operation='echo',payload={value:7}){return {apiVersion:'ctx.plugin/v1',id:'1',plugin:descriptor.identity,contract:{name:'ctx.conformance',version:'v1'},operation,payload,deadline:new Date(Date.now()+5000).toISOString()};}
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
test('C host with async policy, generated calls and drain',{skip:!process.env.CTX_CENGINE_GUEST},async()=>{
 let verified=0,authorized=0;
 const host=await engine.Host.open({executable:process.env.CTX_CENGINE_GUEST,descriptor,verify:async()=>{verified++;return true;},authorize:async()=>{authorized++;return true;}});
 assert.equal(verified,1);const reply=await host.call({name:'ctx.conformance',version:'v1'},'echo',{value:9});assert.deepEqual(reply.payload,{value:9});assert.equal(authorized,1);await host.drain();host.close();
});
test('C host denies before launch',{skip:!process.env.CTX_CENGINE_GUEST},async()=>{
 await assert.rejects(engine.Host.open({executable:process.env.CTX_CENGINE_GUEST,descriptor,verify:()=>false,authorize:()=>true}),e=>e.status===2);
});
test('C integrity work and symlink rejection',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ctx-node-integrity-'));
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
test('C metadata observer does not expose payloads',{skip:!process.env.CTX_CENGINE_GUEST},async()=>{
 const events=[];
 const host=await engine.Host.open({executable:process.env.CTX_CENGINE_GUEST,descriptor,verify:()=>true,authorize:()=>true,observe:event=>events.push(event)});
 try{
  await host.call({name:'ctx.conformance',version:'v1'},'echo',{secret:'private payload'});
  await new Promise(resolve=>setImmediate(resolve));
  assert.ok(events.some(e=>e.stage==='verify'));
  assert.ok(events.some(e=>e.stage==='invoke'));
  assert.ok(!JSON.stringify(events).includes('private payload'));
 }finally{host.close();}
});
