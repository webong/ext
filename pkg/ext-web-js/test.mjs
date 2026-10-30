import test from 'node:test';
import assert from 'node:assert/strict';
import { createHost, createGuest, PeerError } from './index.mjs';

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
