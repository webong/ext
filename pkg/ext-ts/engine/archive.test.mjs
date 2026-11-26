// Install from an archive through the C engine: every case of the shared fixtures
// (lib/plugin/testdata/v1/archive-cases.json), which the Go stores run too.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,readdir,rm,access} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {loadEngine,status} from './index.mjs';

const engine=loadEngine(process.env.EXT_ENGINE_ADDON);
const fixtures=fileURLToPath(new URL('../../../lib/plugin/testdata/v1/',import.meta.url));
const fixture=JSON.parse(await readFile(join(fixtures,'archive-cases.json')));
const names={[status.INVALID]:'invalid',[status.MISMATCH]:'mismatch',[status.UNSUPPORTED]:'unsupported',[status.NOT_FOUND]:'not_found',[status.EXISTS]:'exists',[status.MISSING]:'missing'};
const outcome=async promise=>{
  try{return {status:'ok',value:await promise};}
  catch(error){return {status:names[error?.status]??'failed: '+error};}
};
const escape='/tmp/ext-archive-escape';
const exists=path=>access(path).then(()=>true,()=>false);

test('archive install reproduces every case of the shared fixture',async t=>{
  assert.equal(fixture.cases.length,71);
  const escapedBefore=await exists(escape);
  for(const c of fixture.cases){
    await t.test(c.name,async()=>{
      const base=await mkdtemp(join(tmpdir(),'ext-node-archive-'));
      const root=join(base,'plugins');
      const store=new engine.PackageStore(root,c.limits??{});
      try{
        for(const [n,step] of c.steps.entries()){
          const want=step.status??'ok',where=c.name+' step '+n;
          if(step.install!==undefined){
            const got=await outcome(store.installArchive(join(fixtures,step.install),step.sha256??'',step.id===undefined?{}:{id:step.id}));
            assert.equal(got.status,want,where);
            if(got.status==='ok'&&step.revision!==undefined)assert.equal(JSON.parse(got.value).identity.revision,step.revision,where);
          }else if(step.receipt!==undefined){
            const got=await outcome(store.receipt(step.receipt));
            assert.equal(got.status,want,where);
            if(got.status==='ok')assert.deepEqual(got.value,{id:step.receipt,revision:step.revision,artifact_sha256:step.sha256},where);
          }else if(step.remove!==undefined){
            assert.equal((await outcome(store.remove(step.remove))).status,want,where);
          }else if(step.damage!==undefined){
            await writeFile(join(root,step.damage.replaceAll('/','%2f'),step.path),'damaged');
          }else assert.fail(where+': unknown step');
        }
        // Nothing is left under the root but package directories and the receipts.
        const installed=new Set((await store.ids()).map(id=>id.replaceAll('/','%2f')));
        for(const name of await readdir(root).catch(()=>[]))
          assert.ok(name==='.receipts'||installed.has(name),c.name+': '+name+' left under the root');
      }finally{store.close();await rm(base,{recursive:true,force:true});}
    });
  }
  if(!escapedBefore)assert.equal(await exists(escape),false,'an archive wrote '+escape);
});

test('archive install arguments',async()=>{
  const base=await mkdtemp(join(tmpdir(),'ext-node-archive-'));
  const store=new engine.PackageStore(join(base,'plugins'));
  const zeros='0'.repeat(64);
  try{
    await assert.rejects(store.installArchive(join(base,'absent'),zeros),e=>e.status===status.MISSING);
    for(const bad of ["","https://example.invalid/a.zip","file:///tmp/a.zip"])
      await assert.rejects(store.installArchive(bad,zeros),e=>e.status===status.INVALID,bad);
    await assert.rejects(store.receipt('example/none'),e=>e.status===status.NOT_FOUND);
    await assert.rejects(store.installArchive(join(base,'absent'),7),TypeError);
    assert.throws(()=>new engine.PackageStore(join(base,'other'),{maxArchiveBytes:-1}),TypeError);
  }finally{store.close();await rm(base,{recursive:true,force:true});}
  await assert.rejects(store.installArchive('x',zeros),e=>e.status===5);
});
