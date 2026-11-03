import test from 'node:test';
import assert from 'node:assert/strict';
import { createUserscriptRegistry } from './userscripts.mjs';

test('userscript registry authorizes changes and repairs native registrations', async () => {
  const data = new Map(); const installed = new Map(); const approvals = [];
  const storage = { async get(key) { return { [key]: data.get(key) }; }, async set(values) { for (const [key, value] of Object.entries(values)) data.set(key, value); } };
  const native = { async getScripts() { return [...installed.values()]; }, async register(scripts) { for (const script of scripts) installed.set(script.id, script); }, async update(scripts) { for (const script of scripts) installed.set(script.id, script); }, async unregister({ ids }) { for (const id of ids) installed.delete(id); } };
  let registry;
  registry = createUserscriptRegistry({ native, storage, namespace: 'reader', prepare: input => ({ id: input.id, revision: input.revision, enabled: input.enabled, registration: { id: registry.registrationId(input.id), matches: ['https://example.com/*'] } }), authorize: action => approvals.push(action) });
  await registry.install({ id: 'highlight', revision: 'a', enabled: true });
  assert.equal(installed.size, 1);
  await assert.rejects(registry.install({ id: 'highlight', revision: 'a', enabled: true }), { code: 'conflict' });
  await registry.setEnabled('highlight', false); assert.equal(installed.size, 0);
  await registry.update({ id: 'highlight', revision: 'b', enabled: true }); assert.equal(installed.size, 1);
  installed.clear(); assert.deepEqual(await registry.reconcile(), { restored: 1, removed: 0 });
  assert.equal(installed.size, 1);
  await registry.remove('highlight'); assert.equal(installed.size, 0);
  assert.deepEqual(approvals, ['install', 'update', 'update', 'remove']);
});

test('userscript plans cannot use a foreign registration ID', async () => {
  const registry = createUserscriptRegistry({ native: { getScripts: async () => [], register: async () => {}, update: async () => {}, unregister: async () => {} }, storage: { get: async () => ({}), set: async () => {} }, namespace: 'a', prepare: () => ({ id: 'script', revision: 'a', enabled: true, registration: { id: 'foreign' } }), authorize: async () => {} });
  await assert.rejects(registry.install({}), { code: 'invalid' });
});
