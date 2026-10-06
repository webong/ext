// Build the Go example, then pass its executable path to this host.
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {resolve} from 'node:path';
import {Session,Registry,schemaCodec,call,JSONLineBackend,matchHandshake} from '../../pkg/plugin-ts/index.mjs';
import {nodeIO} from '../../pkg/plugin-ts/node.mjs';
if(!process.argv[2])throw new Error('usage: node host.mjs PATH_TO_BUILT_GO_EXAMPLE');
const executable=resolve(process.argv[2]);
const digest=async()=>createHash('sha256').update(await readFile(executable)).digest('hex');
const expected=await digest(); // Explicitly selected local development fixture.
const codec=schemaCodec({type:'object',properties:{message:{type:'string',maxLength:256}},required:['message']});
const method={contract:{name:'example.echo',version:'v1'},operation:{name:'echo',surface:'observation'},input:codec,output:codec};
const registry=new Registry({id:'example/typescript',revision:'compiled-1'}).register(method,x=>x);
const session=await Session.open(registry.descriptor,{
 async verify(d){matchHandshake(registry.descriptor,d);if(await digest()!==expected)throw new Error('fixture changed');},
 authorize(request){if(request.operation!=='echo')throw new Error('denied');},
 async connect(){
  const env={};for(const key of ['SystemRoot','TMPDIR','TEMP','TMP'])if(process.env[key])env[key]=process.env[key];
  const child=spawn(executable,['--guest'],{stdio:['pipe','pipe','inherit'],env});
  const ended=once(child,'exit');await once(child,'spawn');
  return new JSONLineBackend(nodeIO(child.stdout,child.stdin,{async close(){child.stdin.destroy();child.stdout.destroy();child.kill();await ended;}}));
 }
});
try{console.log(await call(session,method,{message:'hello from TypeScript to Go'}));}finally{await session.close();}
