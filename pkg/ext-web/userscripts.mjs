import { PeerError } from './index.mjs';

const identifier = value => {
  if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)) throw new PeerError('invalid', 'Invalid userscript ID');
  return value;
};
const encode = value => value.replaceAll('_', '_5f').replaceAll('.', '_2e');

/** Browser-native userscript registration around caller-reviewed plans. */
export function createUserscriptRegistry({ native, storage, namespace, prepare, authorize, onChange } = {}) {
  if (!native?.getScripts || !native?.register || !native?.update || !native?.unregister || !storage?.get || !storage?.set || typeof prepare !== 'function' || typeof authorize !== 'function') throw new PeerError('invalid', 'Userscript registry dependencies are incomplete');
  const scope = identifier(namespace);
  const prefix = `ext-us-${scope.length}-${encode(scope)}-`;
  const key = `ext.web.userscripts.${scope}.v1`;
  const registrationId = id => {
    const result = prefix + encode(identifier(id));
    if (result.length > 128) throw new PeerError('invalid', 'Registration ID too long');
    return result;
  };
  const valid = value => {
    if (!value || typeof value !== 'object' || typeof value.revision !== 'string' || !value.revision || typeof value.enabled !== 'boolean' || !value.registration || value.registration.id !== registrationId(value.id)) throw new PeerError('invalid', 'Invalid userscript registration plan');
    return structuredClone(value);
  };
  let queue = Promise.resolve();
  const serialize = work => { const result = queue.then(work, work); queue = result.catch(() => {}); return result; };
  const read = async () => {
    const raw = (await storage.get(key))[key];
    if (raw === undefined) return {};
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new PeerError('invalid', 'Invalid userscript catalog');
    const records = {};
    for (const [id, value] of Object.entries(raw)) { const plan = valid(value); if (id !== plan.id) throw new PeerError('invalid', 'Userscript ID mismatch'); records[id] = plan; }
    return records;
  };
  const write = records => storage.set({ [key]: records });
  const changed = (type, plan) => onChange?.(type, structuredClone(plan));
  return {
    registrationId,
    availability: async () => { try { await native.getScripts(); return true; } catch { return false; } },
    list: () => serialize(async () => Object.values(await read()).sort((a, b) => a.id.localeCompare(b.id))),
    install: value => serialize(async () => {
      const plan = valid(await prepare(value)); const records = await read();
      if (records[plan.id]) throw new PeerError('conflict', 'Userscript already installed');
      await authorize('install', plan); if (plan.enabled) await native.register([plan.registration]);
      records[plan.id] = plan; await write(records); await changed('installed', plan); return structuredClone(plan);
    }),
    update: value => serialize(async () => {
      const plan = valid(await prepare(value)); const records = await read(); const previous = records[plan.id];
      if (!previous) throw new PeerError('unavailable', 'Userscript not installed');
      await authorize('update', plan, previous);
      if (previous.enabled && plan.enabled) await native.update([plan.registration]);
      else if (previous.enabled) await native.unregister({ ids: [registrationId(plan.id)] });
      else if (plan.enabled) await native.register([plan.registration]);
      records[plan.id] = plan; await write(records); await changed('updated', plan); return structuredClone(plan);
    }),
    setEnabled: (id, enabled) => serialize(async () => {
      identifier(id); if (typeof enabled !== 'boolean') throw new PeerError('invalid', 'Enabled must be boolean');
      const records = await read(); const previous = records[id]; if (!previous) throw new PeerError('unavailable', 'Userscript not installed');
      if (previous.enabled === enabled) return structuredClone(previous);
      const plan = { ...previous, enabled }; await authorize('update', plan, previous);
      if (enabled) await native.register([plan.registration]); else await native.unregister({ ids: [registrationId(id)] });
      records[id] = plan; await write(records); await changed(enabled ? 'enabled' : 'disabled', plan); return structuredClone(plan);
    }),
    remove: id => serialize(async () => {
      identifier(id); const records = await read(); const previous = records[id]; if (!previous) throw new PeerError('unavailable', 'Userscript not installed');
      await authorize('remove', previous); if (previous.enabled) await native.unregister({ ids: [registrationId(id)] });
      delete records[id]; await write(records); await changed('removed', previous);
    }),
    reconcile: () => serialize(async () => {
      const records = await read(); const actual = await native.getScripts();
      const expected = Object.values(records).filter(plan => plan.enabled);
      const wanted = new Set(expected.map(plan => plan.registration.id));
      const present = new Set(actual.map(item => item.id));
      const orphans = [...present].filter(id => id.startsWith(prefix) && !wanted.has(id));
      const missing = expected.filter(plan => !present.has(plan.registration.id));
      if (orphans.length) await native.unregister({ ids: orphans });
      if (missing.length) await native.register(missing.map(plan => plan.registration));
      return { restored: missing.length, removed: orphans.length };
    }),
  };
}
