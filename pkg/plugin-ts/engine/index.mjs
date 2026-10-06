import {createRequire} from 'node:module';
const require=createRequire(import.meta.url);
const json=value=>JSON.stringify(value??null);
const timeout=value=>{value??=30_000;if(!Number.isInteger(value)||value<1||value>0xffffffff)throw new TypeError('timeout must be 1..4294967295 milliseconds');return value;};
// Cancellation belongs to each callback invocation. A late promise can settle
// safely after native timeout; the addon retains no borrowed engine pointers.
function callback(fn,policy){
  if(typeof fn!=='function')throw new TypeError('callback required');
  return (raw,remaining,reply)=>{
    const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),remaining);timer.unref();
    Promise.resolve().then(()=>fn(JSON.parse(raw),{signal:controller.signal,timeout:remaining})).then(value=>{
      if(policy){reply(value===false?2:0,0,'null');return;}
      if(value?.error!==undefined)reply(0,1,json(value.error));else reply(0,0,json(value?.payload));
    }).catch(()=>reply(policy?2:7,0,'null')).finally(()=>clearTimeout(timer));
  };
}

// Resource managers: C calls this function on engine threads; the addon blocks
// the calling worker until reply. Fields are NUL separated after the operation.
function managerCallback(handlers){
  return (raw,remaining,reply)=>{
    const [op,...fields]=raw.split('\0');
    const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),remaining);timer.unref();
    const context={signal:controller.signal,timeout:remaining};
    Promise.resolve().then(()=>handlers[op]?.(fields,context)).then(value=>reply(0,0,value===undefined?'null':String(value))).catch(error=>reply(error?.status??7,0,'null')).finally(()=>clearTimeout(timer));
  };
}
const invalid=message=>Object.assign(new Error(message),{status:1});
/** Load a caller-selected addon built from this package and the C engine. */
export function loadEngine(addonPath){
 const native=require(addonPath);
 async function request(handle,operation,input,options={}){
   const duration=timeout(options.timeout),cancel=native.newCancel();
   const abort=()=>native.cancel(cancel);options.signal?.addEventListener('abort',abort,{once:true});
   if(options.signal?.aborted)abort();
   try{return JSON.parse(await native.request(handle,operation,json(input),duration,cancel));}
   finally{options.signal?.removeEventListener('abort',abort);}
 }

 async function managerRequest(handle,operation,fields,options={},lease){
   const duration=timeout(options.timeout),cancel=native.newCancel();
   const abort=()=>native.cancel(cancel);options.signal?.addEventListener('abort',abort,{once:true});
   if(options.signal?.aborted)abort();
   const [a='',b='',c='']=fields;
   try{const raw=lease===undefined?native.managerRequest(handle,operation,a,b,c,duration,cancel):native.managerRequest(handle,operation,a,b,c,duration,cancel,lease);return await raw;}
   finally{options.signal?.removeEventListener('abort',abort);}
 }
 const text=(value,name)=>{if(typeof value!=='string'||value.length===0||value.includes('\0'))throw new TypeError(name+' must be a non-empty string without NUL');return value;};
 const capacity=value=>{if(!Number.isInteger(value)||value<1||value>4096)throw new TypeError('capacity must be 1..4096');return value;};
 /** Keyed, revisioned, leased resources. Values stay alive for their leases. */
 class Instances{
   #handle;#values=new Map();#next=1;#leases=0;#closed=false;#drained=false;#destroyed=false;#destroy;
   constructor(options){
     const {create,dispose,validate}=options;
     if(typeof create!=='function')throw new TypeError('create required');
     this.#handle=native.createInstances(capacity(options.capacity??64),managerCallback({
       validate:async([config],context)=>{try{await validate?.(JSON.parse(config),context);}catch{throw invalid('invalid configuration');}},
       create:async([key,config],context)=>{const value=await create({key,config:JSON.parse(config)},context);const id=this.#next++;this.#values.set(id,value);return id;},
       dispose:async([id])=>{const key=Number(id);const value=this.#values.get(key);this.#values.delete(key);await dispose?.(value);},
     }));
   }
   configure(key,revision,config,options){return managerRequest(this.#handle,'configure',[text(key,'key'),text(revision,'revision'),json(config)],options).then(()=>undefined);}
   async acquire(key){
     if(this.#closed)throw Object.assign(new Error('closed'),{status:5});
     const lease=await managerRequest(this.#handle,'acquire',[text(key,'key')],{});
     const {revision,id}=native.leaseInfo(lease);this.#leases++;let released=false;
     return {value:this.#values.get(id),revision,release:async options=>{
       if(released)return;released=true;
       try{await managerRequest(this.#handle,'release',[],options,lease);}finally{this.#leases--;await this.#maybeDestroy();}
     }};
   }
   remove(key){return managerRequest(this.#handle,'remove',[text(key,'key')]).then(()=>undefined);}
   /** Stops admission and waits for leases. A timeout leaves the manager closed
    * but undrained; release leases and call close again. */
   async close(options){
     this.#closed=true;
     if(this.#destroyed)return;
     await managerRequest(this.#handle,'close',[],options);
     this.#drained=true;
     await this.#maybeDestroy();
   }
   async #maybeDestroy(){
     if(!this.#drained||this.#leases>0||this.#destroy||this.#destroyed)return;
     this.#destroy=managerRequest(this.#handle,'destroy',[],{timeout:60_000}).then(()=>{this.#destroyed=true;},()=>{this.#destroy=undefined;});await this.#destroy;
   }
 }
 /** Scoped pull streams with bounded capacity and expiry. */
 class Streams{
   #handle;#values=new Map();#next=1;#closed=false;
   constructor(options){
     const {open}=options;if(typeof open!=='function')throw new TypeError('open required');
     const take=id=>{const value=this.#values.get(Number(id));if(!value)throw new Error('unknown stream');return value;};
     this.#handle=native.createStreams(capacity(options.capacity??64),timeout(options.maxAge??300_000),managerCallback({
       open:async([parameters],context)=>{const stream=await open(parameters===''?null:JSON.parse(parameters),context);const id=this.#next++;this.#values.set(id,stream);return id;},
       read:async([id,limit],context)=>JSON.stringify(await take(id).read(Number(limit),context)),
       close:async([id])=>{await take(id).close?.();},
       release:async([id])=>{this.#values.delete(Number(id));},
     }));
   }
   async open(scope,parameters,options){return JSON.parse(await managerRequest(this.#handle,'open',[text(scope,'scope'),JSON.stringify(parameters??null)],options));}
   async read(scope,id,sequence,limit,options){
     if(!Number.isSafeInteger(sequence)||sequence<1||!Number.isInteger(limit)||limit<1||limit>256)throw new TypeError('sequence must be >= 1 and limit 1..256');
     return JSON.parse(await managerRequest(this.#handle,'read',[text(scope,'scope'),text(id,'id'),sequence+' '+limit],options));
   }
   remove(scope,id){return managerRequest(this.#handle,'remove',[text(scope,'scope'),text(id,'id')]).then(()=>undefined);}
   async close(options){
     if(this.#closed)return;this.#closed=true;
     await managerRequest(this.#handle,'close',[],options);
     await managerRequest(this.#handle,'destroy',[],{timeout:60_000});
   }
 }
 class Host{
   #handle;#closed=false;
   constructor(options){
     if(options.observe!==undefined&&typeof options.observe!=='function')throw new TypeError('observe must be a function');
     const observe=options.observe===undefined?undefined:raw=>{try{Promise.resolve(options.observe(JSON.parse(raw))).catch(()=>{});}catch{}};
     this.#handle=native.createHost(options.executable,json(options.descriptor),options.arguments??[],options.environment??[],callback(options.verify,true),callback(options.authorize,true),observe);
   }
   static async open(options,callOptions){const h=new Host(options);try{await h.start(callOptions);return h;}catch(e){h.close();throw e;}}
   #invoke(op,input,options){if(this.#closed)return Promise.reject(new Error('host closed'));return request(this.#handle,op,input,options);}
   start(options){return this.#invoke('start',null,options);}
   invoke(envelope,options){return this.#invoke('invoke',envelope,options);}
   call(contract,operation,payload,options){return this.#invoke('call',{contract,operation,payload},options);}
   async drain(options){await this.#invoke('drain',null,options);this.close();}
   close(){if(!this.#closed){this.#closed=true;native.closeHost(this.#handle);this.#handle=null;}}
 }
 class Guest{
   #handle;#closed=false;
   constructor(options){this.#handle=native.createGuest(json(options.descriptor),timeout(options.maxCallDuration),callback(options.handle,false));}
   descriptor(){if(this.#closed)return Promise.reject(new Error('guest closed'));return request(this.#handle,'descriptor',null);}
   // A request the engine cannot even parse (for example an unusable ID) is
   // answered like every other malformed request: as a public invalid_request
   // response, matching the Go guest, not as a failed call.
   async invoke(envelope,options){
     if(this.#closed)throw new Error('guest closed');
     try{return await request(this.#handle,'guest.invoke',envelope,options);}
     catch(error){
       if(error?.status!==1)throw error;
       const id=typeof envelope?.id==='string'?envelope.id:'';
       return {apiVersion:'ctx.plugin/v1',id,error:{code:'invalid_request',message:'request does not match selected contract'}};
     }
   }
   close(){if(!this.#closed){this.#closed=true;native.closeHost(this.#handle);this.#handle=null;}}
 }
 return {Host,Guest,Instances,Streams,service:(operation,input)=>JSON.parse(native.service(operation,json(input))),
   directoryDigest:async root=>JSON.parse(await native.integrity('digest',root,'null')),
   verifyArtifacts:async(manifest,root)=>{await native.integrity('verify',root,json(manifest));}};
}
