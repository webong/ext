import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { PassThrough } from 'node:stream';
import { decodeJSON, validateDescriptor, matchHandshake, Guest, Session, Registry, JSONLineBackend, serveGuest, call, RemoteError, configurationMethod, healthMethod } from './index.mjs';
import { nodeIO } from './node.mjs';
const descriptor = decodeJSON(await readFile(new URL('../../lib/plugin/testdata/v1/descriptor.json', import.meta.url)));
test('shared frozen fixtures', async () => {
    validateDescriptor(descriptor);
    matchHandshake(descriptor, structuredClone(descriptor));
    const bad = JSON.parse(await readFile(new URL('../../lib/plugin/testdata/v1/invalid.json', import.meta.url)));
    for (const raw of bad)
        assert.throws(() => decodeJSON(raw));
});
test('typed JSON-line host and guest', async () => {
    const codec = { parse(value) {
            if (typeof value !== 'string')
                throw new Error('wrong type');
            return value;
        } };
    const method = {
        contract: {
            name: 'example.echo', version: 'v1'
        }, operation: { name: 'echo' }, input: codec, output: codec
    };
    const registry = new Registry({
        id: 'example/typed', revision: 'fixture-1'
    }).register(method, v => v);
    const guest = registry.guest({ authorize() {
        } });
    const invalid = await guest.invoke({
        apiVersion: 'ext.plugin/v1',
        id: 'invalid',
        plugin: registry.descriptor.identity,
        contract: method.contract,
        operation: method.operation.name,
        deadline: new Date(Date.now() + 5000).toISOString(),
        payload: 42
    });
    assert.equal(invalid.error.code, 'invalid_input');
    const a = new PassThrough(), b = new PassThrough();
    const server = serveGuest(nodeIO(a, b), guest);
    const backend = new JSONLineBackend(nodeIO(b, a));
    const session = await Session.open(registry.descriptor, {
        verify(d) {
            matchHandshake(registry.descriptor, d);
        }, connect() {
            return backend;
        }, authorize() {
        }
    });
    assert.equal(await call(session, method, 'hello'), 'hello');
    assert.deepEqual(await Promise.all([call(session, method, 'a'), call(session, method, 'b')]), ['a', 'b']);
    await assert.rejects(() => call(session, method, 42));
    await session.close();
    await server.catch(e => {
        if (e.code !== 'ERR_STREAM_PREMATURE_CLOSE')
            throw e;
    });
    assert.equal(session.state, 'closed');
});
test('authorization and private errors', async () => {
    const guest = new Guest(descriptor, { handler() {
            throw new Error('secret');
        } });
    const backend = {
        handshake: s => guest.handshake(s), invoke: (r, s) => guest.invoke(r, s), close() {
        }
    };
    let allowed = false;
    const session = await Session.open(descriptor, {
        verify() {
        }, connect() {
            return backend;
        }, authorize() {
            if (!allowed)
                throw new Error('denied');
        }
    });
    await assert.rejects(() => session.call(descriptor.contracts[0], 'echo', {}), /denied/);
    allowed = true;
    await assert.rejects(() => session.call({
        name: 'ext.conformance', version: 'v1'
    }, 'echo', {}), e => e instanceof RemoteError && e.code === 'operation_failed' && !e.message.includes('secret'));
    await session.close();
});

test('schema validation and bounded parser', async () => {
 const {schemaCodec}=await import('./index.mjs');
 const codec=schemaCodec({type:'object',properties:{items:{type:'array',items:{type:'integer'},maxItems:2}},required:['items']});
 assert.deepEqual(codec.parse({items:[1,2]}),{items:[1,2]});
 for(const value of [{},{items:[1.5]},{items:[1,2,3]},{items:[],extra:true}])assert.throws(()=>codec.parse(value));
 assert.throws(()=>schemaCodec({type:'object',unsupported:true}));
 assert.throws(()=>decodeJSON('9007199254740993'));
 assert.throws(()=>decodeJSON('1e1000'));
 assert.throws(()=>decodeJSON('['.repeat(66)+'0'+']'.repeat(66)));
 assert.throws(()=>decodeJSON(new Uint8Array([0xff])));
});

test('cancellation fails a session and drain deadline restores admission', async () => {
 let release;let entered;let closed=0;
 const called=new Promise(resolve=>{entered=resolve;});
 const guest=new Guest(descriptor,{handler:async()=>{entered();await new Promise(resolve=>{release=resolve;});return null;}});
 const session=await Session.open(descriptor,{verify(){},authorize(){},connect(){return {handshake:s=>guest.handshake(s),invoke:(r,s)=>guest.invoke(r,s),close(){closed++;release?.();}};}});
 const controller=new AbortController();
 const result=session.call({name:'ext.conformance',version:'v1'},'wait',{},controller.signal);
 await called;
 const drain=new AbortController();const closing=session.close(drain.signal);drain.abort();await assert.rejects(closing);assert.equal(session.state,'ready');
 controller.abort();await assert.rejects(result);assert.equal(session.state,'failed');assert.equal(closed,1);await session.abort();assert.equal(closed,1);
});

test('handshake mismatch cleans up the backend', async () => {
 let closed=0;const bad=structuredClone(descriptor);bad.identity.revision='wrong';
 await assert.rejects(()=>Session.open(descriptor,{verify(){},authorize(){},connect(){return {handshake:async()=>bad,invoke:async()=>{throw new Error('unexpected');},close(){closed++;}};}}),/mismatch/);
 assert.equal(closed,1);
});

test('selection is frozen before asynchronous verification', async () => {
 const chosen=structuredClone(descriptor);let continueVerify;let enterVerify;
 const entered=new Promise(resolve=>{enterVerify=resolve;});
 const guest=new Guest(descriptor,{handler:r=>r.payload});
 const opening=Session.open(chosen,{verify:async()=>{enterVerify();await new Promise(resolve=>{continueVerify=resolve;});},authorize(){},connect(){return {handshake:s=>guest.handshake(s),invoke:(r,s)=>guest.invoke(r,s),close(){}};}});
 await entered;chosen.identity.revision='changed';continueVerify();const session=await opening;assert.equal(session.descriptor.identity.revision,'fixture-1');await session.close();
});

test('fragmented frames and multiple frames in one chunk preserve boundaries', async () => {
 const encoder=new TextEncoder();let bytes=new Uint8Array();let closed=false;
 const io={async write(data){const request=decodeJSON(data);const payload=request.operation==='plugin.hello'?descriptor:request.payload;const response=encoder.encode(JSON.stringify({apiVersion:'ext.plugin/v1',id:request.id,payload})+'\n');const next=new Uint8Array(bytes.length+response.length);next.set(bytes);next.set(response,bytes.length);bytes=next;},async read(){if(closed)return null;const n=Math.min(7,bytes.length);const part=bytes.slice(0,n);bytes=bytes.slice(n);return part;},close(){closed=true;}};
 const backend=new JSONLineBackend(io);const s=await Session.open(descriptor,{verify(){},authorize(){},connect(){return backend;}});
 assert.equal(await s.call({name:'ext.conformance',version:'v1'},'echo','x'.repeat(12000)),'x'.repeat(12000));await s.close();
});

test('shared Go and TypeScript schema fixtures',async()=>{
 const {schemaCodec}=await import('./index.mjs');
 const cases=JSON.parse(await readFile(new URL('../../lib/plugin/testdata/schema-v1.json',import.meta.url)));
 for(const c of cases){const codec=schemaCodec(c.schema);for(const v of c.valid)assert.deepEqual(codec.parse(v),v);for(const v of c.invalid)assert.throws(()=>codec.parse(v));}
});

test('configuration and health methods match the Go contract', () => {
    assert.deepEqual(configurationMethod.contract, { name: 'ext.configuration', version: 'v1' });
    assert.deepEqual(configurationMethod.operation, { name: 'update', surface: 'configuration' });
    const ok = { instance: 'one', revision: 'r1', values: { name: 'a' } };
    assert.deepEqual(configurationMethod.input.parse(ok), ok);
    assert.deepEqual(configurationMethod.input.parse({ ...ok, instance: 'x'.repeat(256), revision: 'r'.repeat(256) }).instance.length, 256);
    for (const bad of [
        { ...ok, instance: '' }, { ...ok, revision: '' }, { ...ok, instance: 'x'.repeat(257) }, { ...ok, revision: 'r'.repeat(257) },
        { instance: 'one', revision: 'r1' }, { ...ok, extra: 1 }, { ...ok, instance: 1 }, null, [], 'x'
    ])
        assert.throws(() => configurationMethod.input.parse(bad), undefined, JSON.stringify(bad));
    assert.deepEqual(configurationMethod.output.parse({ instance: 'one', revision: 'r1' }), { instance: 'one', revision: 'r1' });
    assert.throws(() => configurationMethod.output.parse({ instance: 'one' }));
    assert.deepEqual(healthMethod.output.parse({ status: 'degraded', ready: false }), { status: 'degraded', ready: false });
    for (const status of ['', 'great', 'HEALTHY'])
        assert.throws(() => healthMethod.output.parse({ status, ready: true }));
});
