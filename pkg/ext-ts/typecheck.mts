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

import {loadEngine, download, MAX_REDIRECTS, DEFAULT_MAX_BYTES, type ArchiveReceipt, type PackageRecord, type PackageCheck, type UnverifiedListing, type ResolvedEntrypoint} from './engine/index.mjs';
const engine=loadEngine('/absolute/path/ext_engine.node');
const nativeHost=await engine.Host.open({executable:'/absolute/path/guest',descriptor:{},verify:()=>true,authorize:async()=>true,observe:event=>{void event.stage;}});
await nativeHost.call({name:'example.echo',version:'v1'},'echo',{value:'ok'},{timeout:1000});
await engine.directoryDigest('/absolute/path/package');
await engine.verifyArtifacts({},'/absolute/path/package');
nativeHost.close();
const packageStore=new engine.PackageStore('/absolute/path/store',{maxArtifactBytes:1<<20,maxTreeFiles:100});
const archiveManifest:string=await packageStore.installArchive('/absolute/path/a.tar.gz','0'.repeat(64),{id:'example/a'});
const fetchedManifest:string=await packageStore.installArchiveFrom('https://example.invalid/a.zip','0'.repeat(64),{timeout:60_000,headers:{Authorization:'Bearer x'}});
const archiveReceipt:ArchiveReceipt=await packageStore.receipt('example/a');
await download('https://example.invalid/a.zip','/absolute/path/a.zip',{maxBytes:DEFAULT_MAX_BYTES});
void archiveManifest;void fetchedManifest;void archiveReceipt.artifact_sha256;void MAX_REDIRECTS;
const installed:string=await packageStore.install('/absolute/path/package',{preserveExecutable:true});
await packageStore.upgrade('/absolute/path/package');
await packageStore.replace('/absolute/path/package');
const stored:string=await packageStore.load('example/a');
const installedIds:string[]=await packageStore.ids();
const listed:PackageRecord[]=await packageStore.list();
const checked:PackageCheck[]=await packageStore.verify();
const unverified:UnverifiedListing=await packageStore.listUnverified();
const entrypoint:ResolvedEntrypoint=await packageStore.resolveEntrypoint({id:'example/a',contract:'example',version:'v1',protocol:'example/v1',reviewedDigest:'00',os:'linux',arch:'arm64'});
await packageStore.remove('example/a');
const scopedValue:number=await packageStore.withWriter('/absolute/ext-home',async store=>{await store.ids();return 7;});
const writer=new engine.PackageWriter('/absolute/ext-home');
writer.close();
const scopedResult:string=await engine.withPackageWriter('/absolute/ext-home',()=> 'done');
packageStore.close();
const layered=new engine.LayeredPackageStore(['/absolute/path/user','/absolute/path/system'],{maxTreeFiles:100});
const served:{manifest:string;layer:number}=await layered.load('example/a');
await layered.list();await layered.verify();await layered.listUnverified();
layered.close();
void installed;void stored;void installedIds;void listed;void checked;void unverified;void entrypoint;void served;

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
