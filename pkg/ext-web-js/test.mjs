import test from 'node:test';
import assert from 'node:assert/strict';
import { createHost, createGuest, createHub, createSpoke, discoverExtension, PeerError } from './index.mjs';

function event() { const listeners = new Set(); return { addListener: x => listeners.add(x), removeListener: x => listeners.delete(x), emit: x => { for (const listener of [...listeners]) listener(x); } }; }
function messages() { const listeners = new Set(); const queued = []; return { addListener(listener) { listeners.add(listener); for (const value of queued.splice(0)) listener(value); }, removeListener: listener => listeners.delete(listener), emit(value) { if (!listeners.size) queued.push(value); else for (const listener of [...listeners]) listener(value); } }; }
function browserPair() {
  const onConnectExternal = event();
  const runtime = { onConnectExternal, connect(id, { name }) {
    assert.equal(id, 'host');
    const a = { name, sender: { id: 'guest' }, onMessage: messages(), onDisconnect: event(), postMessage(value) { queueMicrotask(() => b.onMessage.emit(value)); }, disconnect() { queueMicrotask(() => { a.onDisconnect.emit(); b.onDisconnect.emit(); }); } };
    const b = { name, onMessage: messages(), onDisconnect: event(), postMessage(value) { queueMicrotask(() => a.onMessage.emit(value)); }, disconnect: a.disconnect };
    queueMicrotask(() => onConnectExternal.emit(a)); return b;
  } };
  return runtime;
}
async function until(check) { for (let i = 0; i < 100; i++) { if (check()) return; await new Promise(resolve => setTimeout(resolve, 1)); } throw new Error('condition not reached'); }
const string = x => { if (typeof x !== 'string') throw new PeerError('invalid', 'string required'); return x; };

test('bidirectional calls, sender admission, state, events, and reconnection', async () => {
  const runtime = browserPair(); const events = []; const states = [];
  const host = createHost({ runtime, admit: sender => sender.id === 'guest', methods: { 'host.greet': { validateInput: string, validateOutput: string, handle: x => `hi ${x}` } }, onEvent: (_sender, event) => events.push(event), onState: (_sender, state) => states.push(state) });
  const guest = createGuest({ runtime, hostId: 'host', reconnectMs: 1, methods: { 'guest.echo': { validateInput: string, validateOutput: string, handle: x => x } } });
  host.start(); guest.start();
  await until(() => guest.connected() && host.peers().length === 1);
  assert.equal(await host.call('guest', 'guest.echo', 'test'), 'test');
  assert.equal(await guest.call('host.greet', 'guest'), 'hi guest');
  guest.event({ kind: 'ready' }); guest.state({ ready: true });
  await until(() => events.length && states.length);
  assert.deepEqual(events[0], { kind: 'ready' });
  assert.deepEqual(states[0], { ready: true });
  host.stop(); await until(() => !guest.connected());
  host.start(); await until(() => guest.connected() && host.peers().length === 1);
  assert.equal(await host.call('guest', 'guest.echo', 'again'), 'again');
  guest.stop(); host.stop();
});

test('denied peer is not registered', async () => {
  const runtime = browserPair(); const host = createHost({ runtime, admit: () => false });
  const guest = createGuest({ runtime, hostId: 'host', reconnectMs: 1000 });
  host.start(); guest.start(); await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(host.peers().length, 0); assert.equal(guest.connected(), false);
  guest.stop(); host.stop();
});

test('authorization, cancellation, and timeout keep the session usable', async () => {
  const runtime = browserPair();
  const host = createHost({ runtime, admit: () => true, authorize: (_sender, method) => { if (method === 'host.denied') throw new PeerError('denied', 'Denied'); }, methods: {
    'host.denied': { validateInput: string, validateOutput: string, handle: x => x },
    'host.slow': { validateInput: string, validateOutput: string, handle: (x, { signal }) => new Promise(resolve => { signal.addEventListener('abort', () => resolve('stopped'), { once: true }); }) },
    'host.echo': { validateInput: string, validateOutput: string, handle: x => x },
  } });
  const guest = createGuest({ runtime, hostId: 'host' });
  host.start(); guest.start(); await until(() => guest.connected());
  await assert.rejects(guest.call('host.denied', 'x'), { code: 'denied' });
  const controller = new AbortController();
  const cancelled = guest.call('host.slow', 'x', { signal: controller.signal });
  controller.abort(); await assert.rejects(cancelled, { code: 'cancelled' });
  await assert.rejects(guest.call('host.slow', 'x', { timeoutMs: 5 }), { code: 'timeout' });
  assert.equal(await guest.call('host.echo', 'still ready'), 'still ready');
  guest.stop(); host.stop();
});

test('invalid and oversized peer frames close the connection', async () => {
  const runtime = browserPair(); const host = createHost({ runtime, admit: () => true }); host.start();
  const port = runtime.connect('host', { name: 'ext.web.peer' });
  port.postMessage({ version: 'wrong', type: 'hello', methods: [] });
  await until(() => host.peers().length === 0);
  const second = runtime.connect('host', { name: 'ext.web.peer' });
  second.postMessage({ version: 'ext.web.peer/v1', type: 'hello', methods: [] });
  await until(() => host.peers().length === 1);
  second.postMessage({ version: 'ext.web.peer/v1', type: 'state', value: 'x'.repeat(1024 * 1024) });
  await until(() => host.peers().length === 0);
  host.stop();
});

test('new registration replaces a stale connection from the same extension', async () => {
  const runtime = browserPair(); const host = createHost({ runtime, admit: () => true }); host.start();
  const first = runtime.connect('host', { name: 'ext.web.peer' });
  let disconnected = false; first.onDisconnect.addListener(() => { disconnected = true; });
  first.postMessage({ version: 'ext.web.peer/v1', type: 'hello', methods: ['old.method'] });
  await until(() => host.peers()[0]?.methods[0] === 'old.method');
  const second = runtime.connect('host', { name: 'ext.web.peer' });
  second.postMessage({ version: 'ext.web.peer/v1', type: 'hello', methods: ['new.method'] });
  await until(() => disconnected && host.peers()[0]?.methods[0] === 'new.method');
  second.disconnect(); host.stop();
});

test('spoke registers, updates state, and replays its snapshot after host restart', async () => {
  const runtime = browserPair(); const states = [];
  const host = createHost({ runtime, admit: () => true, onState: (_sender, state) => states.push(state) });
  const spoke = createSpoke({ runtime, hostId: 'host', name: 'Example', initialState: { count: 1 }, uiPath: 'popup.html', reconnectMs: 1 });
  host.start(); spoke.start();
  await until(() => states.length === 1);
  assert.deepEqual(states[0], { type: 'register', name: 'Example', state: { count: 1 }, uiPath: 'popup.html' });
  assert.equal(spoke.updateState({ count: 2 }), true);
  await until(() => states.length === 2);
  host.stop(); host.start();
  await until(() => states.length === 3);
  assert.deepEqual(states[2].state, { count: 2 });
  spoke.stop(); host.stop();
});

test('optional discovery selects an enabled extension and changes hosts', async () => {
  const selected = await discoverExtension(ext => ext.name === 'Host', { getAll: async () => [{ id: 'off', name: 'Host', enabled: false }, { id: 'host', name: 'Host', enabled: true }] });
  assert.equal(selected, 'host');
  const runtime = browserPair(); const host = createHost({ runtime, admit: () => true });
  let found = null; const spoke = createSpoke({ runtime, name: 'Example', discover: async () => found, discoveryMs: 100000 });
  host.start(); spoke.start();
  found = 'host'; await spoke.refresh(); await until(() => spoke.connected());
  found = null; await spoke.refresh(); assert.equal(spoke.connected(), false);
  spoke.stop(); host.stop();
});

test('hub registry uses browser sender IDs and detached state snapshots', async () => {
  const runtime = browserPair(); const changes = [];
  const hub = createHub({ runtime, admit: sender => sender.id === 'guest', onSpokesChange: spokes => changes.push(spokes) });
  const spoke = createSpoke({ runtime, hostId: 'host', name: 'Notes', initialState: { notes: [] } });
  hub.start(); spoke.start(); await until(() => hub.spokes().length === 1);
  assert.equal(hub.spokes()[0].id, 'guest');
  const detached = hub.spokes(); detached[0].state.notes.push('fake');
  assert.deepEqual(hub.spokes()[0].state, { notes: [] });
  spoke.updateState({ notes: ['real'] }); await until(() => changes.length === 2);
  assert.deepEqual(hub.spokes()[0].state, { notes: ['real'] });
  spoke.stop(); await until(() => hub.spokes().length === 0); hub.stop();
});

test('late discovery cannot reconnect a stopped spoke', async () => {
  const runtime = browserPair(); const host = createHost({ runtime, admit: () => true }); host.start();
  let resolveDiscovery;
  const spoke = createSpoke({ runtime, name: 'Late', discover: () => new Promise(resolve => { resolveDiscovery = resolve; }), discoveryMs: 100000 });
  spoke.start(); await until(() => !!resolveDiscovery);
  spoke.stop(); resolveDiscovery('host');
  await new Promise(resolve => setTimeout(resolve, 1));
  assert.equal(spoke.hostId(), null); assert.equal(host.peers().length, 0);
  host.stop();
});
