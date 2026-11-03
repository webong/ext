import { PeerError } from './index.mjs';

const id = value => {
  if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)) throw new PeerError('invalid', 'Invalid identifier');
  return value;
};
const object = value => value && typeof value === 'object' && !Array.isArray(value);
const clone = value => structuredClone(value);
const queues = new WeakMap();
const serialize = (storage, operation) => {
  const previous = queues.get(storage) ?? Promise.resolve();
  const result = previous.then(operation, operation);
  queues.set(storage, result.catch(() => {}));
  return result;
};

/** Isolated keys within one browser storage area. */
export function createScopedStorage({ storage, namespace }) {
  if (!storage?.get || !storage?.set) throw new PeerError('unavailable', 'Storage API unavailable');
  const prefix = `ext.web.${id(namespace)}.`;
  return {
    async get(keys) {
      if (!Array.isArray(keys) || keys.length > 256) throw new PeerError('invalid', 'Invalid storage keys');
      const names = keys.map(id); const raw = await storage.get(names.map(key => prefix + key));
      return Object.fromEntries(names.map(key => [key, raw[prefix + key] === undefined ? null : clone(raw[prefix + key])]));
    },
    async set(values) {
      if (!object(values) || Object.keys(values).length > 256) throw new PeerError('invalid', 'Invalid storage values');
      const names = Object.keys(values).map(id);
      await storage.set(Object.fromEntries(names.map(key => [prefix + key, clone(values[key])])));
    },
    async remove(keys) {
      if (!storage.remove) throw new PeerError('unavailable', 'Storage removal unavailable');
      if (!Array.isArray(keys) || keys.length > 256) throw new PeerError('invalid', 'Invalid storage keys');
      await storage.remove(keys.map(key => prefix + id(key)));
    },
  };
}

/** Owns packaged content-script registrations in one extension namespace. */
export function createScriptRegistry({ scripting, namespace, assetPrefix }) {
  if (!scripting?.registerContentScripts || !scripting?.unregisterContentScripts) throw new PeerError('unavailable', 'Scripting API unavailable');
  const scope = id(namespace);
  if (typeof assetPrefix !== 'string' || !assetPrefix.endsWith('/') || assetPrefix.startsWith('/') || assetPrefix.includes('..')) throw new PeerError('invalid', 'Invalid asset prefix');
  const scriptId = key => {
    const encode = value => value.replaceAll('_', '_5f').replaceAll('.', '_2e');
    const result = `ext-${scope.length}-${encode(scope)}-${encode(id(key))}`;
    if (result.length > 128) throw new PeerError('invalid', 'Scoped script ID too long');
    return result;
  };
  const files = values => {
    if (!Array.isArray(values) || !values.length || values.some(file => typeof file !== 'string' || !file.startsWith(assetPrefix) || file.includes('..') || /[:?#\\]/.test(file))) throw new PeerError('invalid', 'Script file is outside asset prefix');
    return values;
  };
  return {
    id: scriptId,
    async register(spec) {
      if (!object(spec) || !Array.isArray(spec.matches) || !spec.matches.length || spec.matches.some(match => typeof match !== 'string')) throw new PeerError('invalid', 'Invalid script matches');
      const registration = { id: scriptId(spec.id), matches: [...spec.matches], js: files(spec.files), runAt: spec.runAt ?? 'document_idle', allFrames: spec.allFrames === true, world: spec.world ?? 'ISOLATED', persistAcrossSessions: spec.persistAcrossSessions !== false };
      await scripting.registerContentScripts([registration]); return registration.id;
    },
    async unregister(keys) {
      if (!Array.isArray(keys) || !keys.length || keys.length > 256) throw new PeerError('invalid', 'Invalid script IDs');
      await scripting.unregisterContentScripts({ ids: keys.map(scriptId) });
    },
  };
}

/** Durable rule ownership for independent features sharing one extension. */
export function createRuleRegistry({ network, storage, namespace }) {
  if (!network?.updateDynamicRules || !network?.getDynamicRules || !storage?.get || !storage?.set) throw new PeerError('unavailable', 'Network rules or storage unavailable');
  const owner = id(namespace); const key = 'ext.web.networkOwners.v1';
  const read = async () => { const raw = (await storage.get(key))[key]; return object(raw) ? { ...raw } : {}; };
  const ruleIds = values => {
    if (!Array.isArray(values) || !values.length || values.some(value => !Number.isSafeInteger(value) || value <= 0) || new Set(values).size !== values.length) throw new PeerError('invalid', 'Invalid rule IDs');
    return values;
  };
  return {
    install(rules) {
      if (!Array.isArray(rules) || rules.some(rule => !object(rule))) throw new PeerError('invalid', 'Invalid network rules');
      const ids = ruleIds(rules.map(rule => rule.id));
      return serialize(storage, async () => {
        const owners = await read();
        for (const ruleId of ids) if (owners[ruleId] && owners[ruleId] !== owner) throw new PeerError('denied', `Rule ${ruleId} belongs to another namespace`);
        const previous = (await network.getDynamicRules()).filter(rule => ids.includes(rule.id));
        await network.updateDynamicRules({ removeRuleIds: ids, addRules: clone(rules) });
        for (const ruleId of ids) owners[ruleId] = owner;
        try { await storage.set({ [key]: owners }); }
        catch (error) { await network.updateDynamicRules({ removeRuleIds: ids, addRules: previous }); throw error; }
        return ids;
      });
    },
    remove(ids) {
      ruleIds(ids);
      return serialize(storage, async () => {
        const owners = await read();
        for (const ruleId of ids) if (owners[ruleId] !== owner) throw new PeerError('denied', `Rule ${ruleId} is not owned by this namespace`);
        const previous = (await network.getDynamicRules()).filter(rule => ids.includes(rule.id));
        await network.updateDynamicRules({ removeRuleIds: ids });
        for (const ruleId of ids) delete owners[ruleId];
        try { await storage.set({ [key]: owners }); }
        catch (error) { await network.updateDynamicRules({ removeRuleIds: ids, addRules: previous }); throw error; }
        return ids;
      });
    },
  };
}
