import test from 'node:test';
import assert from 'node:assert/strict';
import { connectSandboxHost, startSandboxGuest, createPageBridgeClient, createPageBridgeReceiver } from './bridge.mjs';

function pair() {
  const hostListeners = new Set(); const guestListeners = new Set();
  const host = { addEventListener(_type, listener) { hostListeners.add(listener); }, removeEventListener(_type, listener) { hostListeners.delete(listener); }, postMessage(data, _origin, ports) { queueMicrotask(() => { for (const listener of hostListeners) listener({ source: guest, data, ports }); }); } };
  const guest = { addEventListener(_type, listener) { guestListeners.add(listener); }, removeEventListener(_type, listener) { guestListeners.delete(listener); }, postMessage(data, _origin, ports) { queueMicrotask(() => { for (const listener of guestListeners) listener({ source: host, data, ports }); }); } };
  return { host, guest };
}

test('sandbox handshake binds source and nonce', async () => {
  const { host, guest } = pair(); let context;
  const dispose = startSandboxGuest({ self: guest, parent: host, nonce: 'randomnonce123', connect: (value, port) => { context = value; port.close(); } });
  const port = await connectSandboxHost({ host, guest, nonce: 'randomnonce123', context: { feature: 'viewer' } });
  assert.deepEqual(context, { feature: 'viewer' }); port.close(); dispose();
});

test('page bridge allows only declared page-safe methods and closes pending calls', async () => {
  const listeners = new Set();
  const target = { addEventListener(_type, listener) { listeners.add(listener); }, removeEventListener(_type, listener) { listeners.delete(listener); }, postMessage(data) { queueMicrotask(() => { for (const listener of listeners) listener({ source: target, data }); }); } };
  const dispose = createPageBridgeReceiver({ target, channel: 'example.page', allowedMethods: new Set(['echo']), dispatch: (_method, value) => value });
  const client = createPageBridgeClient({ target, channel: 'example.page' });
  assert.equal(await client.call('echo', 'hello'), 'hello');
  await assert.rejects(client.call('secret', {}), { code: 'remote_error' });
  await assert.rejects(client.call('echo', 'x'.repeat(1024 * 1024)), { code: 'too_large' });
  client.close(); dispose();
  await assert.rejects(client.call('echo', 'later'), { code: 'closed' });
});
