// Download of a package archive over HTTPS, under the rules every binding shares
// (lib/plugin/testdata/v1/download-cases.json): only https URLs with a host and
// without user information or a fragment, for the first request and for every
// redirect; at most MAX_REDIRECTS redirects; Authorization, Cookie and Referer
// not sent after a redirect; a body no larger than the limit, whatever
// Content-Length says; and no file left behind on failure. The C engine has no
// HTTPS, so this is the binding's; the engine then installs the file.
import {request} from 'node:https';
import {open,rm} from 'node:fs/promises';

/** How many redirects a download follows. */
export const MAX_REDIRECTS=5;
/** The size limit of a download when none is given: the store's archive limit. */
export const DEFAULT_MAX_BYTES=512*1024*1024;
const INVALID=1;
const refuse=message=>Object.assign(new Error(message),{status:INVALID});
const dropped=new Set(['authorization','cookie','referer']);

// A URL is checked as written, since the WHATWG parser repairs what the rules
// refuse: it reads 'https:///x' as the host 'x' and drops an empty '#' and '@'.
function checked(raw,base){
  if(typeof raw!=='string')throw refuse('download URL must be a string');
  const scheme=/^[a-z][a-z0-9+.-]*:/i.test(raw);
  // The authority as written: of an absolute URL, or of a network-path redirect.
  const authority=scheme?/^https:\/\/([^/?#]*)/i.exec(raw):/^\/\/([^/?#]*)/.exec(raw);
  if(scheme&&authority===null)throw refuse('download requires an HTTPS URL');
  if(!scheme&&base===undefined)throw refuse('download requires an absolute HTTPS URL');
  if(authority!==null&&(authority[1]===''||authority[1].includes('@')))throw refuse('download requires an HTTPS URL with a host and without user information');
  if(raw.includes('#'))throw refuse('download URL must not have a fragment');
  let url;
  try{url=new URL(raw,base);}catch{throw refuse('download URL does not parse');}
  if(url.protocol!=='https:'||url.hostname===''||url.username!==''||url.password!=='')throw refuse('download requires an HTTPS URL with a host and without user information');
  return url;
}
const size=value=>{
  if(value===undefined||value===0)return DEFAULT_MAX_BYTES;
  if(!Number.isSafeInteger(value)||value<0)throw new TypeError('maxBytes must be a non-negative integer');
  return value;
};

function get(url,headers,options){
  return new Promise((resolve,reject)=>{
    const req=request(url,{method:'GET',headers,ca:options.ca,agent:options.agent,signal:options.signal,timeout:options.timeout},resolve);
    req.on('timeout',()=>req.destroy(new Error('download timed out')));
    req.on('error',reject);
    req.end();
  });
}
const discard=response=>{response.resume();response.on('error',()=>{});};

/**
 * Downloads `source` into the new file `destination` (never over an existing one)
 * under the shared rules. A refusal by the rules rejects with `status` INVALID (1);
 * any other failure (an HTTP status, the network, the file) rejects without it.
 */
export async function download(source,destination,options={}){
  if(typeof destination!=='string'||destination==='')throw new TypeError('destination required');
  const limit=size(options.maxBytes);
  let url=checked(source);
  let headers={...(options.headers??{})};
  let response;
  for(let redirects=0;;redirects++){
    response=await get(url,headers,options);
    const location=response.headers.location;
    if(![301,302,303,307,308].includes(response.statusCode)||location===undefined)break;
    discard(response);
    if(redirects>=MAX_REDIRECTS)throw refuse('download exceeded '+MAX_REDIRECTS+' redirects');
    url=checked(location,url);
    headers=Object.fromEntries(Object.entries(headers).filter(([name])=>!dropped.has(name.toLowerCase())));
  }
  if(response.statusCode!==200){discard(response);throw Object.assign(new Error('download: origin returned HTTP '+response.statusCode),{httpStatus:response.statusCode});}
  const declared=response.headers['content-length'];
  if(declared!==undefined&&Number(declared)>limit){discard(response);throw refuse('download exceeds size limit');}
  let file;
  try{file=await open(destination,'wx',0o600);}catch(error){discard(response);throw error;}
  let written=0,failure;
  try{
    for await(const chunk of response){
      written+=chunk.length;
      if(written>limit){failure=refuse('download exceeds size limit');break;}
      await file.write(chunk);
    }
  }catch(error){failure=error;}
  try{await file.close();}catch(error){failure??=error;}
  if(failure){response.destroy();await rm(destination,{force:true});throw failure;}
}
