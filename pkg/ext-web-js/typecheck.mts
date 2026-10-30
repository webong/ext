import { createScopedStorage, createScriptRegistry, createRuleRegistry } from './workspace.mjs';
import { createUserscriptRegistry } from './userscripts.mjs';
import { createPageBridgeClient } from './bridge.mjs';
import { createExtensionManager, type ExtensionInfo } from './native.mjs';
import { createThemeManifest, createThemeController } from './themes.mjs';

declare const storage: { get(keys: string | string[]): Promise<Record<string, unknown>>; set(values: Record<string, unknown>): Promise<void>; remove(keys: string[]): Promise<void> };
declare const scripting: { registerContentScripts(value: Record<string, unknown>[]): Promise<void>; unregisterContentScripts(value: { ids: string[] }): Promise<void> };
declare const network: { getDynamicRules(): Promise<Record<string, unknown>[]>; updateDynamicRules(value: { removeRuleIds: number[]; addRules?: Record<string, unknown>[] }): Promise<void> };
declare const target: { addEventListener(type: 'message', listener: (event: any) => void): void; removeEventListener(type: 'message', listener: (event: any) => void): void; postMessage(value: unknown, origin: string): void };

const scoped = createScopedStorage({ storage, namespace: 'example' });
const scripts = createScriptRegistry({ scripting, namespace: 'example', assetPrefix: 'assets/example/' });
const rules = createRuleRegistry({ network, storage, namespace: 'example' });
const client = createPageBridgeClient({ target, channel: 'example.page' });
const info: ExtensionInfo = { id: 'extension', name: 'Example', version: '1', enabled: true };
void scoped; void scripts; void rules; void client; void info;
const manager = createExtensionManager({ setEnabled: async () => {}, uninstall: async () => {} });
void manager.setEnabled('guest@example.org', true);
const theme = createThemeManifest({ name: 'Night', version: '1.0.0', colors: { frame: [10, 20, 30] } });
const controller = createThemeController({ update: async () => {}, reset: async () => {} });
void theme; void controller.apply(theme.theme);
createUserscriptRegistry({ native: { getScripts: async () => [], register: async () => {}, update: async () => {}, unregister: async () => {} }, storage, namespace: 'example', prepare: () => ({ id: 'script', revision: '1', enabled: true, registration: { id: 'generated' } }), authorize: async () => {} });
