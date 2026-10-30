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
const packageStore=new engine.PackageStore('/absolute/path/store',{maxArtifactBytes:1<<20,maxTreeFiles:100});
const installed:string=await packageStore.install('/absolute/path/package',{preserveExecutable:true});
await packageStore.upgrade('/absolute/path/package');
await packageStore.replace('/absolute/path/package');
const stored:string=await packageStore.load('example/a');
const installedIds:string[]=await packageStore.ids();
await packageStore.remove('example/a');
packageStore.close();
void installed;void stored;void installedIds;

import { FrameError, Limiter, splitProgressText, type CallFrames } from './index.mjs';
const withFrames = new Registry({id:'example/frames',revision:'r1'})
    .declareReverse({name:'example.host',version:'v1'},[{name:'ask'}])
    .register({...method, operation:{name:'echo',progress:true}}, async (input, signal, request, frames: CallFrames) => {
        await frames.progress({kind:'progress',completed:1,total:2});
        const answer = await frames.reverse?.call({name:'example.host',version:'v1'},'ask',{value:input.value},signal);
        void answer; void request;
        return input;
    });
withFrames.guest({authorize(){}});
const limiter = new Limiter({eventsPerSecond:5}, 0);
const admitted: number | null = limiter.admit(3, 1);
void admitted; void splitProgressText('text'); void FrameError;
