import test from 'node:test';
import assert from 'node:assert/strict';
import { createScopedStorage, createScriptRegistry, createRuleRegistry } from './workspace.mjs';

function storage() {
  const data = new Map();
  return { data, async get(keys) { return Object.fromEntries((Array.isArray(keys) ? keys : [keys]).map(key => [key, data.get(key)])); }, async set(values) { for (const [key, value] of Object.entries(values)) data.set(key, value); }, async remove(keys) { for (const key of keys) data.delete(key); } };
}

test('scoped storage separates namespaces and detaches values', async () => {
  const area = storage(); const a = createScopedStorage({ storage: area, namespace: 'alpha' }); const b = createScopedStorage({ storage: area, namespace: 'beta' });
  await a.set({ value: { n: 1 } });
  assert.deepEqual(await b.get(['value']), { value: null });
  const fetched = await a.get(['value']); fetched.value.n = 2;
  assert.deepEqual(await a.get(['value']), { value: { n: 1 } });
  await a.remove(['value']); assert.deepEqual(await a.get(['value']), { value: null });
  await assert.rejects(a.set({ '../bad': true }), { code: 'invalid' });
});

test('content scripts use collision-free scoped IDs and packaged files', async () => {
  const registered = []; const removed = [];
  const registry = createScriptRegistry({ namespace: 'a.b', assetPrefix: 'assets/a.b/', scripting: { registerContentScripts: async scripts => registered.push(...scripts), unregisterContentScripts: async request => removed.push(...request.ids) } });
  assert.notEqual(registry.id('a.b'), registry.id('a_2eb'));
  const id = await registry.register({ id: 'main', matches: ['https://example.com/*'], files: ['assets/a.b/main.js'] });
  assert.equal(registered[0].id, id); await registry.unregister(['main']); assert.deepEqual(removed, [id]);
  await assert.rejects(registry.register({ id: 'bad', matches: ['https://example.com/*'], files: ['other/main.js'] }), { code: 'invalid' });
});

test('network rules cannot cross namespace ownership', async () => {
  const area = storage(); const calls = []; const network = { getDynamicRules: async () => [], updateDynamicRules: async request => calls.push(request) };
  const a = createRuleRegistry({ network, storage: area, namespace: 'a' }); const b = createRuleRegistry({ network, storage: area, namespace: 'b' });
  await a.install([{ id: 3, condition: {}, action: {} }]);
  await assert.rejects(b.install([{ id: 3 }]), { code: 'denied' });
  await assert.rejects(b.remove([3]), { code: 'denied' });
  await a.remove([3]); assert.equal(calls.length, 2);
});

test('network rule update rolls back when ownership storage fails', async () => {
  const area = { async get() { return {}; }, async set() { throw new Error('disk full'); } };
  const calls = []; const network = { getDynamicRules: async () => [{ id: 7, action: { type: 'allow' } }], updateDynamicRules: async request => calls.push(request) };
  const rules = createRuleRegistry({ network, storage: area, namespace: 'a' });
  await assert.rejects(rules.install([{ id: 7, action: { type: 'block' } }]), /disk full/);
  assert.deepEqual(calls[1].addRules, [{ id: 7, action: { type: 'allow' } }]);
});
