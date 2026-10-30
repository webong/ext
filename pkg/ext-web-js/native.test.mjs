import test from 'node:test';
import assert from 'node:assert/strict';
import { createExtensionCatalog, createTabMessenger } from './native.mjs';

function event() { const listeners = new Set(); return { addListener: x => listeners.add(x), removeListener: x => listeners.delete(x), emit: value => { for (const listener of listeners) listener(value); }, count: () => listeners.size }; }

test('extension inventory filters browser items and disposes change listeners', async () => {
  const enabled = event(); const uninstalled = event();
  const items = [{ id: 'b', name: 'B', version: '1', enabled: true, type: 'extension' }, { id: 'theme', name: 'Theme', enabled: true, type: 'theme' }, { id: 'a', name: 'A', enabled: false, type: 'extension' }];
  const catalog = createExtensionCatalog({ getAll: async () => items, get: async id => items.find(item => item.id === id), onEnabled: enabled, onUninstalled: uninstalled });
  assert.deepEqual((await catalog.list()).map(item => item.id), ['a', 'b']);
  assert.equal((await catalog.describe('b')).name, 'B');
  await assert.rejects(catalog.describe('../bad'), { code: 'invalid' });
  const changes = []; const stop = catalog.watch(change => changes.push(change));
  enabled.emit(items[0]); uninstalled.emit('a');
  assert.deepEqual(changes.map(change => change.type), ['enabled', 'uninstalled']);
  stop(); assert.equal(enabled.count(), 0); assert.equal(uninstalled.count(), 0);
});

test('tab messaging retries only a declared missing receiver on an HTTP page', async () => {
  let calls = 0; let attached = 0;
  const messenger = createTabMessenger({
    tabs: { get: async () => ({ url: 'https://example.com/' }), sendMessage: async () => { if (++calls === 1) throw new Error('missing'); return { ok: true }; } },
    attach: async () => { attached++; }, isMissingReceiver: error => error.message === 'missing',
  });
  assert.deepEqual(await messenger.send(3, { action: 'ping' }), { ok: true });
  assert.equal(calls, 2); assert.equal(attached, 1);
  await assert.rejects(messenger.send(-1, {}), { code: 'invalid' });
  await assert.rejects(messenger.send(3, { data: 'x'.repeat(1024 * 1024) }), { code: 'too_large' });
});

test('tab messaging refuses attachment on browser pages and unrelated errors', async () => {
  let attached = 0;
  const protectedTab = createTabMessenger({ tabs: { get: async () => ({ url: 'chrome://extensions' }), sendMessage: async () => { throw new Error('missing'); } }, attach: async () => { attached++; }, isMissingReceiver: () => true });
  await assert.rejects(protectedTab.send(1, {}), { code: 'denied' }); assert.equal(attached, 0);
  const unrelated = createTabMessenger({ tabs: { get: async () => ({ url: 'https://example.com/' }), sendMessage: async () => { throw new Error('other'); } }, attach: async () => { attached++; }, isMissingReceiver: error => error.message === 'missing' });
  await assert.rejects(unrelated.send(1, {}), /other/); assert.equal(attached, 0);
});
