import { PeerError } from './index.mjs';

const extensionId = value => {
  if (typeof value !== 'string' || !/^[a-zA-Z0-9@._-]{1,128}$/.test(value)) throw new PeerError('invalid', 'Invalid extension ID');
  return value;
};
const tabId = value => {
  if (!Number.isSafeInteger(value) || value < 0) throw new PeerError('invalid', 'Invalid tab ID');
  return value;
};

function extensionInfo(value) {
  if (!value || value.type !== 'extension' || typeof value.name !== 'string' || typeof value.enabled !== 'boolean') throw new PeerError('invalid', 'Invalid extension description');
  return Object.freeze({ id: extensionId(value.id), name: value.name, version: String(value.version ?? ''), enabled: value.enabled });
}

/** Read-only extension inventory and lifecycle observation. Requires management permission. */
export function createExtensionCatalog(management = globalThis.browser?.management ?? globalThis.chrome?.management) {
  if (!management?.getAll || !management?.get) throw new PeerError('unavailable', 'Extension management API unavailable');
  const events = [
    ['installed', management.onInstalled], ['enabled', management.onEnabled],
    ['disabled', management.onDisabled], ['uninstalled', management.onUninstalled],
  ];
  return {
    async list() { return (await management.getAll()).filter(item => item.type === 'extension').map(extensionInfo).sort((a, b) => a.id.localeCompare(b.id)); },
    async describe(id) { return extensionInfo(await management.get(extensionId(id))); },
    watch(listener) {
      if (typeof listener !== 'function') throw new PeerError('invalid', 'Listener required');
      const bound = [];
      for (const [type, event] of events) {
        if (!event?.addListener || !event?.removeListener) continue;
        const handler = raw => {
          try {
            if (type === 'uninstalled') listener({ type, id: extensionId(raw) });
            else if (raw?.type === 'extension') listener({ type, extension: extensionInfo(raw) });
          } catch { /* Ignore invalid browser events or consumer callback errors. */ }
        };
        event.addListener(handler); bound.push(() => event.removeListener(handler));
      }
      return () => { for (const remove of bound.splice(0)) remove(); };
    },
  };
}

/** Manage installed extensions from an extension context. Requires management permission. */
export function createExtensionManager(management = globalThis.browser?.management ?? globalThis.chrome?.management) {
  if (!management) throw new PeerError('unavailable', 'Extension management API unavailable');
  return {
    setEnabled(id, enabled) {
      const selected = extensionId(id);
      if (typeof enabled !== 'boolean') throw new PeerError('invalid', 'Enabled must be a boolean');
      if (!management.setEnabled) throw new PeerError('unavailable', 'Enable/disable unavailable');
      // Call the native API before yielding: browsers may require a user gesture.
      return management.setEnabled(selected, enabled);
    },
    uninstall(id) {
      const selected = extensionId(id);
      if (!management.uninstall) throw new PeerError('unavailable', 'Uninstall unavailable');
      return management.uninstall(selected, { showConfirmDialog: true });
    },
    permissionWarnings(id) {
      const selected = extensionId(id);
      if (!management.getPermissionWarningsById) throw new PeerError('unavailable', 'Permission warnings unavailable');
      return management.getPermissionWarningsById(selected);
    },
  };
}

/** Send to an explicit tab, optionally attaching a caller-owned receiver once. */
export function createTabMessenger({ tabs, attach, isMissingReceiver } = {}) {
  if (!tabs?.get || !tabs?.sendMessage) throw new PeerError('unavailable', 'Tab messaging API unavailable');
  if (attach && (typeof attach !== 'function' || typeof isMissingReceiver !== 'function')) throw new PeerError('invalid', 'Attach requires a missing-receiver classifier');
  return {
    async send(id, message) {
      const selected = tabId(id);
      let size;
      try { size = new TextEncoder().encode(JSON.stringify(message)).length; }
      catch { throw new PeerError('invalid', 'Tab message must be JSON serializable'); }
      if (size > 1024 * 1024) throw new PeerError('too_large', 'Tab message exceeds 1 MiB');
      try { return await tabs.sendMessage(selected, message); }
      catch (error) {
        if (!attach || !isMissingReceiver(error)) throw error;
        const tab = await tabs.get(selected);
        let protocol;
        try { protocol = new URL(tab.url).protocol; } catch { throw new PeerError('unavailable', 'Tab URL unavailable'); }
        if (protocol !== 'http:' && protocol !== 'https:') throw new PeerError('denied', 'Cannot attach to this tab URL');
        await attach(selected);
        return tabs.sendMessage(selected, message);
      }
    },
  };
}
