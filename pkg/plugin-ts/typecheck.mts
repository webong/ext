import { Registry, schemaCodec, call, Session, healthMethod, type Method } from './index.mjs';
const codec = schemaCodec<{value:string}>({type:'object',properties:{value:{type:'string'}},required:['value']});
const method: Method<{value:string},{value:string}> = {contract:{name:'example.echo',version:'v1'},operation:{name:'echo'},input:codec,output:codec};
const registry = new Registry({id:'example/types',revision:'r1'}).register(method, async input => input);
registry.guest({authorize(){}});
declare const session: Session;
const output: {value:string} = await call(session,method,{value:'ok'});
await call(session,healthMethod,{});
// @ts-expect-error wrong payload type
await call(session,method,{value:123});
void output;

import {loadEngine} from './engine/index.mjs';
const engine=loadEngine('/absolute/path/ext_engine.node');
const nativeHost=await engine.Host.open({executable:'/absolute/path/guest',descriptor:{},verify:()=>true,authorize:async()=>true,observe:event=>{void event.stage;}});
await nativeHost.call({name:'example.echo',version:'v1'},'echo',{value:'ok'},{timeout:1000});
await engine.directoryDigest('/absolute/path/package');
await engine.verifyArtifacts({},'/absolute/path/package');
nativeHost.close();
