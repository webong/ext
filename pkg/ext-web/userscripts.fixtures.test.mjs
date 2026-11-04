import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { createUserscriptRegistry } from './userscripts.mjs';

// The rule for a userscript ID is written twice: in Go (res/web/host/artifact/userscript, whose tests run
// the shared fixtures in res/web/testdata/userscript) and in this registry. Go, Swift and Java already run
// record.json; this runs its ID cases against the registry so the copies cannot drift apart.
const web = new URL('../../res/web/', import.meta.url);
const inCheckout = existsSync(new URL('host/go.mod', web));
const skip = !inCheckout && 'res/web/testdata is not part of this package; run the test in a checkout of the repository';

const registry = createUserscriptRegistry({
  native: { getScripts: async () => [], register: async () => {}, update: async () => {}, unregister: async () => {} },
  storage: { get: async () => ({}), set: async () => {} },
  namespace: 'reader',
  prepare: () => { throw new Error('not used'); },
  authorize: async () => {},
});

test('the registry accepts and rejects userscript IDs as the shared fixtures do', { skip }, async () => {
  const { cases } = JSON.parse(readFileSync(new URL('testdata/userscript/v1alpha1/record.json', web), 'utf8'));
  let rejected = 0;
  for (const { name, record, error } of cases) {
    // remove() validates the ID first, then reports an unknown script: 'invalid' means the ID was refused,
    // 'unavailable' means it was accepted and simply is not installed.
    const code = error === 'invalid_id' ? 'invalid' : 'unavailable';
    await assert.rejects(registry.remove(record.id), { code }, `${name}: ${JSON.stringify(record.id.slice(0, 20))} (${record.id.length} characters)`);
    if (code === 'invalid') rejected++;
  }
  assert.ok(rejected >= 2, 'the fixtures must keep covering rejected IDs');
});

test('a registration ID is the ID behind a namespaced prefix and stays within 128 characters', () => {
  // An intended limit, documented in the README and userscripts.d.mts. The Go rule allows IDs of up to 128
  // characters, but the browser registration ID derived from one has the prefix in front (and '.' and '_'
  // take three characters each), so the longest ID the registry can register depends on its namespace.
  // The registry refuses an over-long one before registering anything; nothing records what the
  // browsers accept, so the cap is kept until the loaded-extension checks show a larger one is safe.
  const prefix = registry.registrationId('x').length - 1;
  assert.equal(registry.registrationId('a'.repeat(128 - prefix)).length, 128);
  assert.throws(() => registry.registrationId('a'.repeat(129 - prefix)), { code: 'invalid' });
  assert.throws(() => registry.registrationId('a'.repeat(128)), { code: 'invalid' });
});
