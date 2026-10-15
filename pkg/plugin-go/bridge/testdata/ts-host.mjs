// Independent TypeScript SDK host; no C engine or Go runtime in this host.
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
import {Session,JSONLineBackend} from '../../../plugin-ts/index.mjs';
import {nodeIO} from '../../../plugin-ts/node.mjs';
const [executable,descriptorPath]=process.argv.slice(2);
const descriptor=JSON.parse(await readFile(descriptorPath,'utf8'));
const session=await Session.open(descriptor,{
 verify(){}, // Explicitly selected local test fixture.
 authorize(r){if(!['echo','public-error'].includes(r.operation))throw new Error('denied');},
 async connect(){
  const child=spawn(executable,[],{stdio:['pipe','pipe','inherit'],env:{}});
  const ended=once(child,'exit');await once(child,'spawn');
  return new JSONLineBackend(nodeIO(child.stdout,child.stdin,{async close(){child.stdin.destroy();child.stdout.destroy();child.kill();await ended;}}));
 }
});
try{
 const contract={name:'ext.conformance',version:'v1'};
 assert.deepEqual(await session.call(contract,'echo',{value:7}),{value:7});
 await assert.rejects(session.call(contract,'public-error',null),e=>e.code==='busy'&&e.retryAfterMilliseconds===10);
 console.log('Independent TypeScript host passed');
}finally{await session.close();}
