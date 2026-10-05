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
   invoke(envelope,options){if(this.#closed)return Promise.reject(new Error('guest closed'));return request(this.#handle,'guest.invoke',envelope,options);}
   close(){if(!this.#closed){this.#closed=true;native.closeHost(this.#handle);this.#handle=null;}}
 }
 return {Host,Guest,service:(operation,input)=>JSON.parse(native.service(operation,json(input))),
   directoryDigest:async root=>JSON.parse(await native.integrity('digest',root,'null')),
   verifyArtifacts:async(manifest,root)=>{await native.integrity('verify',root,json(manifest));}};
}
