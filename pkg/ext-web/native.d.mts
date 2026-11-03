export type ExtensionInfo = Readonly<{ id: string; name: string; version: string; enabled: boolean }>;
export type ExtensionChange = { type: 'installed' | 'enabled' | 'disabled'; extension: ExtensionInfo } | { type: 'uninstalled'; id: string };
export type NativeEvent<T> = { addListener(listener: (value: T) => void): void; removeListener(listener: (value: T) => void): void };
export type ManagementAPI = {
  getAll(): Promise<Array<{ id: string; name: string; version?: string; enabled: boolean; type: string }>>;
  get(id: string): Promise<{ id: string; name: string; version?: string; enabled: boolean; type: string }>;
  setEnabled?(id: string, enabled: boolean): Promise<void>;
  uninstall?(id: string, options?: { showConfirmDialog?: boolean }): Promise<void>;
  getPermissionWarningsById?(id: string): Promise<string[]>;
  onInstalled?: NativeEvent<any>; onEnabled?: NativeEvent<any>; onDisabled?: NativeEvent<any>; onUninstalled?: NativeEvent<string>;
};
export function createExtensionCatalog(management?: ManagementAPI): {
  list(): Promise<ExtensionInfo[]>;
  describe(id: string): Promise<ExtensionInfo>;
  watch(listener: (change: ExtensionChange) => void): () => void;
};
export function createExtensionManager(management?: Pick<ManagementAPI, 'setEnabled' | 'uninstall' | 'getPermissionWarningsById'>): {
  setEnabled(id: string, enabled: boolean): Promise<void>;
  uninstall(id: string): Promise<void>;
  permissionWarnings(id: string): Promise<string[]>;
};
export function createTabMessenger(options: {
  tabs: { get(id: number): Promise<{ url?: string }>; sendMessage(id: number, message: unknown): Promise<unknown> };
  attach?: (tabId: number) => Promise<void>;
  isMissingReceiver?: (error: unknown) => boolean;
}): { send<T = unknown>(tabId: number, message: unknown): Promise<T> };
